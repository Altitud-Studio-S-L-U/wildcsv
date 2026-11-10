package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/Altitud-Studio-S-L-U/wildcsv"
)

type checkDoc struct {
	Schema        string         `json:"schema"`
	File          string         `json:"file"`
	Kind          string         `json:"kind"`
	Compression   *string        `json:"compression"`
	Encoding      *encodingJSON  `json:"encoding"`
	Dialect       *dialectJSON   `json:"dialect"`
	Records       int64          `json:"records"`
	Fields        fieldsJSON     `json:"fields"`
	OK            bool           `json:"ok"`
	ProblemCounts map[string]int `json:"problem_counts"`
	Problems      []problem      `json:"problems"`
	ProblemsCut   bool           `json:"problems_cut"`
}

type fieldsJSON struct {
	Min  int `json:"min"`
	Max  int `json:"max"`
	Mode int `json:"mode"`
}

type problem struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Record   int    `json:"record"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Message  string `json:"message"`
}

var problemCodes = map[error]string{
	wildcsv.ErrBareQuote:         "bare_quote",
	wildcsv.ErrStrayQuote:        "stray_quote",
	wildcsv.ErrInvalidUTF8:       "invalid_utf8",
	wildcsv.ErrUnterminatedQuote: "unterminated_quote",
	wildcsv.ErrFieldTooLarge:     "field_too_large",
	wildcsv.ErrRecordTooLarge:    "record_too_large",
	wildcsv.ErrTooManyFields:     "too_many_fields",
}

var problemMessages = map[string]string{
	"bare_quote":         "quote character in an unquoted field; it was read as data",
	"stray_quote":        "quote character inside a quoted field neither doubles nor closes it; it was read as data",
	"invalid_utf8":       "invalid UTF-8 sequence; it was replaced with U+FFFD",
	"unterminated_quote": "the quoted field that starts here is not closed before the end of the input",
	"field_too_large":    "field is larger than --max-field-bytes; the rest of the line was skipped",
	"record_too_large":   "record is larger than --max-record-bytes; the rest of the line was skipped",
	"too_many_fields":    "record has more fields than --max-fields; the rest of the line was skipped",
}

// collector keeps the first problems and counts all of them.
type collector struct {
	doc *checkDoc
	max int
}

func (c *collector) add(p problem) {
	c.doc.ProblemCounts[p.Code]++
	if p.Severity == "error" {
		c.doc.OK = false
	}
	if len(c.doc.Problems) < c.max {
		c.doc.Problems = append(c.doc.Problems, p)
	} else {
		c.doc.ProblemsCut = true
	}
}

func (c *collector) parseError(pe *wildcsv.ParseError, severity string) {
	code := problemCodes[pe.Err]
	c.add(problem{Code: code, Severity: severity, Record: pe.Record, Line: pe.Line, Column: pe.Column, Message: problemMessages[code]})
}

func (e *env) check(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	var c common
	c.register(fs)
	strict := fs.Bool("strict", false, "report quoting defects and invalid UTF-8 as errors")
	maxProblems := fs.Int("max-problems", 100, "problems to list; all are counted")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	doc, err := e.checkDoc(fs, &c, *strict, *maxProblems)
	if err != nil {
		return e.fail(c.json, err)
	}
	if c.json {
		if err := writeJSON(e.stdout, doc); err != nil {
			return e.fail(false, ioErr(err))
		}
	} else if err := printCheck(e.stdout, doc); err != nil {
		return e.fail(false, ioErr(err))
	}
	if !doc.OK {
		return 1
	}
	return 0
}

func (e *env) checkDoc(fs *flag.FlagSet, c *common, strict bool, maxProblems int) (*checkDoc, error) {
	if maxProblems < 0 {
		return nil, usageErr("--max-problems must not be negative")
	}
	opts, err := c.options()
	if err != nil {
		return nil, err
	}
	in, err := e.open(fs)
	if err != nil {
		return nil, err
	}
	defer in.close()

	doc := &checkDoc{Schema: "wildcsv/check/v1", File: in.name, OK: true, ProblemCounts: map[string]int{}, Problems: []problem{}}
	if in.compression != "" {
		doc.Compression = &in.compression
	}
	col := &collector{doc: doc, max: maxProblems}
	opts.Strict = strict
	if !strict {
		opts.Warn = func(pe *wildcsv.ParseError) { col.parseError(pe, "warning") }
	}
	r, err := wildcsv.NewReader(in.r, opts)
	var ke *wildcsv.KindError
	if errors.As(err, &ke) {
		doc.Kind = string(ke.Kind)
		col.add(problem{Code: "not_delimited", Severity: "error", Message: fmt.Sprintf("the input is %s, not delimited text", ke.Kind)})
		return doc, nil
	}
	if err != nil {
		return nil, readError(in, err)
	}
	det := r.Detection()
	doc.Kind = string(wildcsv.KindDelimited)
	if det.Kind == wildcsv.KindEmpty {
		doc.Kind = string(wildcsv.KindEmpty)
	}
	doc.Encoding = toEncodingJSON(det)
	dj := toDialectJSON(det.Dialect)
	doc.Dialect = &dj
	if opts.Dialect == nil && det.Kind == wildcsv.KindDelimited && det.Confidence < lowConfidence {
		col.add(problem{Code: "low_confidence", Severity: "warning",
			Message: fmt.Sprintf("the dialect was detected with confidence %.3f; pass --delimiter if it is wrong", det.Confidence)})
	}
	columns := len(det.Columns)
	counts := map[int]int64{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		var pe *wildcsv.ParseError
		if errors.As(err, &pe) {
			col.parseError(pe, "error")
			continue
		}
		if err != nil {
			return nil, readError(in, err)
		}
		counts[len(rec)]++
		if columns > 0 && len(rec) != columns {
			line, _ := r.FieldPos(0)
			col.add(problem{Code: "ragged_record", Severity: "error", Record: int(r.Stats().Records), Line: line, Column: 1,
				Message: fmt.Sprintf("record has %d fields; the sampled records have %d", len(rec), columns)})
		}
	}
	doc.Records = r.Stats().Records
	if det.Kind == wildcsv.KindDelimited && det.Encoding != r.Stats().Encoding && r.Stats().Encoding != "" {
		doc.Encoding.Name = string(r.Stats().Encoding)
	}
	keys := make([]int, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) > 0 {
		doc.Fields.Min, doc.Fields.Max = keys[0], keys[len(keys)-1]
		best := int64(-1)
		for _, k := range keys {
			if counts[k] > best {
				doc.Fields.Mode, best = k, counts[k]
			}
		}
	}
	return doc, nil
}

func printCheck(w io.Writer, doc *checkDoc) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "file\t%s\n", doc.File)
	fmt.Fprintf(tw, "kind\t%s\n", doc.Kind)
	if doc.Dialect != nil {
		fmt.Fprintf(tw, "records\t%d\n", doc.Records)
		fmt.Fprintf(tw, "fields\tmin %d, max %d, most common %d\n", doc.Fields.Min, doc.Fields.Max, doc.Fields.Mode)
	}
	status := "no errors"
	if !doc.OK {
		status = "errors found"
	}
	fmt.Fprintf(tw, "result\t%s\n", status)
	codes := make([]string, 0, len(doc.ProblemCounts))
	for code := range doc.ProblemCounts {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	for _, code := range codes {
		fmt.Fprintf(tw, "problems\t%s: %d\n", code, doc.ProblemCounts[code])
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(doc.Problems) > 0 {
		fmt.Fprintln(w)
	}
	for _, p := range doc.Problems {
		where := "input"
		if p.Line > 0 {
			where = fmt.Sprintf("line %d, column %d", p.Line, p.Column)
		}
		fmt.Fprintf(w, "%s: %s %s: %s\n", where, p.Severity, p.Code, p.Message)
	}
	if doc.ProblemsCut {
		fmt.Fprintln(w, "more problems were counted than listed; raise --max-problems to list them")
	}
	return nil
}
