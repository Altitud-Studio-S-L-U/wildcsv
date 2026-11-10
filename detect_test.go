package wildcsv

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := map[ValueKind][]string{
		ValueEmpty:    {"", "   "},
		ValueNull:     {"NA", "n/a", "NaN", "NULL", "nil", "none", "-"},
		ValueBoolean:  {"true", "FALSE", "Yes", "no"},
		ValueInteger:  {"0", "-12", "+7", "0012", "12345678901234567890", "20260105"},
		ValueNumber:   {"3.14", "-0.5", ".5", "5.", "1e10", "1.5E-3", "12,5", "1,234", "1,234,567.89", "1.234.567,89", "1 234 567,89", "1'234'567.89", "1.5", "192.168.100.200", "1.234"},
		ValuePercent:  {"12%", "12.5 %", "-3,5%"},
		ValueCurrency: {"$12", "€ 12,50", "12,50 €", "£1,234.00", "-$5"},
		ValueDate:     {"2026-01-05", "2026/1/5", "05.01.2026", "5-1-26", "31/12/2026", "12/31/2026"},
		ValueTime:     {"9:05", "09:05", "23:59:59", "23:59:59.123", "9:05 PM", "09:05Z", "09:05:00+02:00", "09:05:00+0200"},
		ValueDateTime: {"2026-01-05 09:05", "2026-01-05T09:05:00Z", "05/01/2026 9:05 PM"},
		ValueURL:      {"http://a.b/c?d=e", "https://example.com", "ftp://x/y", "www.example.com"},
		ValueEmail:    {"a.b+c@example.co.uk"},
		ValueIPv4:     {"192.168.0.1"},
		ValueUUID:     {"123e4567-e89b-12d3-a456-426614174000"},
		ValuePath:     {"/usr/local/bin", "~/x/y.txt", "./a/b"},
		ValueText:     {"Hello world", "São Paulo", "item_42", "What?", "example.com", "1-5", "1.2.3", "2026-13-01"},
		ValueOther:    {"a,b", "a;b", "a|b", "x=1", "#tag", "O'Brien", "12,34,56", "32/13/2026", "24:00", "(n/a)", strings.Repeat("a", 300)},
	}
	for want, cells := range cases {
		for _, c := range cells {
			if got, _ := classify(c, false); got != want {
				t.Errorf("classify(%q) = %s, want %s", c, got, want)
			}
		}
	}
	if got, _ := classify("a,b", true); got != ValueText {
		t.Errorf("quoted a,b = %s, want text", got)
	}
	for cell, want := range map[string]int{"12,50": decComma, "3.14": decPoint, "1,234": decUnknown, "1.234.567,89": decComma, "1,234,567.89": decPoint} {
		if _, got := classify(cell, false); got != want {
			t.Errorf("decimal separator of %q = %d, want %d", cell, got, want)
		}
	}
}

func detectString(t *testing.T, in string, opts *Options) *Detection {
	t.Helper()
	det, err := Detect(strings.NewReader(in), opts)
	if err != nil {
		t.Fatal(err)
	}
	return det
}

func TestDetectGolden(t *testing.T) {
	cases := []struct {
		name string
		in   string
		opts *Options
		want Dialect
	}{
		{"G01", "a,b,c\n1,2,3\n4,5,6\n", nil, Dialect{Delimiter: ',', Quote: '"'}},
		{"G02", "a;b;c\n1,5;2,5;3,5\n4,5;5,5;6,5\n", nil, Dialect{Delimiter: ';', Quote: '"'}},
		{"G03", "a\tb\n1\t2\n3\t4\n", nil, Dialect{Delimiter: '\t', Quote: '"'}},
		{"G04", "a|b|c\n1|2|3\n4|5|6\n", nil, Dialect{Delimiter: '|', Quote: '"'}},
		{"G05", "name,notes\n'Ana','likes, commas'\n'Luis','x, y'\n", nil, Dialect{Delimiter: ',', Quote: '\''}},
		{"G06", `a,b` + "\n" + `"x \"y\" z",1` + "\n" + `"p \"q\" r",2` + "\n", nil, Dialect{Delimiter: ',', Quote: '"', Escape: '\\'}},
		{"G07", `a, "b,c", d` + "\n" + `1, "2,3", 4` + "\n", nil, Dialect{Delimiter: ',', Quote: '"', SkipInitialSpace: true}},
		{"G08", "alpha\nbeta\ngamma\n", nil, Dialect{Delimiter: ',', Quote: '"'}},
		{"G10", "a,b,c\n1,2,3\n4,5,6\n", &Options{Delimiters: []rune{';'}}, Dialect{Delimiter: ';', Quote: '"'}},
		{"G11", "size,desc\n5\" nail,steel\n6\" nail,brass\n", nil, Dialect{Delimiter: ',', Quote: 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			det := detectString(t, c.in, c.opts)
			if det.Kind != KindDelimited {
				t.Fatalf("kind = %s", det.Kind)
			}
			if det.Dialect != c.want {
				t.Errorf("dialect = %v, want %v (alternatives %v)", det.Dialect, c.want, det.Alternatives)
			}
		})
	}
	if det := detectString(t, "a,b,c\n1,2,3\n4,5,6\n", &Options{Delimiters: []rune{';'}}); det.Confidence != 1 || len(det.Columns) != 1 {
		t.Errorf("G10: confidence %v, %d columns", det.Confidence, len(det.Columns))
	}
}

