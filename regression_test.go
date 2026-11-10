package wildcsv

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// readOutcomes reads every record and error from src with opts, as text.
func readOutcomes(t *testing.T, src io.Reader, opts *Options) ([]string, *Reader) {
	t.Helper()
	r, err := NewReader(src, opts)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	var out []string
	for range 1 << 20 {
		rec, err := r.Read()
		if err == io.EOF {
			return out, r
		}
		if err != nil {
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("Read: %v", err)
			}
			out = append(out, fmt.Sprintf("err %v rec=%d line=%d col=%d off=%d quoted=%v", pe.Err, pe.Record, pe.Line, pe.Column, pe.Offset, pe.Quoted))
			continue
		}
		out = append(out, fmt.Sprintf("%q", rec))
	}
	t.Fatal("too many reads")
	return nil, nil
}

// chunkReader returns at most n bytes per Read.
type chunkReader struct {
	r io.Reader
	n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(p) > c.n {
		p = p[:c.n]
	}
	return c.r.Read(p)
}

// MaxRecordBytes counts the closing quote of the last field.
func TestRecordLimitCountsClosingQuote(t *testing.T) {
	for _, c := range []struct {
		in    string
		quote rune
		limit int
	}{
		{"\"abc\"\n", '"', 4},   // 5 bytes in the record, limit 4
		{"\"abc\"", '"', 4},     // same at the end of the input
		{"x,\"abc\"\n", '"', 6}, // 7 bytes
		{"«abc«\n", '«', 5},     // 7 bytes with a two-byte quote
	} {
		opts := &Options{
			Dialect:  &Dialect{Delimiter: ',', Quote: c.quote},
			Encoding: UTF8,
			Limits:   Limits{MaxFieldBytes: 3, MaxRecordBytes: c.limit},
		}
		r, err := NewReader(strings.NewReader(c.in), opts)
		if err != nil {
			t.Fatal(err)
		}
		rec, err := r.Read()
		if !errors.Is(err, ErrRecordTooLarge) {
			recordBytes := len(strings.TrimRight(c.in, "\n"))
			t.Errorf("%q with MaxRecordBytes %d: got record %q, err %v; the record has %d bytes with its quotes",
				c.in, c.limit, rec, err, recordBytes)
		}
	}
}

// ErrRecordTooLarge points at the start of the record. When the record starts
// with a quoted field that position is its opening quote, so Quoted must be
// true (section 8.4: "the position is inside a quoted field, or is its opening
// quote").
func TestRecordTooLargeQuotedFlag(t *testing.T) {
	opts := &Options{
		Dialect:  &Dialect{Delimiter: ',', Quote: '"'},
		Encoding: UTF8,
		Limits:   Limits{MaxFieldBytes: 8, MaxRecordBytes: 8},
	}
	r, err := NewReader(strings.NewReader("\"ab\",\"cd\",\"ef\"\n"), opts)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Read()
	var pe *ParseError
	if !errors.As(err, &pe) || !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("got %v, want ErrRecordTooLarge", err)
	}
	if pe.Offset != 0 || !pe.Quoted {
		t.Errorf("ErrRecordTooLarge at offset %d (the opening quote of field 1) has Quoted=%v, want true", pe.Offset, pe.Quoted)
	}
}

// ErrTooManyFields points at the start of the first field over the limit.
// When that field is quoted, the position is its opening quote, so Quoted
// must be true.
func TestTooManyFieldsQuotedFlag(t *testing.T) {
	opts := &Options{
		Dialect:  &Dialect{Delimiter: ',', Quote: '"'},
		Encoding: UTF8,
		Limits:   Limits{MaxFields: 1},
	}
	r, err := NewReader(strings.NewReader("a,\"b\"\n"), opts)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Read()
	var pe *ParseError
	if !errors.As(err, &pe) || !errors.Is(err, ErrTooManyFields) {
		t.Fatalf("got %v, want ErrTooManyFields", err)
	}
	if pe.Offset != 2 || !pe.Quoted {
		t.Errorf("ErrTooManyFields at offset %d (an opening quote) has Quoted=%v, want true", pe.Offset, pe.Quoted)
	}
}

