// Command wildcsv tells what a tabular text file is and how to read it, lists
// every problem in it with its position, and rewrites it as RFC 4180 CSV in
// UTF-8.
//
//	wildcsv inspect [flags] [file]    what the file is and how to read it
//	wildcsv check   [flags] [file]    read all of it and list every problem
//	wildcsv convert [flags] [file]    rewrite it as RFC 4180 CSV in UTF-8
//	wildcsv version
//
// With no file, or with "-", it reads standard input. A gzip or bzip2 file is
// decompressed as it is read. With --json, standard output holds exactly one
// JSON object, also when the command fails.
//
// Exit status: 0 when the command did its work and found no errors; 1 when
// check found errors, or check or convert was given input that is not
// delimited text or failed to parse; 2 for invalid arguments, or input that
// could not be read or output that could not be written.
package main

import (
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"unicode/utf8"

	"github.com/Altitud-Studio-S-L-U/wildcsv"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usageText = `usage:
  wildcsv inspect [flags] [file]    what the file is and how to read it
  wildcsv check   [flags] [file]    read all of it and list every problem
  wildcsv convert [flags] [file]    rewrite it as RFC 4180 CSV in UTF-8
  wildcsv version

Run "wildcsv <command> -h" for the flags of a command.
`

// env carries the streams a command uses, so tests can run it in process.
type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	e := &env{stdin: stdin, stdout: stdout, stderr: stderr}
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	switch args[0] {
	case "inspect":
		return e.inspect(args[1:])
	case "check":
		return e.check(args[1:])
	case "convert":
		return e.convert(args[1:])
	case "version", "--version", "-version":
		return e.version(args[1:])
	case "help", "-h", "--help", "-help":
		fmt.Fprint(stdout, usageText)
		return 0
	}
	fmt.Fprintf(stderr, "wildcsv: unknown command %q\n\n%s", args[0], usageText)
	return 2
}

// failure is an error with the exit status and code it reports.
type failure struct {
	status int
	code   string // usage or io
	err    error
}

func (f *failure) Error() string { return f.err.Error() }

func usageErr(format string, args ...any) error {
	return &failure{status: 2, code: "usage", err: fmt.Errorf(format, args...)}
}

func ioErr(err error) error { return &failure{status: 2, code: "io", err: err} }

