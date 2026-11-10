package wildcsv

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

// outcome is one result of Read: a record with optional field positions, or
// a ParseError.
type outcome struct {
	rec    []string
	pos    [][2]int // optional: FieldPos of each field
	err    error
	line   int
	col    int
	off    int64
	quoted bool
}

func rec(fields ...string) outcome { return outcome{rec: fields} }

func recAt(pos [][2]int, fields ...string) outcome { return outcome{rec: fields, pos: pos} }

func perr(err error, line, col int, off int64, quoted bool) outcome {
	return outcome{err: err, line: line, col: col, off: off, quoted: quoted}
}

type readCase struct {
	name  string
	in    string
	mod   func(*Options)
	want  []outcome
	check func(t *testing.T, r *Reader)
}

func baseOptions() Options {
	return Options{Dialect: &Dialect{Delimiter: ',', Quote: '"'}, Encoding: UTF8}
}

func escapeBackslash(o *Options) { o.Dialect.Escape = '\\' }
func strict(o *Options)          { o.Strict = true }
func skipSpace(o *Options)       { o.Dialect.SkipInitialSpace = true }

var replacementChar = string(utf8.RuneError)

var readCases = []readCase{
	{name: "R01", in: "a,b,c\n", want: []outcome{rec("a", "b", "c")}},
	{name: "R02", in: "a,b,c", want: []outcome{rec("a", "b", "c")}},
	{name: "R03", in: "a,b\r\nc,d\r\n", want: []outcome{recAt([][2]int{{1, 1}, {1, 3}}, "a", "b"), recAt([][2]int{{2, 1}, {2, 3}}, "c", "d")}},
	{name: "R04", in: "a,b\rc,d\r", want: []outcome{rec("a", "b"), recAt([][2]int{{2, 1}, {2, 3}}, "c", "d")}},
	{name: "R05", in: "a,b\n\n\nc,d\n", want: []outcome{rec("a", "b"), recAt([][2]int{{4, 1}, {4, 3}}, "c", "d")}},
	{name: "R06", in: "a,b,\n", want: []outcome{rec("a", "b", "")}},
	{name: "R07", in: ",\n", want: []outcome{rec("", "")}},
	{name: "R08", in: `""` + "\n", want: []outcome{rec("")}},
	{name: "R09", in: `"a,b",c` + "\n", want: []outcome{rec("a,b", "c")}},
	{name: "R10", in: "\"a\nb\",c\nd,e\n", want: []outcome{rec("a\nb", "c"), recAt([][2]int{{3, 1}, {3, 3}}, "d", "e")}},
	{name: "R11", in: "\"a\r\nb\",c\n", want: []outcome{rec("a\r\nb", "c")}},
	{name: "R12", in: `"a""b",c` + "\n", want: []outcome{rec(`a"b`, "c")}},
	{name: "R13", in: `"""",x` + "\n", want: []outcome{rec(`"`, "x")}},
	{name: "R14", in: `a"b,c` + "\n", want: []outcome{rec(`a"b`, "c")},
		check: func(t *testing.T, r *Reader) { wantStats(t, r, Stats{Records: 1, BareQuotes: 1, Encoding: UTF8}) }},
	{name: "R14 strict", in: `a"b,c` + "\n", mod: strict, want: []outcome{perr(ErrBareQuote, 1, 2, 1, false)}},
	{name: "R15", in: `"a "b" c",d` + "\n", want: []outcome{rec(`a "b" c`, "d")},
		check: func(t *testing.T, r *Reader) { wantStats(t, r, Stats{Records: 1, StrayQuotes: 2, Encoding: UTF8}) }},
	{name: "R15 strict", in: `"a "b" c",d` + "\n", mod: strict, want: []outcome{perr(ErrStrayQuote, 1, 4, 3, true)}},
	{name: "R16", in: `"abc"def,x` + "\n", want: []outcome{perr(ErrUnterminatedQuote, 1, 1, 0, true)}},
	{name: "R17", in: `"abc" ,x` + "\n", want: []outcome{rec("abc", "x")}},
	{name: "R17 strict", in: `"abc" ,x` + "\n", mod: strict, want: []outcome{perr(ErrStrayQuote, 1, 5, 4, true)}},
	{name: "R18", in: `a, "b,c", d` + "\n", mod: skipSpace, want: []outcome{recAt([][2]int{{1, 1}, {1, 4}, {1, 11}}, "a", "b,c", "d")}},
	{name: "R19", in: `a, "b,c", d` + "\n", want: []outcome{rec("a", ` "b`, `c"`, " d")},
		check: func(t *testing.T, r *Reader) { wantStats(t, r, Stats{Records: 1, BareQuotes: 2, Encoding: UTF8}) }},
	{name: "R20", in: "a,\"bc\nd,e\n", want: []outcome{perr(ErrUnterminatedQuote, 1, 3, 2, true)}},
	{name: "R21", in: `"a\"b",c` + "\n", mod: escapeBackslash, want: []outcome{rec(`a"b`, "c")}},
	{name: "R22", in: `"a\\b",c` + "\n", mod: escapeBackslash, want: []outcome{rec(`a\b`, "c")}},
	{name: "R23", in: `"C:\dir",x` + "\n", mod: escapeBackslash, want: []outcome{rec(`C:\dir`, "x")}},
	{name: "R24", in: `"a\\",b` + "\n", mod: escapeBackslash, want: []outcome{rec(`a\`, "b")}},
	{name: "R25", in: `"a""b"` + "\n", mod: escapeBackslash, want: []outcome{rec(`a"b`)}},
	{name: "R26", in: "abcde,f\ng,h\n", mod: func(o *Options) { o.Limits.MaxFieldBytes = 4 },
		want:  []outcome{perr(ErrFieldTooLarge, 1, 1, 0, false), recAt([][2]int{{2, 1}, {2, 3}}, "g", "h")},
		check: func(t *testing.T, r *Reader) { wantStats(t, r, Stats{Records: 1, Encoding: UTF8}) }},
	{name: "R27", in: "a,b,c\nd,e\n", mod: func(o *Options) { o.Limits.MaxFields = 2 },
		want: []outcome{perr(ErrTooManyFields, 1, 5, 4, false), rec("d", "e")}},
	{name: "R28", in: "abc,def\nx\n", mod: func(o *Options) { o.Limits.MaxRecordBytes = 5; o.Limits.MaxFieldBytes = 5 },
		want: []outcome{perr(ErrRecordTooLarge, 1, 1, 0, false), rec("x")}},
	{name: "R29", in: "\"abcdefgh\nij\",k\nl,m\n", mod: func(o *Options) { o.Limits.MaxFieldBytes = 4 },
		want:  []outcome{perr(ErrFieldTooLarge, 1, 1, 0, true), rec(`ij"`, "k"), rec("l", "m")},
		check: func(t *testing.T, r *Reader) { wantStats(t, r, Stats{Records: 2, BareQuotes: 1, Encoding: UTF8}) }},
	{name: "R30", in: "a,bb,ccc\n", want: []outcome{recAt([][2]int{{1, 1}, {1, 3}, {1, 6}}, "a", "bb", "ccc")},
		check: func(t *testing.T, r *Reader) {
			if got := r.InputOffset(); got != 9 {
				t.Errorf("InputOffset = %d, want 9", got)
			}
		}},
	{name: "R31", in: "é,b\n", want: []outcome{recAt([][2]int{{1, 1}, {1, 4}}, "é", "b")}},
	{name: "R32", in: "a§b\n", mod: func(o *Options) { o.Dialect.Delimiter = '§' }, want: []outcome{rec("a", "b")}},
	{name: "R33", in: " \n", want: []outcome{rec(" ")}},
	{name: "R33 skip", in: " \n", mod: skipSpace, want: []outcome{rec("")}},
	{name: "R34 empty", in: "", want: nil},
	{name: "R34 blank", in: "\n\n", want: nil},
	{name: "R35", in: "a\x00b,c\n", want: []outcome{rec("a\x00b", "c")}},
	{name: "R36", in: `"a,b",c` + "\n", mod: func(o *Options) { o.Dialect.Quote = 0 }, want: []outcome{rec(`"a`, `b"`, "c")},
		check: func(t *testing.T, r *Reader) { wantStats(t, r, Stats{Records: 1, Encoding: UTF8}) }},
	{name: "R37", in: `"abc"`, want: []outcome{rec("abc")}},
	{name: "R38", in: `"abc"  `, want: []outcome{rec("abc")}},
	{name: "R39", in: "a,", want: []outcome{rec("a", "")}},
	{name: "R40", in: "a\xffb,c\n", want: []outcome{rec("a"+replacementChar+"b", "c")},
		check: func(t *testing.T, r *Reader) { wantStats(t, r, Stats{Records: 1, InvalidUTF8: 1, Encoding: UTF8}) }},
	{name: "R40 strict", in: "a\xffb,c\n", mod: strict, want: []outcome{perr(ErrInvalidUTF8, 1, 2, 1, false)}},
	{name: "R40 run", in: "a\xff\xfe\xfdb\n", want: []outcome{rec("a" + replacementChar + "b")},
		check: func(t *testing.T, r *Reader) { wantStats(t, r, Stats{Records: 1, InvalidUTF8: 1, Encoding: UTF8}) }},
	{name: "R41", in: "\"a\rb\",c\nd\n", want: []outcome{rec("a\rb", "c"), recAt([][2]int{{3, 1}}, "d")}},
	{name: "R42", in: `"a" , "b"` + "\n", mod: func(o *Options) { skipSpace(o); strict(o) }, want: []outcome{rec("a", "b")}},
	{name: "R43", in: "a\n\"b\nc\n", want: []outcome{rec("a"), perr(ErrUnterminatedQuote, 2, 1, 2, true)}},
	{name: "escape before end", in: `"a\`, mod: escapeBackslash, want: []outcome{perr(ErrUnterminatedQuote, 1, 1, 0, true)}},
	{name: "stray then spaces then data", in: `"a" b",c` + "\n", want: []outcome{rec(`a" b`, "c")}},
	{name: "space delimiter after quote", in: `"a" "b"` + "\n", mod: func(o *Options) { o.Dialect.Delimiter = ' ' }, want: []outcome{rec("a", "b")}},
	{name: "multi-byte quote", in: "«a,b««»«,c\n", mod: func(o *Options) { o.Dialect.Quote = '«' }, want: []outcome{rec("a,b«»", "c")}},
	{name: "multi-byte quote unterminated", in: "«a,b»,c\n", mod: func(o *Options) { o.Dialect.Quote = '«' }, want: []outcome{perr(ErrUnterminatedQuote, 1, 1, 0, true)}},
	{name: "skip lines", in: "title\n\nid,x\n1,2\n", mod: func(o *Options) { o.SkipLines = 2 },
		want: []outcome{recAt([][2]int{{3, 1}, {3, 4}}, "id", "x"), rec("1", "2")}},
}

func wantStats(t *testing.T, r *Reader, want Stats) {
	t.Helper()
	if got := r.Stats(); got != want {
		t.Errorf("Stats = %+v, want %+v", got, want)
	}
}

func runRead(t *testing.T, c readCase, src io.Reader) {
	t.Helper()
	opts := baseOptions()
	d := *opts.Dialect
	opts.Dialect = &d
	if c.mod != nil {
		c.mod(&opts)
	}
	r, err := NewReader(src, &opts)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	for i, w := range c.want {
		got, err := r.Read()
		if w.err != nil {
			var pe *ParseError
			if !errors.As(err, &pe) || !errors.Is(err, w.err) {
				t.Fatalf("result %d: got %q, %v; want error %v", i, got, err, w.err)
			}
			if pe.Line != w.line || pe.Column != w.col || pe.Offset != w.off || pe.Quoted != w.quoted {
				t.Errorf("result %d: error at line %d column %d offset %d quoted %v; want %d %d %d %v",
					i, pe.Line, pe.Column, pe.Offset, pe.Quoted, w.line, w.col, w.off, w.quoted)
			}
			continue
		}
		if err != nil {
			t.Fatalf("result %d: unexpected error %v", i, err)
		}
		if !reflect.DeepEqual(got, w.rec) {
			t.Fatalf("result %d: got %q, want %q", i, got, w.rec)
		}
		for f, p := range w.pos {
			if l, col := r.FieldPos(f); l != p[0] || col != p[1] {
				t.Errorf("result %d field %d: FieldPos = (%d,%d), want (%d,%d)", i, f, l, col, p[0], p[1])
			}
		}
	}
	if got, err := r.Read(); err != io.EOF {
		t.Fatalf("after the expected results: got %q, %v; want io.EOF", got, err)
	}
	if c.check != nil {
		c.check(t, r)
	}
}

func TestRead(t *testing.T) {
	for _, c := range readCases {
		t.Run(c.name, func(t *testing.T) {
			t.Run("whole", func(t *testing.T) { runRead(t, c, strings.NewReader(c.in)) })
			t.Run("one byte", func(t *testing.T) { runRead(t, c, iotest.OneByteReader(strings.NewReader(c.in))) })
			t.Run("halves", func(t *testing.T) { runRead(t, c, iotest.HalfReader(strings.NewReader(c.in))) })
		})
	}
}

func TestAllStopsAfterUnterminatedQuote(t *testing.T) {
	opts := baseOptions()
	r, err := NewReader(strings.NewReader("a\n\"b\nc\n"), &opts)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rec, err := range r.All() {
		if err != nil {
			got = append(got, "error: "+err.Error())
			continue
		}
		got = append(got, strings.Join(rec, "|"))
	}
	want := []string{"a", "error: wildcsv: record 2, line 2, column 1: " + ErrUnterminatedQuote.Error()}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("All yielded %q, want %q", got, want)
	}
}

func TestWarn(t *testing.T) {
	var warnings []*ParseError
	opts := baseOptions()
	opts.Warn = func(e *ParseError) { warnings = append(warnings, e) }
	r, err := NewReader(strings.NewReader("a,b\n"), &opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(); err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("Warn called %d times for clean input", len(warnings))
	}
	r, err = NewReader(strings.NewReader(`a"b`+"\n"), &opts)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Read()
	if err != nil || !reflect.DeepEqual(got, []string{`a"b`}) {
		t.Fatalf("Read = %q, %v", got, err)
	}
	if len(warnings) != 1 || warnings[0].Err != ErrBareQuote || warnings[0].Line != 1 || warnings[0].Column != 2 {
		t.Fatalf("warnings = %+v", warnings)
	}
}

func TestReuseRecord(t *testing.T) {
	opts := baseOptions()
	opts.ReuseRecord = true
	r, err := NewReader(strings.NewReader("a,b\nc,d\n"), &opts)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := r.Read()
	second, _ := r.Read()
	if &first[0] != &second[0] {
		t.Error("ReuseRecord did not reuse the slice")
	}
	if !reflect.DeepEqual(second, []string{"c", "d"}) {
		t.Errorf("second = %q", second)
	}
}

func TestFieldPosPanics(t *testing.T) {
	opts := baseOptions()
	r, _ := NewReader(strings.NewReader("a\n"), &opts)
	r.Read()
	defer func() {
		if recover() == nil {
			t.Error("FieldPos(1) did not panic")
		}
	}()
	r.FieldPos(1)
}

func TestInvalidOptions(t *testing.T) {
	for _, o := range []Options{
		{Limits: Limits{MaxFields: -1}},
		{Limits: Limits{MaxFieldBytes: 10, MaxRecordBytes: 5}},
		{Dialect: &Dialect{Delimiter: '\n'}},
		{Dialect: &Dialect{Delimiter: ',', Quote: ','}},
		{Dialect: &Dialect{Delimiter: ',', Escape: '\\'}},
		{Dialect: &Dialect{Delimiter: ' ', Quote: '"', SkipInitialSpace: true}},
		{Encoding: "ebcdic"},
		{SkipLines: -1},
	} {
		if _, err := NewReader(strings.NewReader("a"), &o); !errors.Is(err, ErrInvalidOptions) {
			t.Errorf("%+v: err = %v, want ErrInvalidOptions", o, err)
		}
	}
}

type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

func TestReadErrorsPassThrough(t *testing.T) {
	boom := errors.New("boom")
	opts := baseOptions()
	opts.Limits.SampleBytes = 4
	r, err := NewReader(io.MultiReader(strings.NewReader("a,b\nc,d"), failingReader{boom}), &opts)
	if err != nil {
		t.Fatal(err)
	}
	if rec, err := r.Read(); err != nil || !reflect.DeepEqual(rec, []string{"a", "b"}) {
		t.Fatalf("first Read = %q, %v", rec, err)
	}
	if _, err := r.Read(); err != boom {
		t.Fatalf("second Read err = %v, want boom", err)
	}
	if _, err := r.Read(); err != boom {
		t.Fatalf("third Read err = %v, want boom", err)
	}
}
