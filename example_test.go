package wildcsv_test

import (
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"

	"github.com/Altitud-Studio-S-L-U/wildcsv"
)

// A spreadsheet export with semicolons, decimal commas and a Windows-1252
// accented letter is read without saying any of that.
func ExampleNewReader() {
	export := "id;name;amount\r\n1;Jos\xe9;12,50\r\n2;Ana;7,00\r\n"
	r, err := wildcsv.NewReader(strings.NewReader(export), nil)
	if err != nil {
		log.Fatal(err)
	}
	det := r.Detection()
	fmt.Printf("delimiter %q, encoding %s, header %s\n", det.Dialect.Delimiter, det.Encoding, det.Header)
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(strings.Join(rec, " | "))
	}
	// Output:
	// delimiter ';', encoding windows-1252, header present
	// id | name | amount
	// 1 | José | 12,50
	// 2 | Ana | 7,00
}

func ExampleDetect() {
	det, err := wildcsv.Detect(strings.NewReader("date|total\n2026-01-05|3.5\n2026-01-06|4\n"), nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(det.Kind, det.Dialect, det.Header)
	for _, c := range det.Columns {
		fmt.Println(c.Name, c.Kind)
	}
	// Output:
	// delimited delimiter '|' quote '"' present
	// date date
	// total number
}

// A file that is not delimited text is named, not misread.
func ExampleKindError() {
	_, err := wildcsv.NewReader(strings.NewReader(`{"id": 1}`+"\n"+`{"id": 2}`+"\n"), nil)
	var ke *wildcsv.KindError
	if errors.As(err, &ke) {
		fmt.Println("not delimited text:", ke.Kind)
	}
	// Output:
	// not delimited text: jsonl
}

// Every problem carries its position, and the reader goes on with the next
// line, so one pass lists all of them.
func ExampleReader_All() {
	in := "a,b\n1,2,3\n\"open,4\n"
	r, err := wildcsv.NewReader(strings.NewReader(in), &wildcsv.Options{
		Dialect: &wildcsv.Dialect{Delimiter: ',', Quote: '"'},
		Limits:  wildcsv.Limits{MaxFields: 2},
	})
	if err != nil {
		log.Fatal(err)
	}
	for rec, err := range r.All() {
		var pe *wildcsv.ParseError
		if errors.As(err, &pe) {
			fmt.Printf("line %d, column %d: %v\n", pe.Line, pe.Column, pe.Err)
			continue
		}
		fmt.Println(rec)
	}
	// Output:
	// [a b]
	// line 2, column 5: record exceeds MaxFields
	// line 3, column 1: quoted field not closed before the end of the input
}

// Lenient reading keeps going through defects and reports each of them.
func ExampleOptions_warn() {
	in := `id,comment` + "\n" + `1,"He said "hi" to me, twice"` + "\n"
	opts := &wildcsv.Options{Warn: func(e *wildcsv.ParseError) {
		fmt.Printf("warning: line %d, column %d: %v\n", e.Line, e.Column, e.Err)
	}}
	r, err := wildcsv.NewReader(strings.NewReader(in), opts)
	if err != nil {
		log.Fatal(err)
	}
	for rec, err := range r.All() {
		if err == nil {
			fmt.Println(rec[1])
		}
	}
	// Output:
	// comment
	// warning: line 2, column 12: quote character inside a quoted field that neither doubles nor closes it
	// warning: line 2, column 15: quote character inside a quoted field that neither doubles nor closes it
	// He said "hi" to me, twice
}

// Options.Decode accepts any decoder to UTF-8, such as one from
// golang.org/x/text; this one upper-cases ASCII to stand in for it.
func ExampleOptions_decode() {
	upper := func(r io.Reader) io.Reader {
		b, _ := io.ReadAll(r)
		return strings.NewReader(strings.ToUpper(string(b)))
	}
	r, err := wildcsv.NewReader(strings.NewReader("a;b\nc;d\n"), &wildcsv.Options{Decode: upper})
	if err != nil {
		log.Fatal(err)
	}
	rec, _ := r.Read()
	fmt.Println(rec, r.Detection().Encoding)
	// Output:
	// [A B] custom
}

// ReadSlices hands out the fields without copying them, for code that parses
// or copies each field right away, such as this sum of amounts.
func ExampleReader_ReadSlices() {
	data := "sku,amount\nA1,12.50\nB2,7.25\nC3,0.25\n"
	r, err := wildcsv.NewReader(strings.NewReader(data), nil)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := r.ReadSlices(); err != nil { // the header
		log.Fatal(err)
	}
	total := 0.0
	for {
		fields, err := r.ReadSlices()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		// fields[1] is valid only until the next call, so it is used now.
		v, err := strconv.ParseFloat(string(fields[1]), 64)
		if err != nil {
			log.Fatal(err)
		}
		total += v
	}
	fmt.Printf("%.2f\n", total)
	// Output: 20.00
}
