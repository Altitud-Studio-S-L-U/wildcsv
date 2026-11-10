package wildcsv

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"
)

func addReadSeeds(f *testing.F) {
	for _, c := range readCases {
		f.Add([]byte(c.in), uint16(0))
		f.Add([]byte(c.in), uint16(0xFFFF))
	}
}

// fuzzOptions derives a dialect, a mode and small limits from flags.
func fuzzOptions(flags uint16) Options {
	delims := []rune{',', ';', '\t', '|', ' ', '§'}
	quotes := []rune{'"', '\'', 0, '«'}
	d := Dialect{Delimiter: delims[int(flags)%len(delims)], Quote: quotes[int(flags>>3)%len(quotes)]}
	if flags&0x20 != 0 && d.Quote != 0 {
		d.Escape = '\\'
	}
	if flags&0x40 != 0 && d.Delimiter != ' ' {
		d.SkipInitialSpace = true
	}
	field := 1 + int(flags>>8)%16
	return Options{
		Dialect:  &d,
		Encoding: UTF8,
		Strict:   flags&0x80 != 0,
		Limits:   Limits{MaxFieldBytes: field, MaxRecordBytes: field * (1 + int(flags>>12)%4), MaxFields: 1 + int(flags>>10)%8},
	}
}

type readResult struct {
	rec []string
	err string
}

func collect(t *testing.T, src io.Reader, opts Options, maxCalls int) ([]readResult, Stats) {
	t.Helper()
	r, err := NewReader(src, &opts)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	var out []readResult
	lastOffset := int64(-1)
	for calls := 0; ; calls++ {
		if calls > maxCalls {
			t.Fatalf("more than %d calls to Read", maxCalls)
		}
		rec, err := r.Read()
		if err == io.EOF {
			return out, r.Stats()
		}
		if err != nil {
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("unexpected error %v", err)
			}
			if pe.Offset < lastOffset || pe.Line < 1 || pe.Column < 1 {
				t.Fatalf("bad position %+v after offset %d", pe, lastOffset)
			}
			lastOffset = pe.Offset
			out = append(out, readResult{err: err.Error()})
			continue
		}
		if len(rec) > opts.Limits.MaxFields {
			t.Fatalf("record with %d fields, limit %d", len(rec), opts.Limits.MaxFields)
		}
		for i, f := range rec {
			if !utf8.ValidString(f) {
				t.Fatalf("field %q is not valid UTF-8", f)
			}
			if len(f) > opts.Limits.MaxFieldBytes {
				t.Fatalf("field of %d bytes, limit %d", len(f), opts.Limits.MaxFieldBytes)
			}
			line, col := r.FieldPos(i)
			if line < 1 || col < 1 {
				t.Fatalf("bad field position (%d,%d)", line, col)
			}
		}
		out = append(out, readResult{rec: rec})
	}
}

func FuzzReader(f *testing.F) {
	addReadSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte, flags uint16) {
		opts := fuzzOptions(flags)
		whole, st := collect(t, bytes.NewReader(data), opts, len(data)+2)
		one, _ := collect(t, iotest.OneByteReader(bytes.NewReader(data)), opts, len(data)+2)
		if !reflect.DeepEqual(whole, one) {
			t.Fatalf("whole and one-byte reads differ:\n%v\n%v", whole, one)
		}
		if !opts.Strict {
			return
		}
		for _, r := range whole {
			if r.err != "" {
				return
			}
		}
		opts.Strict = false
		lenient, lst := collect(t, bytes.NewReader(data), opts, len(data)+2)
		if !reflect.DeepEqual(whole, lenient) {
			t.Fatalf("strict and lenient records differ:\n%v\n%v", whole, lenient)
		}
		if lst.BareQuotes+lst.StrayQuotes+lst.InvalidUTF8 != 0 || st.Records != lst.Records {
			t.Fatalf("lenient stats %+v after a clean strict read", lst)
		}
	})
}