type errorDoc struct {
	Schema string `json:"schema"`
	Error  struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// fail reports err and returns its exit status.
func (e *env) fail(jsonOut bool, err error) int {
	var f *failure
	if !errors.As(err, &f) {
		f = &failure{status: 2, code: "io", err: err}
	}
	fmt.Fprintln(e.stderr, "wildcsv:", f.err)
	if jsonOut {
		var doc errorDoc
		doc.Schema = "wildcsv/error/v1"
		doc.Error.Code = f.code
		doc.Error.Message = f.err.Error()
		writeJSON(e.stdout, doc)
	}
	return f.status
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// common holds the flags every reading command takes.
type common struct {
	json           bool
	delimiter      string
	quote          string
	escape         string
	encoding       string
	skipLines      int
	sampleBytes    int
	maxFieldBytes  int
	maxRecordBytes int
	maxFields      int
}

func (c *common) register(fs *flag.FlagSet) {
	fs.BoolVar(&c.json, "json", false, "print one JSON object on standard output")
	fs.StringVar(&c.delimiter, "delimiter", "", "use this delimiter: a character or comma, semicolon, tab, pipe, colon, space")
	fs.StringVar(&c.quote, "quote", "", `use this quote character, or "none"`)
	fs.StringVar(&c.escape, "escape", "", `escape character inside quoted fields, or "none"; needs --delimiter`)
	fs.StringVar(&c.encoding, "encoding", "", "use this encoding: utf-8, utf-16le, utf-16be, windows-1252 (latin1)")
	fs.IntVar(&c.skipLines, "skip-lines", 0, "lines to discard before the table")
	fs.IntVar(&c.sampleBytes, "sample-bytes", 0, "bytes examined by detection (default 65536)")
	fs.IntVar(&c.maxFieldBytes, "max-field-bytes", 0, "bytes of one field (default 1048576)")
	fs.IntVar(&c.maxRecordBytes, "max-record-bytes", 0, "bytes of one record (default 4194304)")
	fs.IntVar(&c.maxFields, "max-fields", 0, "fields of one record (default 16384)")
}

var delimiterNames = map[string]rune{
	"comma": ',', "semicolon": ';', "tab": '\t', "pipe": '|', "colon": ':', "space": ' ',
}

func parseChar(flagName, v string, names map[string]rune) (rune, error) {
	if r, ok := names[strings.ToLower(v)]; ok {
		return r, nil
	}
	switch v {
	case `\t`:
		return '\t', nil
	}
	if utf8.RuneCountInString(v) != 1 {
		return 0, usageErr("--%s must be a single character, got %q", flagName, v)
	}
	r, _ := utf8.DecodeRuneInString(v)
	return r, nil
}

func (c *common) options() (*wildcsv.Options, error) {
	opts := &wildcsv.Options{
		Encoding:  wildcsv.Encoding(c.encoding),
		SkipLines: c.skipLines,
		Limits: wildcsv.Limits{
			SampleBytes:    c.sampleBytes,
			MaxFieldBytes:  c.maxFieldBytes,
			MaxRecordBytes: c.maxRecordBytes,
			MaxFields:      c.maxFields,
		},
	}
	quote, quoteSet := '"', false
	if c.quote != "" {
		quoteSet = true
		if strings.EqualFold(c.quote, "none") {
			quote = 0
		} else {
			r, err := parseChar("quote", c.quote, nil)
			if err != nil {
				return nil, err
			}
			quote = r
		}
	}
	if c.delimiter == "" {
		if c.escape != "" {
			return nil, usageErr("--escape needs --delimiter")
		}
		if quoteSet {
			opts.Quotes = []rune{quote}
		}
		return opts, nil
	}
	d, err := parseChar("delimiter", c.delimiter, delimiterNames)
	if err != nil {
		return nil, err
	}
	dialect := &wildcsv.Dialect{Delimiter: d, Quote: quote}
	if c.escape != "" && !strings.EqualFold(c.escape, "none") {
		r, err := parseChar("escape", c.escape, nil)
		if err != nil {
			return nil, err
		}
		dialect.Escape = r
	}
	opts.Dialect = dialect
	return opts, nil
}

// input is the file or standard input, decompressed if needed.
type input struct {
	name        string
	r           io.Reader
	compression string // "", "gzip" or "bzip2"
	close       func() error
}

func (e *env) open(fs *flag.FlagSet) (*input, error) {
	if fs.NArg() > 1 {
		return nil, usageErr("expected at most one file, got %d", fs.NArg())
	}
	in := &input{name: "-", close: func() error { return nil }}
	src := e.stdin
	if fs.NArg() == 1 && fs.Arg(0) != "-" {
		f, err := os.Open(fs.Arg(0))
		if err != nil {
			return nil, ioErr(err)
		}
		in.name, src, in.close = fs.Arg(0), f, f.Close
	}
	br := bufio.NewReaderSize(src, 64<<10)
	head, _ := br.Peek(4)
	switch {
	case len(head) >= 2 && head[0] == 0x1F && head[1] == 0x8B:
		gz, err := gzip.NewReader(br)
		if err != nil {
			in.close()
			return nil, ioErr(fmt.Errorf("%s: %w", in.name, err))
		}
		in.r, in.compression = gz, "gzip"
	case len(head) >= 4 && string(head[:3]) == "BZh" && head[3] >= '1' && head[3] <= '9':
		in.r, in.compression = bzip2.NewReader(br), "bzip2"
	default:
		in.r = br
	}
	return in, nil
}

// readError classifies an error from the library: data and kind problems are
// reported by the command; anything else is an input failure.
func readError(in *input, err error) error {
	var ie *wildcsv.EncodingError
	if errors.Is(err, wildcsv.ErrInvalidOptions) {
		return usageErr("%v", strings.TrimPrefix(err.Error(), "wildcsv: "))
	}
	if errors.As(err, &ie) {
		return &failure{status: 2, code: "io", err: fmt.Errorf("%s: %w", in.name, err)}
	}
	return ioErr(fmt.Errorf("%s: %w", in.name, err))
}

type versionDoc struct {
	Schema  string `json:"schema"`
	Version string `json:"version"`
	Go      string `json:"go"`
}

func moduleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(devel)"
	}
	return info.Main.Version
}

func (e *env) version(args []string) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	jsonOut := fs.Bool("json", false, "print one JSON object")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *jsonOut {
		writeJSON(e.stdout, versionDoc{Schema: "wildcsv/version/v1", Version: moduleVersion(), Go: runtime.Version()})
		return 0
	}
	fmt.Fprintf(e.stdout, "wildcsv %s (%s, %s/%s)\n", moduleVersion(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return 0
}
