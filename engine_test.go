package wildcsv

// Tests from an independent review of the reading engine. FieldPos after
// recycling, the fallback handing back, Stats.Encoding and the abandoned
// Reader with a Warn closure each failed before its fix; the others compare
// large inputs read with one goroutine and with several.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func withChunk(size int, f func()) {
	saved := chunkBytes
	chunkBytes = size
	defer func() { chunkBytes = saved }()
	f()
}

// Records whose second field starts at a column that changes record by record.
func varyingInput(n int) []byte {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%s,%d\n", strings.Repeat("x", i%13), i)
	}
	return []byte(b.String())
}

// FieldPos is called on record 0 and then only on record j: a recycled chunk
// struct with the same record index returns the cached positions of record 0.
func TestParallelFieldPosAfterRecycling(t *testing.T) {
	in := varyingInput(600)
	d := Dialect{Delimiter: ',', Quote: '"'}
	want := map[int]int{}
	{
		r, _ := NewReader(bytes.NewReader(in), &Options{Dialect: &d, Encoding: UTF8, Concurrency: 1, Limits: Limits{SampleBytes: 16}})
		for i := 0; ; i++ {
			if _, err := r.Read(); err != nil {
				break
			}
			_, c := r.FieldPos(1)
			want[i] = c
		}
	}
	bad := 0
	for j := 1; j < 600; j++ {
		var r *Reader
		withChunk(32, func() {
			r, _ = NewReader(bytes.NewReader(in), &Options{Dialect: &d, Encoding: UTF8, Concurrency: 2, Limits: Limits{SampleBytes: 16}})
		})
		for i := 0; i <= j; i++ {
			if _, err := r.Read(); err != nil {
				t.Fatal(err)
			}
			if i == 0 || i == j {
				if _, c := r.FieldPos(1); c != want[i] && i == j {
					if bad == 0 {
						t.Errorf("record %d: FieldPos(1) column %d, want %d", j, c, want[j])
					}
					bad++
				}
			}
		}
		r.Close()
	}
	if bad > 0 {
		t.Fatalf("%d of 599 records had a stale FieldPos", bad)
	}
}

// After one forced chunk (a line longer than 4 chunks) the fallback should
// land on a later chunk start and give the rest back to the workers.
func TestParallelFallbackHandsBack(t *testing.T) {
	var b strings.Builder
	b.WriteString("a,b\n")
	b.WriteString(strings.Repeat("y", 5*4096))
	b.WriteString(",z\n")
	for i := range 40000 {
		fmt.Fprintf(&b, "%d,row\n", i)
	}
	d := Dialect{Delimiter: ',', Quote: '"'}
	var r *Reader
	withChunk(4096, func() {
		r, _ = NewReader(strings.NewReader(b.String()), &Options{Dialect: &d, Encoding: UTF8, Concurrency: 4, Limits: Limits{SampleBytes: 16}})
	})
	defer r.Close()
	fb, total := 0, 0
	for {
		_, err := r.Read()
		if err != nil {
			break
		}
		total++
		if r.par.fb != nil || r.par.lastChunk == nil {
			fb++
		}
	}
	if fb > 2000 {
		t.Fatalf("%d of %d records were read by the sequential fallback", fb, total)
	}
}

