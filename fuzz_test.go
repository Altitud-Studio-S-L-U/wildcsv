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
		mk := func() converter {
			switch which % 4 {
			case 0:
				return cp1252Conv{}
			case 1:
				return &utf16Conv{}
			case 2:
				return &utf16Conv{bigEndian: true}
			}
			return &lateConv{}
		}
		whole, err := io.ReadAll(newDecoder(bytes.NewReader(data), mk()))
		if err != nil {
			t.Fatal(err)
		}
		one, err := io.ReadAll(newDecoder(iotest.OneByteReader(bytes.NewReader(data)), mk()))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(whole, one) {
			t.Fatalf("whole %q, one byte %q", whole, one)
		}
		if which%4 != 3 && !utf8.Valid(whole) {
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