func TestDetectDeterministic(t *testing.T) {
	in := "x;y,z\n1;2,3\n4,5;6\n'a';\"b\"\n"
	first := detectString(t, in, nil)
	for range 5 {
		if again := detectString(t, in, nil); !reflect.DeepEqual(first, again) {
			t.Fatalf("reports differ:\n%+v\n%+v", first, again)
		}
	}
}

func TestDetectReport(t *testing.T) {
	det := detectString(t, "id;name\r\n1;Ana\r\n2;Luis\r\n", nil)
	if det.LineEnding != "crlf" || !det.Complete || det.SampleRecords != 3 || det.Encoding != UTF8 || det.EncodingBasis != BasisASCII {
		t.Errorf("report = %+v", det)
	}
	if det.Confidence <= 0 || det.Confidence > 1 {
		t.Errorf("confidence = %v", det.Confidence)
	}
	big := strings.Repeat("a,b\n", 100000)
	det = detectString(t, big, nil)
	if det.Complete || det.SampleBytes != DefaultSampleBytes || det.SampleRecords != maxScoredRecords {
		t.Errorf("large input: complete %v, sample %d bytes, %d records", det.Complete, det.SampleBytes, det.SampleRecords)
	}
}

func TestDetectReadsBoundedSample(t *testing.T) {
	src := &countingReader{r: strings.NewReader(strings.Repeat("a,b\n", 100000))}
	if _, err := Detect(src, &Options{Limits: Limits{SampleBytes: 1000}}); err != nil {
		t.Fatal(err)
	}
	if src.n > 1001 {
		t.Errorf("Detect read %d bytes, limit 1001", src.n)
	}
}

type countingReader struct {
	r interface{ Read([]byte) (int, error) }
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

func TestHeader(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		header HeaderGuess
		cols   []Column
	}{
		{"H01", "id,name,amount\n1,Ana,10.5\n2,Luis,7\n", HeaderPresent,
			[]Column{{Name: "id", Kind: ValueInteger}, {Name: "name", Kind: ValueText}, {Name: "amount", Kind: ValueNumber}}},
		{"H02", "1,Ana,10.5\n2,Luis,7\n3,Eva,1\n", HeaderAbsent,
			[]Column{{Kind: ValueInteger}, {Kind: ValueText}, {Kind: ValueNumber}}},
		{"H03", "name,city\nAna,Madrid\nLuis,Vigo\n", HeaderUnknown, nil},
		{"H04", "id,name\n", HeaderUnknown, nil},
		{"H05", "id,,amount\n1,x,2\n", HeaderUnknown, nil},
		{"H06", "fecha;importe\n05/01/2026;12,50\n06/01/2026;7,00\n", HeaderPresent,
			[]Column{{Name: "fecha", Kind: ValueDate}, {Name: "importe", Kind: ValueNumber, DecimalComma: true}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			det := detectString(t, c.in, nil)
			if det.Header != c.header {
				t.Errorf("header = %s, want %s", det.Header, c.header)
			}
			if c.cols != nil && !reflect.DeepEqual(det.Columns, c.cols) {
				t.Errorf("columns = %+v, want %+v", det.Columns, c.cols)
			}
		})
	}
}

func TestReaderUsesDetectedDialect(t *testing.T) {
	r, err := NewReader(bytes.NewReader([]byte("a;b\n1,5;2\n")), nil)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := r.Read()
	if err != nil || !reflect.DeepEqual(rec, []string{"a", "b"}) {
		t.Fatalf("Read = %q, %v", rec, err)
	}
	if r.Detection().Dialect.Delimiter != ';' {
		t.Errorf("dialect = %v", r.Detection().Dialect)
	}
}