// Stats().Encoding depends on how far ahead the producer has read.
func TestStatsEncodingNotAhead(t *testing.T) {
	var b strings.Builder
	b.WriteString("name,value\n")
	for b.Len() < 600<<10 {
		b.WriteString("plain,ascii\n")
	}
	b.WriteString("caf\xe9,x\n")
	for b.Len() < 2<<20 {
		b.WriteString("plain,ascii\n")
	}
	enc := func(workers int) Encoding {
		r, err := NewReader(strings.NewReader(b.String()), &Options{Concurrency: workers})
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if _, err := r.Read(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		return r.Stats().Encoding
	}
	if a, b := enc(1), enc(4); a != b {
		t.Fatalf("Stats().Encoding after one record: %q with 1 goroutine, %q with 4", a, b)
	}
}

// bigInput mixes quoted fields with line feeds, doubled quotes, multi-byte
// text, bare and stray quotes, invalid UTF-8 and blank lines.
func bigInput(n int, seed int) []byte {
	var b strings.Builder
	b.WriteString("skip me\nand me \"\n")
	for i := range n {
		switch (i*7 + seed) % 23 {
		case 0:
			fmt.Fprintf(&b, "%d,\"multi\nline \"\"q\"\" ñ\",z\n", i)
		case 5:
			fmt.Fprintf(&b, "%d,ba\"re,%s\n", i, strings.Repeat("é", i%9))
		case 9:
			fmt.Fprintf(&b, "%d,\"st\"ray\",x\r\n", i)
		case 13:
			fmt.Fprintf(&b, "%d,inv\xffalid,\"\"\n\n", i)
		case 17:
			fmt.Fprintf(&b, "%d,\"%s\",cr\r", i, strings.Repeat("w", i%300))
		default:
			fmt.Fprintf(&b, "%d,%s,\"q\"\"d\"\n", i, strings.Repeat("v", i%40))
		}
	}
	return []byte(b.String())
}

type stepFn func(i int) (slices, pos bool)

func runLog(t *testing.T, src io.Reader, opts Options, step stepFn) []string {
	t.Helper()
	var warns []string
	opts.Warn = func(pe *ParseError) { warns = append(warns, fmt.Sprintf("warn %+v", *pe)) }
	r, err := NewReader(src, &opts)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var log []string
	for i := 0; ; i++ {
		slices, pos := step(i)
		var rec []string
		if slices {
			var b [][]byte
			b, err = r.ReadSlices()
			for _, f := range b {
				rec = append(rec, string(f))
			}
		} else {
			var s []string
			s, err = r.Read()
			rec = append(rec, s...)
		}
		log = append(log, warns...)
		warns = warns[:0]
		if pe, ok := errors.AsType[*ParseError](err); ok {
			log = append(log, fmt.Sprintf("error %+v", *pe))
			continue
		}
		if err != nil {
			log = append(log, "end "+err.Error())
			break
		}
		s := fmt.Sprintf("%q off %d", rec, r.InputOffset())
		if pos {
			for k := range rec {
				l, c := r.FieldPos(k)
				s += fmt.Sprintf(" %d:%d", l, c)
			}
		}
		log = append(log, s)
	}
	return append(log, fmt.Sprintf("stats %+v", r.Stats()))
}

func diffLogs(t *testing.T, name string, got, want []string) {
	t.Helper()
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			t.Errorf("%s: first difference at %d:\ngot  %.300s\nwant %.300s", name, i, got[i], want[i])
			return
		}
	}
	if len(got) != len(want) {
		t.Errorf("%s: %d results, want %d; last got %.200s", name, len(got), len(want), got[len(got)-1])
	}
}

func TestParallelLargeMixed(t *testing.T) {
	in := bigInput(60000, 0)
	t.Logf("input %d bytes", len(in))
	d := Dialect{Delimiter: ',', Quote: '"'}
	steps := map[string]stepFn{
		"read":   func(i int) (bool, bool) { return false, i%5 == 0 },
		"slices": func(i int) (bool, bool) { return true, i%3 == 0 },
		"mixed":  func(i int) (bool, bool) { return (i/3)%2 == 1, i%7 == 0 },
		"mixed2": func(i int) (bool, bool) { return i%2 == 0, i%11 == 3 },
	}
	srcs := map[string]func() io.Reader{
		"plain": func() io.Reader { return bytes.NewReader(in) },
		"err": func() io.Reader {
			return io.MultiReader(bytes.NewReader(in[:len(in)*2/3]), iotest.ErrReader(errInjected))
		},
	}
	for _, strict := range []bool{false, true} {
		for _, reuse := range []bool{false, true} {
			for sn, src := range srcs {
				for name, step := range steps {
					opts := Options{Dialect: &d, Encoding: UTF8, Strict: strict, ReuseRecord: reuse, SkipLines: 2}
					opts.Concurrency = 1
					want := runLog(t, src(), opts, step)
					for _, w := range []int{0, 3, 8} {
						opts.Concurrency = w
						got := runLog(t, src(), opts, step)
						diffLogs(t, fmt.Sprintf("strict %v reuse %v src %s %s workers %d", strict, reuse, sn, name, w), got, want)
					}
				}
			}
		}
	}
}

func toUTF16LE(b []byte) []byte {
	out := []byte{0xFF, 0xFE}
	for _, r := range string(b) {
		if r >= 0x10000 {
			r = '?'
		}
		out = append(out, byte(r), byte(r>>8))
	}
	return out
}

