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
)

var errInjected = errors.New("injected read error")

// outcomes reads everything r returns, with each field's position, the
// offset after each record, the warnings and the final counts.
func outcomes(r *Reader, slices bool, warns *[]string) []string {
	var log []string
	for range 1 << 16 {
		var rec []string
		var err error
		if slices {
			var b [][]byte
			b, err = r.ReadSlices()
			for _, f := range b {
				rec = append(rec, string(f))
			}
		} else {
			rec, err = r.Read()
		}
		log = append(log, *warns...)
		*warns = (*warns)[:0]
		if pe, ok := errors.AsType[*ParseError](err); ok {
			log = append(log, fmt.Sprintf("error %+v", *pe))
			continue
		}
		if err != nil {
			log = append(log, "end "+err.Error())
			break
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%q", rec)
		for i := range rec {
			l, c := r.FieldPos(i)
			fmt.Fprintf(&b, " %d:%d", l, c)
		}
		fmt.Fprintf(&b, " offset %d", r.InputOffset())
		log = append(log, b.String())
	}
	return append(log, fmt.Sprintf("stats %+v", r.Stats()))
}

func readAllWays(t *testing.T, in []byte, opts Options, workers, size int, src func([]byte) io.Reader, slices bool) []string {
	t.Helper()
	var warns []string
	opts.Warn = func(pe *ParseError) { warns = append(warns, fmt.Sprintf("warn %+v", *pe)) }
	opts.Concurrency = workers
	saved := chunkBytes
	chunkBytes = size
	r, err := NewReader(src(in), &opts)
	chunkBytes = saved
	if err != nil {
		return []string{"NewReader " + err.Error()}
	}
	defer r.Close()
	return outcomes(r, slices, &warns)
}

// FuzzParallel reads the same input with one goroutine and with several,
// cutting the text into chunks of a few bytes so that chunks end inside
// records and quoted fields, and requires the same results.
func FuzzParallel(f *testing.F) {
	for i, c := range readCases {
		f.Add([]byte(c.in), uint16(0), uint8(i))
		f.Add([]byte(c.in), uint16(0xFFFF), uint8(i*7))
	}
	f.Add([]byte("a,b\n\"x\ny\",z\n\"p\"\"q\",r\n1,2\n"), uint16(0), uint8(3))
	f.Add([]byte("h1\nh2\na,\"b\nc\"\nd\"e,f\n\"g\",h\n"), uint16(0x8000), uint8(0x45))
	f.Add([]byte("x;y\r\n1;\"a\r\nb\"\r\n2;3"), uint16(1), uint8(7))
	f.Fuzz(func(t *testing.T, in []byte, flags uint16, shape uint8) {
		opts := fuzzOptions(flags)
		if flags&0x8000 != 0 {
			opts.Limits = Limits{}
		}
		if flags&0x4000 != 0 {
			opts.Encoding = ""
		}
		opts.Limits.SampleBytes = 1 + int(shape)%16
		opts.SkipLines = int(shape>>6) % 3
		size := 1 + int(shape>>2)%32
		srcs := []func([]byte) io.Reader{
			func(b []byte) io.Reader { return bytes.NewReader(b) },
			func(b []byte) io.Reader { return iotest.OneByteReader(bytes.NewReader(b)) },
			func(b []byte) io.Reader { return io.MultiReader(bytes.NewReader(b), iotest.ErrReader(errInjected)) },
		}
		for si, src := range srcs {
			for _, slices := range []bool{false, true} {
				want := readAllWays(t, in, opts, 0, size, src, slices)
				for _, workers := range []int{2, 3} {
					got := readAllWays(t, in, opts, workers, size, src, slices)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("source %d, slices %v, %d workers, chunks of %d: got\n%s\nwant\n%s",
							si, slices, workers, size, strings.Join(got, "\n"), strings.Join(want, "\n"))
					}
				}
			}
		}
	})
}

// TestParallelFallback builds inputs where chunk guesses fail: a bare quote
// flips the quote parity before records with line feeds in quoted fields, an
// escape character hides quotes from it, and lines end in carriage returns
// only. Results must still match one goroutine.
func TestParallelFallback(t *testing.T) {
	var flip, esc, cr, big strings.Builder
	for i := range 400 {
		if i == 3 {
			flip.WriteString("5\" screen,x\n")
		}
		fmt.Fprintf(&flip, "%d,\"line one\nline two\",\"a \"\"b\"\"\"\n", i)
		fmt.Fprintf(&esc, "%d,\"say \\\"hi\\\"\nthere\",z\n", i)
		fmt.Fprintf(&cr, "%d,b,\"c\rd\"\r", i)
		fmt.Fprintf(&big, "%d,%s\n", i, strings.Repeat("w", i%97))
	}
	cases := []struct {
		name string
		in   string
		d    Dialect
	}{
		{"parity flipped", flip.String(), Dialect{Delimiter: ',', Quote: '"'}},
		{"escapes", esc.String(), Dialect{Delimiter: ',', Quote: '"', Escape: 0x5C}},
		{"carriage returns", cr.String(), Dialect{Delimiter: ',', Quote: '"'}},
		{"long lines", big.String(), Dialect{Delimiter: ',', Quote: '"'}},
	}
	for _, c := range cases {
		for _, size := range []int{1, 7, 64, 1000} {
			for _, slices := range []bool{false, true} {
				opts := Options{Dialect: &c.d, Encoding: UTF8, Limits: Limits{SampleBytes: 16}}
				src := func(b []byte) io.Reader { return bytes.NewReader(b) }
				want := readAllWays(t, []byte(c.in), opts, 0, size, src, slices)
				got := readAllWays(t, []byte(c.in), opts, 4, size, src, slices)
				if !reflect.DeepEqual(got, want) {
					for i := range min(len(got), len(want)) {
						if got[i] != want[i] {
							t.Fatalf("%s, chunks of %d, slices %v: first difference at %d:\ngot  %s\nwant %s", c.name, size, slices, i, got[i], want[i])
						}
					}
					t.Fatalf("%s, chunks of %d: %d results, want %d", c.name, size, len(got), len(want))
				}
			}
		}
	}
}
