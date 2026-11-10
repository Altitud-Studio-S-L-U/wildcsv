package wildcsv

import (
	"bytes"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxScoredRecords is the largest number of sample records a candidate is
// scored on; it is also δ in the score.
const maxScoredRecords = 50

var defaultDelimiters = []rune{',', ';', '\t', '|', ':', '=', ' ', '#', '*'}

// sampleTable is the sample read with one candidate dialect.
type sampleTable struct {
	records [][]string
	quoted  [][]bool
	kinds   [][]ValueKind
	decs    [][]int
	defects int64 // bare and stray quotes
}

// readTable reads up to max records of the sample with dialect d. When the
// sample is not the whole input, its last record may be cut and is dropped.
// Records that cannot be read are left out, and so are records whose first
// field is unquoted and starts with '#' when the delimiter is not '#'.
func readTable(text []byte, d Dialect, lim Limits, complete bool, max int) *sampleTable {
	sc := newSliceScanner(text, d, lim)
	t := &sampleTable{}
	lastWasRecord := false
	for len(t.records) <= max {
		err := sc.readRecord()
		if err == io.EOF {
			break
		}
		if err != nil {
			lastWasRecord = false
			continue
		}
		// Comment lines read with any other delimiter look like a column of
		// text; leaving them out lets the table show its real shape.
		if d.Delimiter != '#' && !sc.fields[0].quoted && sc.fields[0].end > sc.fields[0].start && sc.out[sc.fields[0].start] == '#' {
			lastWasRecord = false
			continue
		}
		str := sc.recordString()
		rec := make([]string, len(sc.fields))
		quoted := make([]bool, len(sc.fields))
		for i, f := range sc.fields {
			rec[i] = str[f.start:f.end]
			quoted[i] = f.quoted
		}
		t.records = append(t.records, rec)
		t.quoted = append(t.quoted, quoted)
		lastWasRecord = true
	}
	switch {
	case len(t.records) > max:
		t.records, t.quoted = t.records[:max], t.quoted[:max]
	case !complete && lastWasRecord:
		t.records, t.quoted = t.records[:len(t.records)-1], t.quoted[:len(t.quoted)-1]
	}
	t.defects = sc.bare + sc.stray
	t.kinds = make([][]ValueKind, len(t.records))
	t.decs = make([][]int, len(t.records))
	for i, rec := range t.records {
		t.kinds[i] = make([]ValueKind, len(rec))
		t.decs[i] = make([]int, len(rec))
		for j, c := range rec {
			t.kinds[i][j], t.decs[i][j] = classify(c, t.quoted[i][j])
		}
	}
	return t
}

// entropy returns the Shannon entropy, in bits, of a distribution given by
// counts. The counts are summed in sorted order so the result never depends
// on map iteration.
func entropy(counts []int, n int) float64 {
	slices.Sort(counts)
	h := 0.0
	for _, c := range counts {
		p := float64(c) / float64(n)
		h -= p * math.Log2(p)
	}
	return h
}

func median(v []float64) float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

// score implements design §7.4: how typed the cells are times how regular the
// table is.
func score(t *sampleTable) float64 {
	n := len(t.records)
	if n == 0 {
		return 0
	}
	ks := make([]float64, n)
	minK, maxK := len(t.records[0]), 0
	for i, rec := range t.records {
		k := len(rec)
		ks[i] = float64(k)
		minK, maxK = min(minK, k), max(maxK, k)
	}
	if maxK == 0 {
		return 0
	}

	// Weight: typed cells weigh 100, others 0.1.
	total := 0.0
	colSum := make([]float64, maxK)
	for _, rec := range t.kinds {
		for j, k := range rec {
			w := 0.1
			if k != ValueOther {
				w = 100
			}
			total += w
			colSum[j] += w
		}
	}
	lambda := total / float64(maxK)

	// Consistency.
	tau0 := 1.0
	if n > 1 {
		med := median(ks)
		devs := make([]float64, n)
		for i, k := range ks {
			devs[i] = math.Abs(k - med)
		}
		tau0 = 1 / (1 + 2*1.4826*median(devs))
	}

	// Dispersion.
	alpha, run, longest := 0, 1, 1
	for i := 1; i < n; i++ {
		if ks[i] != ks[i-1] {
			alpha++
			run = 1
		} else {
			run++
		}
		longest = max(longest, run)
	}
	countByK := map[int]int{}
	for _, k := range ks {
		countByK[int(k)]++
	}
	kCounts := make([]int, 0, len(countByK))
	for _, c := range countByK {
		kCounts = append(kCounts, c)
	}
	tau1 := 0.0
	if alpha > 0 {
		s := float64(longest)
		r := float64(maxK - minK)
		a := float64(alpha)
		tau1 = 2 * r * (a*a + 1) * ((1 - s/float64(n)) / s) * (1 + entropy(kCounts, n))
	}

	// Column coherence.
	c := make([]float64, maxK)
	mean := 0.0
	for j := range c {
		c[j] = colSum[j] / float64(n)
		mean += c[j]
	}
	mean /= float64(maxK)
	maxD := 0.0
	d := make([]float64, maxK)
	for j := range c {
		d[j] = math.Abs(c[j] - mean)
		maxD = max(maxD, d[j])
	}
	tau2 := 1.0
	if maxD > 0 {
		sum := 0.0
		for _, dj := range d {
			sum += 1 - dj/maxD
		}
		tau2 = sum / float64(maxK)
	}

	// Shape entropy.
	shapes := map[string]int{}
	var key strings.Builder
	for _, rec := range t.records {
		key.Reset()
		for _, cell := range rec {
			key.WriteString(strconv.Itoa(len(cell)))
			key.WriteByte(',')
		}
		shapes[key.String()]++
	}
	shapeCounts := make([]int, 0, len(shapes))
	for _, c := range shapes {
		shapeCounts = append(shapeCounts, c)
	}
	tau3 := entropy(shapeCounts, n)

	const delta = maxScoredRecords
	return (tau0/delta + 1/(tau1+float64(n)) + tau2/delta + 1/(tau3+float64(n))) * lambda
}

// candidates builds the dialects worth scoring from the characters that occur
// in the sample, in the fixed order of design §7.2.
func candidates(text []byte, s *settings) []Dialect {
	delims := s.Delimiters
	if delims == nil {
		delims = defaultDelimiters
	}
	var quotes []rune
	if s.Quotes != nil {
		quotes = s.Quotes
	} else {
		quotes = []rune{'"'}
		if bytes.IndexByte(text, '"') >= 0 {
			quotes = append(quotes, 0)
		}
		if bytes.IndexByte(text, '\'') >= 0 {
			quotes = append(quotes, '\'')
		}
	}
	var out []Dialect
	for _, d := range delims {
		if !bytes.ContainsRune(text, d) {
			continue
		}
		for _, q := range quotes {
			if q == d {
				continue
			}
			escapes := []rune{0}
			if q != 0 && d != '\\' && bytes.Contains(text, utf8.AppendRune([]byte{'\\'}, q)) {
				escapes = append(escapes, '\\')
			}
			skips := []bool{false}
			if q != 0 && d != ' ' && spacesBeforeQuote(text, d, q) {
				skips = append(skips, true)
			}
			for _, e := range escapes {
				for _, sk := range skips {
					out = append(out, Dialect{Delimiter: d, Quote: q, Escape: e, SkipInitialSpace: sk})
				}
			}
		}
	}
	return out
}

// spacesBeforeQuote reports whether a delimiter is followed by spaces and then
// a quote somewhere in the text.
func spacesBeforeQuote(text []byte, d, q rune) bool {
	ds := utf8.AppendRune(nil, d)
	qs := utf8.AppendRune(nil, q)
	for i := 0; ; {
		j := bytes.Index(text[i:], ds)
		if j < 0 {
			return false
		}
		k := i + j + len(ds)
		m := k
		for m < len(text) && text[m] == ' ' {
			m++
		}
		if m > k && bytes.HasPrefix(text[m:], qs) {
			return true
		}
		i = k
	}
}

type scored struct {
	d     Dialect
	score float64
	table *sampleTable
}

// sameScore compares scores with a tolerance, so that equal tables computed
// along different paths still tie.
func sameScore(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(math.Abs(a), math.Abs(b))
}

// better reports whether a beats b: a higher score, then fewer quoting
// defects. Earlier candidates win remaining ties because the caller keeps the
// first.
func better(a, b scored) bool {
	if !sameScore(a.score, b.score) {
		return a.score > b.score
	}
	return a.table.defects < b.table.defects
}

// detectDialect chooses the dialect of a sample.
func detectDialect(text []byte, complete bool, s *settings) (Dialect, float64, []Alternative, *sampleTable) {
	cands := candidates(text, s)
	if len(cands) == 0 {
		d := Dialect{Delimiter: ',', Quote: '"'}
		if len(s.Delimiters) > 0 {
			d.Delimiter = s.Delimiters[0]
		}
		if len(s.Quotes) > 0 {
			d.Quote = s.Quotes[0]
		}
		if d.Quote == d.Delimiter {
			d.Quote = 0
		}
		return d, 1, nil, readTable(text, d, s.lim, complete, maxScoredRecords)
	}
	all := make([]scored, len(cands))
	best := 0
	for i, d := range cands {
		t := readTable(text, d, s.lim, complete, maxScoredRecords)
		all[i] = scored{d: d, score: score(t), table: t}
		if i > 0 && better(all[i], all[best]) {
			best = i
		}
	}
	win := all[best]

	// The best candidate of each other delimiter, in candidate order.
	var others []scored
	for _, c := range all {
		if c.d.Delimiter == win.d.Delimiter {
			continue
		}
		if i := slices.IndexFunc(others, func(o scored) bool { return o.d.Delimiter == c.d.Delimiter }); i >= 0 {
			if better(c, others[i]) {
				others[i] = c
			}
			continue
		}
		others = append(others, c)
	}
	slices.SortStableFunc(others, func(a, b scored) int {
		switch {
		case better(a, b):
			return -1
		case better(b, a):
			return 1
		}
		return 0
	})

	confidence := 0.0
	if win.score > 0 {
		confidence = 1
		if len(others) > 0 {
			confidence = max(0, 1-others[0].score/win.score)
		}
	}
	var alts []Alternative
	for _, o := range others[:min(3, len(others))] {
		rel := 0.0
		if win.score > 0 {
			rel = o.score / win.score
		}
		alts = append(alts, Alternative{Dialect: o.d, RelativeScore: rel})
	}
	return win.d, confidence, alts, win.table
}
