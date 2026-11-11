package wildcsv

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

func readAllRecords(t *testing.T, src io.Reader, opts *Options) ([][]string, *Reader) {
	t.Helper()
	r, err := NewReader(src, opts)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	var out [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return out, r
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		out = append(out, rec)
	}
}

func commaDialect() *Dialect { return &Dialect{Delimiter: ',', Quote: '"'} }

type decodeCase struct {
	name      string
	in        string
	opts      Options
	want      [][]string
	encoding  Encoding // in the report
	basis     string
	bom       bool
	finalEnc  Encoding // Stats.Encoding
	invalid   int64
	offsetEnd int64 // InputOffset after the last record; 0 to skip
}

var decodeCases = []decodeCase{
	{name: "D01", in: "\xEF\xBB\xBFa,b\n", opts: Options{Dialect: commaDialect()}, want: [][]string{{"a", "b"}},
		encoding: UTF8, basis: BasisBOM, bom: true, finalEnc: UTF8, offsetEnd: 4},
	{name: "D02", in: "\xFF\xFEa\x00,\x00b\x00\n\x00", opts: Options{Dialect: commaDialect()}, want: [][]string{{"a", "b"}},
		encoding: UTF16LE, basis: BasisBOM, bom: true, finalEnc: UTF16LE},
	{name: "D03", in: "\xFE\xFF\x00a\x00,\x00b\x00\n", opts: Options{Dialect: commaDialect()}, want: [][]string{{"a", "b"}},
		encoding: UTF16BE, basis: BasisBOM, bom: true, finalEnc: UTF16BE},
	{name: "D04", in: "a\x00,\x00b\x00\n\x001\x00,\x002\x00\n\x00", opts: Options{Dialect: commaDialect()}, want: [][]string{{"a", "b"}, {"1", "2"}},
		encoding: UTF16LE, basis: BasisUTF16Pattern, finalEnc: UTF16LE},
	{name: "D05", in: "caf\xE9;1\n", opts: Options{Dialect: &Dialect{Delimiter: ';', Quote: '"'}}, want: [][]string{{"café", "1"}},
		encoding: Windows1252, basis: BasisFallback, finalEnc: Windows1252},
	{name: "D06", in: "\x80,\x81\n", opts: Options{Dialect: commaDialect(), Encoding: Windows1252}, want: [][]string{{"€", "\xC2\x81"}},
		encoding: Windows1252, basis: BasisDeclared, finalEnc: Windows1252},
	{name: "D07", in: "id,name\n1,Jos\xE9\n", opts: Options{Dialect: commaDialect(), Limits: Limits{SampleBytes: 8}},
		want: [][]string{{"id", "name"}, {"1", "José"}}, encoding: UTF8, basis: BasisASCII, finalEnc: Windows1252},
	{name: "D08", in: "id,name\n1,Jos\xC3\xA9\n", opts: Options{Dialect: commaDialect(), Limits: Limits{SampleBytes: 8}},
		want: [][]string{{"id", "name"}, {"1", "José"}}, encoding: UTF8, basis: BasisASCII, finalEnc: UTF8},
	{name: "D11", in: "\xFF\xFEa\x00\x00\xD8,\x00b\x00\n\x00", opts: Options{Dialect: commaDialect()},
		want: [][]string{{"a" + replacementChar, "b"}}, encoding: UTF16LE, basis: BasisBOM, bom: true, finalEnc: UTF16LE, invalid: 1},
	{name: "D12", in: "\xEF\xBB\xBFa,b\n", opts: Options{Dialect: commaDialect(), Decode: func(r io.Reader) io.Reader { return r }},
		want: [][]string{{"\xEF\xBB\xBFa", "b"}}, encoding: CustomDecoding, basis: BasisCustom, finalEnc: CustomDecoding},
	{name: "D13", in: "caf\xC3\xA9\n", opts: Options{Dialect: commaDialect(), Encoding: "latin1"},
		want: [][]string{{"cafÃ©"}}, encoding: Windows1252, basis: BasisDeclared, finalEnc: Windows1252},
	{name: "D14", in: "\xC3\xA9,1\nx,\xFF\n", opts: Options{Dialect: commaDialect(), Limits: Limits{SampleBytes: 6}},
		want: [][]string{{"é", "1"}, {"x", replacementChar}}, encoding: UTF8, basis: BasisUTF8, finalEnc: UTF8, invalid: 1},
	{name: "UTF-16 surrogate pair", in: "\xFF\xFE\x3D\xD8\x00\xDE,\x00x\x00", opts: Options{Dialect: commaDialect()},
		want: [][]string{{"\xF0\x9F\x98\x80", "x"}}, encoding: UTF16LE, basis: BasisBOM, bom: true, finalEnc: UTF16LE},
	{name: "UTF-16 odd byte", in: "\xFE\xFF\x00a\x00", opts: Options{Dialect: commaDialect()},
		want: [][]string{{"a" + replacementChar}}, encoding: UTF16BE, basis: BasisBOM, bom: true, finalEnc: UTF16BE, invalid: 1},
}

