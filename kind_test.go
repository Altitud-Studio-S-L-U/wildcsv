package wildcsv

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func zipBytes(t *testing.T, entries []string, contents map[string]string, store bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, name := range entries {
		method := zip.Deflate
		if store {
			method = zip.Store
		}
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(f, contents[name])
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestKinds(t *testing.T) {
	xlsx := zipBytes(t, []string{"[Content_Types].xml", "xl/workbook.xml"}, map[string]string{"xl/workbook.xml": "<workbook/>"}, false)
	ods := zipBytes(t, []string{"mimetype", "content.xml"}, map[string]string{"mimetype": "application/vnd.oasis.opendocument.spreadsheet"}, true)
	plainZip := zipBytes(t, []string{"a.txt"}, map[string]string{"a.txt": "hello"}, false)
	cases := []struct {
		name string
		in   []byte
		want Kind
	}{
		{"K01", nil, KindEmpty},
		{"K02", xlsx, KindXLSX},
		{"K03", ods, KindODS},
		{"K04", plainZip, KindZIP},
		{"K05", append([]byte("\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1"), make([]byte, 32)...), KindXLS},
		{"K06", []byte("\x1F\x8B\x08\x00\x00"), KindGzip},
		{"K07", []byte("BZh91AY&SY"), KindBzip2},
		{"K08", []byte("\xFD7zXZ\x00\x00\x04"), KindXZ},
		{"K09", []byte("\x28\xB5\x2F\xFD\x00"), KindZstd},
		{"K10", []byte("%PDF-1.7\n"), KindPDF},
		{"K11", []byte("PAR1\x15\x04"), KindParquet},
		{"K12", []byte("\x00\x01\x02\x03 abc"), KindBinary},
		{"K13a", []byte("<!DOCTYPE html><html>"), KindHTML},
		{"K13b", []byte(" \n<table><tr>"), KindHTML},
		{"K14a", []byte(`<?xml version="1.0"?><a/>`), KindXML},
		{"K14b", []byte("<rows><row/></rows>"), KindXML},
		{"K15", []byte("{\"a\":1}\n{\"a\":2}\n"), KindJSONL},
		{"K16", []byte("[\n {\"a\": 1},\n {\"a\": 2}\n]\n"), KindJSON},
		{"K17", []byte(`{"a":1}`), KindJSON},
		{"K18", []byte("[id],[name]\n1,x\n"), KindDelimited},
		{"K19", []byte("1\n2\n3\n"), KindDelimited},
		{"K20a", []byte("<5,>3\n1,2\n"), KindDelimited},
		{"K20b", []byte("<a,b>\n1,2\n"), KindDelimited},
		{"controls below 1%", []byte(strings.Repeat("a,b\n", 100) + "\x01"), KindDelimited},
		{"UTF-16 text is not binary", []byte("\xFF\xFEa\x00,\x00b\x00"), KindDelimited},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			det, err := Detect(bytes.NewReader(c.in), nil)
			if err != nil {
				t.Fatal(err)
			}
			if det.Kind != c.want {
				t.Fatalf("kind = %s, want %s", det.Kind, c.want)
			}
			_, err = NewReader(bytes.NewReader(c.in), nil)
			if c.want == KindDelimited || c.want == KindEmpty {
				if err != nil {
					t.Fatalf("NewReader: %v", err)
				}
				return
			}
			var ke *KindError
			if !errors.As(err, &ke) || ke.Kind != c.want || !errors.Is(err, ErrNotDelimited) {
				t.Fatalf("NewReader err = %v, want KindError %s", err, c.want)
			}
		})
	}
}

func TestIncompleteJSONSample(t *testing.T) {
	in := "[" + strings.Repeat(`{"name":"x","n":1},`, 1000) + `{"name":"y"}]`
	det, err := Detect(strings.NewReader(in), &Options{Limits: Limits{SampleBytes: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	if det.Kind != KindJSON {
		t.Errorf("kind = %s, want json", det.Kind)
	}
}

func TestEmptyInputReader(t *testing.T) {
	r, err := NewReader(strings.NewReader(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(); err != io.EOF {
		t.Errorf("Read err = %v, want io.EOF", err)
	}
}