func TestParallelDialectsAndEncodings(t *testing.T) {
	base := bigInput(60000, 3)
	skipMany := append([]byte(strings.Repeat("junk \"line\n", 40000)), base...)
	cases := []struct {
		name string
		in   []byte
		opts Options
	}{
		{"utf16", toUTF16LE(base), Options{SkipLines: 2}},
		{"cp1252", bytes.ReplaceAll(bytes.ReplaceAll(base, []byte("é"), []byte{0xE9}), []byte("ñ"), []byte{0xF1}), Options{SkipLines: 2, Encoding: Windows1252, Dialect: &Dialect{Delimiter: ',', Quote: '"'}}},
		{"late1252", append(bytes.Repeat([]byte("abc,def\n"), 200000), bytes.ReplaceAll(base, []byte("é"), []byte{0xE9})...), Options{}},
		{"multibyte", bytes.ReplaceAll(bytes.ReplaceAll(base, []byte(","), []byte("§")), []byte("\""), []byte("«")), Options{SkipLines: 2, Encoding: UTF8, Dialect: &Dialect{Delimiter: '§', Quote: '«'}}},
		{"escape", bytes.ReplaceAll(base, []byte("\"\""), []byte("\\\"")), Options{SkipLines: 2, Encoding: UTF8, Dialect: &Dialect{Delimiter: ',', Quote: '"', Escape: 0x5C}}},
		{"skipmany", skipMany, Options{SkipLines: 40002, Encoding: UTF8, Dialect: &Dialect{Delimiter: ',', Quote: '"'}}},
		{"skipspace", bytes.ReplaceAll(base, []byte(","), []byte(", ")), Options{SkipLines: 2, Encoding: UTF8, Dialect: &Dialect{Delimiter: ',', Quote: '"', SkipInitialSpace: true}}},
	}
	step := func(i int) (bool, bool) { return (i/5)%2 == 1, i%3 == 0 }
	for _, c := range cases {
		for _, reuse := range []bool{false, true} {
			opts := c.opts
			opts.ReuseRecord = reuse
			opts.Concurrency = 1
			want := runLog(t, bytes.NewReader(c.in), opts, step)
			for _, w := range []int{0, 4} {
				opts.Concurrency = w
				got := runLog(t, bytes.NewReader(c.in), opts, step)
				diffLogs(t, fmt.Sprintf("%s reuse %v workers %d", c.name, reuse, w), got, want)
			}
		}
	}
}

func TestParallelStopsGoroutines(t *testing.T) {
	in := bigInput(200000, 1)
	d := Dialect{Delimiter: ',', Quote: '"'}
	baseline := runtime.NumGoroutine()
	wait := func(what string) {
		for range 100 {
			runtime.GC()
			time.Sleep(20 * time.Millisecond)
			if runtime.NumGoroutine() <= baseline {
				return
			}
		}
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Fatalf("%s: %d goroutines, baseline %d\n%s", what, runtime.NumGoroutine(), baseline, buf[:n])
	}
	for _, w := range []int{0, 4} {
		func() {
			r, _ := NewReader(bytes.NewReader(in), &Options{Dialect: &d, Encoding: UTF8, Concurrency: w})
			for range 50000 {
				r.Read()
			}
			r.Close()
		}()
		wait(fmt.Sprintf("closed, workers %d", w))
		func() {
			r, _ := NewReader(bytes.NewReader(in), &Options{Dialect: &d, Encoding: UTF8, Concurrency: w})
			for range 50000 {
				r.ReadSlices()
			}
		}()
		wait(fmt.Sprintf("abandoned, workers %d", w))
	}
}

// A Warn callback that refers to the Reader keeps it reachable from the
// goroutines, so the cleanup never runs for an abandoned Reader.
func TestParallelAbandonedWithWarnClosure(t *testing.T) {
	in := bigInput(200000, 1)
	d := Dialect{Delimiter: ',', Quote: '"'}
	baseline := runtime.NumGoroutine()
	func() {
		var r *Reader
		opts := &Options{Dialect: &d, Encoding: UTF8, Concurrency: 4, Warn: func(*ParseError) { _ = r.InputOffset() }}
		r, _ = NewReader(bytes.NewReader(in), opts)
		for range 1000 {
			r.Read()
		}
	}()
	for range 100 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		if runtime.NumGoroutine() <= baseline {
			return
		}
	}
	t.Fatalf("abandoned Reader: %d goroutines still running, baseline %d", runtime.NumGoroutine(), baseline)
}