func TestDecode(t *testing.T) {
	for _, c := range decodeCases {
		for _, wrap := range []struct {
			name string
			fn   func(io.Reader) io.Reader
		}{{"whole", func(r io.Reader) io.Reader { return r }}, {"one byte", iotest.OneByteReader}, {"halves", iotest.HalfReader}} {
			t.Run(c.name+"/"+wrap.name, func(t *testing.T) {
				opts := c.opts
				got, r := readAllRecords(t, wrap.fn(strings.NewReader(c.in)), &opts)
				if !reflect.DeepEqual(got, c.want) {
					t.Errorf("records = %q, want %q", got, c.want)
				}
				det := r.Detection()
				if det.Encoding != c.encoding || det.EncodingBasis != c.basis || det.BOM != c.bom {
					t.Errorf("report: %s %s bom=%v, want %s %s bom=%v", det.Encoding, det.EncodingBasis, det.BOM, c.encoding, c.basis, c.bom)
				}
				st := r.Stats()
				if st.Encoding != c.finalEnc || st.InvalidUTF8 != c.invalid {
					t.Errorf("Stats: encoding %s invalid %d, want %s %d", st.Encoding, st.InvalidUTF8, c.finalEnc, c.invalid)
				}
				if c.offsetEnd != 0 && r.InputOffset() != c.offsetEnd {
					t.Errorf("InputOffset = %d, want %d", r.InputOffset(), c.offsetEnd)
				}
			})
		}
	}
}

func TestUTF32IsRejected(t *testing.T) {
	in := "\xFF\xFE\x00\x00a\x00\x00\x00"
	if _, err := Detect(strings.NewReader(in), nil); !errors.Is(err, ErrUnsupportedEncoding) {
		t.Errorf("Detect: err = %v", err)
	}
	_, err := NewReader(strings.NewReader(in), nil)
	var ee *EncodingError
	if !errors.As(err, &ee) || ee.Encoding != "utf-32le" {
		t.Errorf("NewReader: err = %v", err)
	}
}

func TestDecoderConsistency(t *testing.T) {
	// The late decision depends only on the first non-ASCII sequence, never
	// on how the input is split into reads.
	inputs := []string{"ab\xC3", "ab\xC3\xA9cd\xE9", "ab\xE9\xC3\xA9", "\xE2\x82", "\xE2\x82\xAC\xFF", strings.Repeat("x", 40000) + "\xE9"}
	for _, in := range inputs {
		whole, _ := io.ReadAll(&lateReader{src: strings.NewReader(in)})
		one, _ := io.ReadAll(&lateReader{src: iotest.OneByteReader(strings.NewReader(in))})
		if string(whole) != string(one) {
			t.Errorf("%q: whole %q, one byte %q", in, whole, one)
		}
	}
}
