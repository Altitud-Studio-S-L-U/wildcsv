package wildcsv

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"testing"
)

// benchInputs are generated, so the numbers are reproducible without data
// files.
func benchInputs() map[string][]byte {
	var plain, quoted, text bytes.Buffer
	for i := 0; plain.Len() < 8<<20; i++ {
		fmt.Fprintf(&plain, "%d,customer-%d,2026-01-%02d,%d.%02d,active\n", i, i%9973, 1+i%28, i%1000, i%100)
	}
	for i := 0; quoted.Len() < 8<<20; i++ {
		fmt.Fprintf(&quoted, "%d,\"Smith, John\",\"line one\nline two\",\"say \"\"hi\"\"\",%d\n", i, i%100)
	}
	for i := 0; text.Len() < 8<<20; i++ {
		fmt.Fprintf(&text, "%d,Ñandú São Paulo,Zürich straße,東京都,%d\n", i, i%100)
	}
	return map[string][]byte{"plain": plain.Bytes(), "quoted": quoted.Bytes(), "multibyte": text.Bytes()}
}

func BenchmarkRead(b *testing.B) {
	inputs := benchInputs()
	for _, name := range []string{"plain", "quoted", "multibyte"} {
		data := inputs[name]
		b.Run(name+"/wildcsv", func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				r, err := NewReader(bytes.NewReader(data), &Options{Dialect: &Dialect{Delimiter: ',', Quote: '"'}, Encoding: UTF8, ReuseRecord: true})
				if err != nil {
					b.Fatal(err)
				}
				for {
					if _, err := r.Read(); err != nil {
						if err != io.EOF {
							b.Fatal(err)
						}
						break
					}
				}
			}
		})
		b.Run(name+"/encoding-csv", func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				r := csv.NewReader(bytes.NewReader(data))
				r.FieldsPerRecord = -1
				r.ReuseRecord = true
				for {
					if _, err := r.Read(); err != nil {
						if err != io.EOF {
							b.Fatal(err)
						}
						break
					}
				}
			}
		})
	}
}

func BenchmarkDetect(b *testing.B) {
	inputs := benchInputs()
	for _, name := range []string{"plain", "quoted", "multibyte"} {
		data := inputs[name]
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				if _, err := Detect(bytes.NewReader(data), nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	wide := strings.Repeat(strings.Repeat("abc;def,ghi|jkl:mno ", 40)+"\n", 2000)
	b.Run("many candidates", func(b *testing.B) {
		for b.Loop() {
			if _, err := Detect(strings.NewReader(wide), nil); err != nil {
				b.Fatal(err)
			}
		}
	})
}
