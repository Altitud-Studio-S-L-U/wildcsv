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
// files: about 8 MiB each of
//   - plain: unquoted ASCII fields;
//   - quoted: quoted fields with commas, line breaks and doubled quotes;
//   - multibyte: unquoted fields of Spanish, Portuguese, German and Japanese.
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

// BenchmarkRead reads each input, repeated to 32 MiB, with encoding/csv and
// with wildcsv in one goroutine and by default, through Read and ReadSlices.
// The dialect is given, so that only reading is measured, and records reuse
// one slice.
func BenchmarkRead(b *testing.B) {
	inputs := benchInputs()
	for _, name := range []string{"plain", "quoted", "multibyte"} {
		data := bytes.Repeat(inputs[name], 4)
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
		for _, c := range []struct {
			name        string
			concurrency int
			slices      bool
		}{
			{"wildcsv-1/Read", 1, false},
			{"wildcsv-1/ReadSlices", 1, true},
			{"wildcsv/Read", 0, false},
			{"wildcsv/ReadSlices", 0, true},
		} {
			b.Run(name+"/"+c.name, func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				for b.Loop() {
					opts := &Options{Dialect: &Dialect{Delimiter: ',', Quote: '"'}, Encoding: UTF8, ReuseRecord: true, Concurrency: c.concurrency}
					readAll(b, data, opts, c.slices)
				}
			})
		}
	}
}

// BenchmarkReadConcurrency reads the 32 MiB inputs with a fixed number of
// goroutines.
func BenchmarkReadConcurrency(b *testing.B) {
	inputs := benchInputs()
	for _, name := range []string{"plain", "quoted", "multibyte"} {
		data := bytes.Repeat(inputs[name], 4)
		for _, n := range []int{1, 2, 4, 8, 16} {
			b.Run(fmt.Sprintf("%s/%d", name, n), func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				for b.Loop() {
					opts := &Options{Dialect: &Dialect{Delimiter: ',', Quote: '"'}, Encoding: UTF8, ReuseRecord: true, Concurrency: n}
					readAll(b, data, opts, false)
				}
			})
		}
	}
}

func readAll(b *testing.B, data []byte, opts *Options, slices bool) {
	r, err := NewReader(bytes.NewReader(data), opts)
	if err != nil {
		b.Fatal(err)
	}
	for {
		if slices {
			_, err = r.ReadSlices()
		} else {
			_, err = r.Read()
		}
		if err != nil {
			if err != io.EOF {
				b.Fatal(err)
			}
			return
		}
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