// FuzzAgainstStdlib checks that on valid UTF-8 without carriage returns a
// strict reader agrees with encoding/csv.
func FuzzAgainstStdlib(f *testing.F) {
	for _, c := range readCases {
		f.Add(c.in)
	}
	f.Fuzz(func(t *testing.T, data string) {
		if strings.ContainsRune(data, '\r') || !utf8.ValidString(data) || strings.HasPrefix(data, "\xEF\xBB\xBF") {
			return
		}
		std := csv.NewReader(strings.NewReader(data))
		std.FieldsPerRecord = -1
		var want [][]string
		var stdErr error
		for {
			rec, err := std.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				stdErr = err
				break
			}
			want = append(want, rec)
		}
		r, err := NewReader(strings.NewReader(data), &Options{Dialect: &Dialect{Delimiter: ',', Quote: '"'}, Encoding: UTF8, Strict: true})
		if err != nil {
			t.Fatal(err)
		}
		var got [][]string
		var ourErr error
		for {
			rec, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				ourErr = err
				break
			}
			got = append(got, rec)
		}
		if (stdErr == nil) != (ourErr == nil) {
			t.Fatalf("encoding/csv error %v, wildcsv error %v\nencoding/csv: %q\nwildcsv: %q", stdErr, ourErr, want, got)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("records differ:\nencoding/csv: %q\nwildcsv: %q", want, got)
		}
	})
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte("caf\xE9"), uint8(0))
	f.Add([]byte("a\x00\x00\xD8b\x00"), uint8(1))
	f.Add([]byte("\x00a\xD8\x00"), uint8(2))
	f.Add([]byte("ab\xC3\xA9\xFF"), uint8(3))
	f.Fuzz(func(t *testing.T, data []byte, which uint8) {
		mk := func(src io.Reader) io.Reader {
			switch which % 4 {
			case 0:
				return newDecoder(src, cp1252Conv{})
			case 1:
				return newDecoder(src, &utf16Conv{})
			case 2:
				return newDecoder(src, &utf16Conv{bigEndian: true})
			}
			return &lateReader{src: src}
		}
		whole, err := io.ReadAll(mk(bytes.NewReader(data)))
		if err != nil {
			t.Fatal(err)
		}
		one, err := io.ReadAll(mk(iotest.OneByteReader(bytes.NewReader(data))))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(whole, one) {
			t.Fatalf("whole %q, one byte %q", whole, one)
		}
		if which%4 == 0 && !utf8.Valid(whole) || (which%4 == 1 || which%4 == 2) && !utf8.Valid(bytes.ReplaceAll(whole, invalidUnit, nil)) {
			t.Fatalf("output %q is not valid UTF-8", whole)
		}
	})
}

func FuzzDetect(f *testing.F) {
	for _, c := range readCases {
		f.Add([]byte(c.in), false)
	}
	f.Add([]byte("a;b\n1,5;2\n'x';\"y\"\n"), true)
	f.Add([]byte("{\"a\":1}\n{\"a\":2}\n"), true)
	f.Add([]byte("\xFF\xFEa\x00,\x00b\x00"), false)
	f.Fuzz(func(t *testing.T, data []byte, small bool) {
		opts := &Options{}
		limit := DefaultSampleBytes
		if small {
			limit = 64
			opts.Limits.SampleBytes = limit
		}
		src := &countingReader{r: bytes.NewReader(data)}
		det, err := Detect(src, opts)
		if src.n > limit+1 {
			t.Fatalf("read %d bytes, limit %d", src.n, limit+1)
		}
		if err != nil {
			if !errors.Is(err, ErrUnsupportedEncoding) {
				t.Fatalf("Detect: %v", err)
			}
			return
		}
		again, _ := Detect(bytes.NewReader(data), opts)
		if fmt.Sprintf("%+v", det) != fmt.Sprintf("%+v", again) {
			t.Fatalf("reports differ:\n%+v\n%+v", det, again)
		}
		if det.Confidence < 0 || det.Confidence > 1 || det.Confidence != det.Confidence {
			t.Fatalf("confidence %v", det.Confidence)
		}
		if det.Kind != KindDelimited && det.Kind != KindEmpty {
			return
		}
		if err := det.Dialect.validate(); err != nil {
			t.Fatalf("detected dialect %v: %v", det.Dialect, err)
		}
		r, err := NewReader(bytes.NewReader(data), opts)
		if err != nil {
			t.Fatalf("NewReader: %v", err)
		}
		for range len(data) + 2 {
			rec, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				var pe *ParseError
				if !errors.As(err, &pe) {
					t.Fatalf("Read: %v", err)
				}
				continue
			}
			for _, f := range rec {
				if !utf8.ValidString(f) {
					t.Fatalf("field %q is not valid UTF-8", f)
				}
			}
		}
	})
}