// With SkipInitialSpace, a field starts after its padding: FieldPos says so.
// ErrTooManyFields should point at that same start, not at the padding.
func TestTooManyFieldsSkipSpacePosition(t *testing.T) {
	d := &Dialect{Delimiter: ',', Quote: '"', SkipInitialSpace: true}
	r, err := NewReader(strings.NewReader("a,   b\n"), &Options{Dialect: d, Encoding: UTF8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(); err != nil {
		t.Fatal(err)
	}
	_, col := r.FieldPos(1)
	r, err = NewReader(strings.NewReader("a,   b\n"), &Options{Dialect: d, Encoding: UTF8, Limits: Limits{MaxFields: 1}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Read()
	var pe *ParseError
	if !errors.As(err, &pe) || !errors.Is(err, ErrTooManyFields) {
		t.Fatalf("got %v, want ErrTooManyFields", err)
	}
	if pe.Column != col {
		t.Errorf("ErrTooManyFields at column %d, but field 2 starts at column %d (FieldPos)", pe.Column, col)
	}
}

// An unpaired surrogate in UTF-16 input reaches Options.Warn like invalid
// UTF-8, so every counted defect can be listed with its position.
func TestUTF16SurrogateIsWarned(t *testing.T) {
	// BOM, "a", lone high surrogate, "b", LF (UTF-16LE).
	in := []byte{0xFF, 0xFE, 'a', 0, 0x00, 0xD8, 'b', 0, '\n', 0}
	var warned int
	opts := &Options{Dialect: &Dialect{Delimiter: ',', Quote: '"'}, Warn: func(*ParseError) { warned++ }}
	_, r := readOutcomes(t, bytes.NewReader(in), opts)
	st := r.Stats()
	if st.InvalidUTF8 != int64(warned) {
		t.Errorf("Stats.InvalidUTF8 = %d, Warn called %d times; Warn should receive every counted defect", st.InvalidUTF8, warned)
	}
}

// The same records whatever the read sizes, around the 64 KiB buffer
// boundary, with multi-byte delimiter, quote and escape and CR LF.
func TestSplitIndependence(t *testing.T) {
	var b strings.Builder
	for i := 0; b.Len() < 200<<10; i++ {
		fmt.Fprintf(&b, "f%d§«x«\\««y»»%d«§é€\r\n", i, i)
		fmt.Fprintf(&b, "«q\r\nr«  §\xff\xfe§z\r")
	}
	in := b.String()
	for _, strictMode := range []bool{false, true} {
		opts := func() *Options {
			return &Options{Dialect: &Dialect{Delimiter: '§', Quote: '«', Escape: '\\'}, Encoding: UTF8, Strict: strictMode}
		}
		whole, _ := readOutcomes(t, strings.NewReader(in), opts())
		for _, n := range []int{1, 2, 3, 5, 7, 4093, 65535, 65537} {
			got, _ := readOutcomes(t, &chunkReader{r: strings.NewReader(in), n: n}, opts())
			if !reflect.DeepEqual(whole, got) {
				for i := range min(len(whole), len(got)) {
					if whole[i] != got[i] {
						t.Fatalf("strict=%v chunk %d: record %d differs: %s vs %s", strictMode, n, i, whole[i], got[i])
					}
				}
				t.Fatalf("strict=%v chunk %d: %d vs %d results", strictMode, n, len(whole), len(got))
			}
		}
	}
}

// The late decision does not depend on how reads split the first non-ASCII
// sequence, and Windows-1252 bytes after an ASCII sample decode.
func TestLateDecisionSplit(t *testing.T) {
	head := strings.Repeat("abc,def\n", 70<<10/8)
	for _, tail := range []string{"caf\xC3\xA9,x\n\xE9\n", "caf\xE9,x\n\xC3\xA9\n", "x\xE2\x82\xAC\n", "x\xE2\x82\n"} {
		in := head + tail
		var ref []string
		var refEnc Encoding
		for _, src := range []func() io.Reader{
			func() io.Reader { return strings.NewReader(in) },
			func() io.Reader { return iotest.OneByteReader(strings.NewReader(in)) },
			func() io.Reader { return &chunkReader{r: strings.NewReader(in), n: 65536 + 4} },
			func() io.Reader { return &chunkReader{r: strings.NewReader(in), n: 3} },
		} {
			got, r := readOutcomes(t, src(), nil)
			if ref == nil {
				ref, refEnc = got, r.Stats().Encoding
				continue
			}
			if !reflect.DeepEqual(ref, got) || refEnc != r.Stats().Encoding {
				t.Fatalf("tail %q: results depend on the read size", tail)
			}
		}
	}
}

// UTF-16 surrogate pairs cut by reads.
func TestUTF16SplitSurrogates(t *testing.T) {
	var raw []byte
	raw = append(raw, 0xFF, 0xFE)
	for i := 0; len(raw) < 140<<10; i++ {
		// "𝄞" (U+1D11E) is D834 DD1E; then ",a\n"
		raw = append(raw, 0x34, 0xD8, 0x1E, 0xDD, ',', 0, 'a', 0, '\n', 0)
		if i%7 == 0 {
			raw = append(raw, 0x00, 0xDC) // a lone low surrogate
		}
	}
	whole, wr := readOutcomes(t, bytes.NewReader(raw), nil)
	for _, n := range []int{1, 3, 32767, 32769} {
		got, gr := readOutcomes(t, &chunkReader{r: bytes.NewReader(raw), n: n}, nil)
		if !reflect.DeepEqual(whole, got) || wr.Stats() != gr.Stats() {
			t.Fatalf("chunk %d: results differ", n)
		}
	}
}

// Invalid bytes cost linear time: a scan that went back over the buffer after
// each invalid byte would make untrusted input stall the reader.
func TestInvalidUTF8IsLinear(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	const n = 256 << 10
	bad := []byte(strings.Repeat("a\xff", n/2) + "\n")
	good := []byte(strings.Repeat("ab", n/2) + "\n")
	timeIt := func(in []byte) time.Duration {
		start := time.Now()
		readOutcomes(t, bytes.NewReader(in), &Options{Dialect: &Dialect{Delimiter: ',', Quote: '"'}, Encoding: UTF8})
		return time.Since(start)
	}
	g, b := timeIt(good), timeIt(bad)
	t.Logf("256 KiB valid: %v; 256 KiB with 128 Ki invalid bytes: %v", g, b)
	if b > 100*g+500*time.Millisecond {
		t.Errorf("invalid bytes make reading %.0f times slower", float64(b)/float64(g))
	}
}

// UTF-16 defects are counted where the scanner reads them, not where the
// decoder meets them, so Stats after a record never depends on read sizes.
func TestUTF16StatsIndependentOfReadSize(t *testing.T) {
	raw := []byte{0xFF, 0xFE}
	for _, r := range "a,b\nc,d\n" {
		raw = append(raw, byte(r), 0)
	}
	raw = append(raw, 0x00, 0xD8, '\n', 0) // a lone high surrogate in record 3
	counts := map[string]int64{}
	for name, src := range map[string]func() io.Reader{
		"whole":    func() io.Reader { return bytes.NewReader(raw) },
		"one byte": func() io.Reader { return iotest.OneByteReader(bytes.NewReader(raw)) },
	} {
		r, err := NewReader(src(), &Options{Dialect: &Dialect{Delimiter: ',', Quote: '"'}, Limits: Limits{SampleBytes: 4}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Read(); err != nil {
			t.Fatal(err)
		}
		counts[name] = r.Stats().InvalidUTF8
	}
	if counts["whole"] != counts["one byte"] {
		t.Errorf("Stats().InvalidUTF8 after record 1, before the defect in record 3: %v", counts)
	}
}

// errAfterReader returns b, then err.
type errAfterReader struct {
	b   []byte
	err error
}

func (e *errAfterReader) Read(p []byte) (int, error) {
	if len(e.b) == 0 {
		return 0, e.err
	}
	n := copy(p, e.b)
	e.b = e.b[n:]
	return n, nil
}

// A read error that cuts a multi-byte character is returned as the read
// error, not as invalid data.
func TestReadErrorCuttingACharacter(t *testing.T) {
	boom := errors.New("boom")
	r, err := NewReader(&errAfterReader{b: []byte("a,caf\xC3"), err: boom},
		&Options{Dialect: &Dialect{Delimiter: ',', Quote: '"'}, Encoding: UTF8, Strict: true, Limits: Limits{SampleBytes: 2}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Read()
	if !errors.Is(err, boom) {
		t.Errorf("first Read returned %v; the input is not invalid, it was cut by the read error %v", err, boom)
	}
}