// FuzzFastPath reads the same input by the general path alone and with the
// fast path, with small buffers and short reads, and requires the same
// records, positions, errors, warnings, counts and offsets.
func FuzzFastPath(f *testing.F) {
	addReadSeeds(f)
	f.Add([]byte("a,b\r\n\"x\"\"y\",\"p\nq\"\r\nz,\"w\"\n"), uint16(0))
	f.Add([]byte("a, \"b\" ,c\n  d,e\r"), uint16(0x40))
	f.Add([]byte("id,name\n1,\"Ana \"\"la\"\" G\"\n2,Zoë\n3,\"long field of more than thirty-two bytes, with \"\"quotes\"\"\"\n"), uint16(0x8000))
	f.Fuzz(func(t *testing.T, in []byte, flags uint16) {
		opts := fuzzOptions(flags)
		if flags&0x8000 != 0 {
			opts.Limits = Limits{} // the defaults, which the fast path rarely gets near
		}
		set, err := opts.settings()
		if err != nil {
			t.Skip()
		}
		run := func(fast bool, size int, src io.Reader) []string {
			sc := newScanner(src, *opts.Dialect, set.lim, opts.Strict)
			sc.buf = make([]byte, size)
			sc.fast = sc.fast && fast
			var log []string
			sc.warn = func(pe *ParseError) { log = append(log, fmt.Sprintf("warn %+v", *pe)) }
			for range 4 * (len(in) + 2) {
				err := sc.readRecord()
				if err == io.EOF {
					break
				}
				if pe, ok := errors.AsType[*ParseError](err); ok {
					log = append(log, fmt.Sprintf("error %+v", *pe))
					continue
				}
				if err != nil {
					log = append(log, "fatal "+err.Error())
					break
				}
				var b strings.Builder
				fmt.Fprintf(&b, "record %d at %d line %d:", sc.recNo, sc.recStart, sc.recLine)
				str := sc.recordString()
				for _, fl := range sc.fields {
					fmt.Fprintf(&b, " %q@%d:%d/%v", str[fl.start:fl.end], fl.line, fl.col, fl.quoted)
				}
				fmt.Fprintf(&b, " quoted %v next %d line %d:%d", sc.recQuoted, sc.abs(), sc.line, sc.lineStart)
				log = append(log, b.String())
			}
			return append(log, fmt.Sprintf("counts %d %d %d", sc.bare, sc.stray, sc.invalid))
		}
		want := run(false, 64<<10, bytes.NewReader(in))
		for _, size := range []int{16, 64 << 10} {
			for _, mk := range []func(io.Reader) io.Reader{
				func(r io.Reader) io.Reader { return r },
				iotest.OneByteReader,
				iotest.HalfReader,
			} {
				for _, fast := range []bool{false, true} {
					got := run(fast, size, mk(bytes.NewReader(in)))
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("fast %v, buffer %d: got\n%s\nwant\n%s", fast, size, strings.Join(got, "\n"), strings.Join(want, "\n"))
					}
				}
			}
		}
	})
}
