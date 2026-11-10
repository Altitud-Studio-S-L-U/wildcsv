// Package accuracy measures dialect detection on the public benchmark sets
// that other tools publish results for: POLLOCK, the W3C CSV on the Web test
// files and the CSV Wrangling files, with the annotations and the pass rule of
// García's CSVsniffer benchmark. It uses wildcsv only through its public API.
package accuracy

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Altitud-Studio-S-L-U/wildcsv"
)

// Repository and commit the benchmark files are fetched from.
const (
	Repository = "https://github.com/ws-garcia/CSVsniffer.git"
	Commit     = "bdb631d85007353fe09995c303b042725438e30f"
)

// SparsePaths are the directories of the repository the benchmark needs.
var SparsePaths = []string{"python/CSV", "python/W3C-CSVW", "python/CSV_Wrangling", "python/ground truth"}

// Row is one line of the results table.
type Row struct {
	Name        string
	Set         string // the name the split hashes; the CSV Wrangling rows share one
	Dir         string // files, relative to the checkout
	Annotations string // ground truth, relative to the checkout
	OnlyMessy   bool   // run only the rows whose normal_file column is "no"
}

// Rows are the five rows of the published tables, in their order.
var Rows = []Row{
	{Name: "POLLOCK", Set: "POLLOCK", Dir: "python/CSV", Annotations: "python/ground truth/POLLOCK-dialect_annotations.txt"},
	{Name: "W3C-CSVW", Set: "W3C-CSVW", Dir: "python/W3C-CSVW", Annotations: "python/ground truth/W3C-CSVW-dialect_annotations.txt"},
	{Name: "CSV Wrangling", Set: "CSV_Wrangling", Dir: "python/CSV_Wrangling", Annotations: "python/ground truth/CCSV-manual_Dialect_Annotation.txt"},
	{Name: "CSV Wrangling CODEC", Set: "CSV_Wrangling", Dir: "python/CSV_Wrangling", Annotations: "python/ground truth/CCSV-manual_Dialect_Annotation_CODEC.txt"},
	{Name: "CSV Wrangling MESSY", Set: "CSV_Wrangling", Dir: "python/CSV_Wrangling", Annotations: "python/ground truth/CCSV-manual_Dialect_Annotation_CODEC.txt", OnlyMessy: true},
}

// Annotation is the expected dialect of one file.
type Annotation struct {
	File      string
	Delimiter string // comma, semicolon, tab, space, vslash, nsign, colon, eqsign, star
	Quote     string // doublequote, singlequote, tilde
	Normal    string // "yes" or "no" in the CODEC file, "" elsewhere
}

// LoadAnnotations reads a ground truth file: a comment line, a header line,
// then file_name|encoding|fields_delimiter|quotechar|escapechar|records_delimiter[|normal_file].
func LoadAnnotations(path string) ([]Annotation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Annotation
	sc := bufio.NewScanner(f)
	header := false
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !header {
			header = true
			continue
		}
		cols := strings.Split(line, "|")
		if len(cols) < 6 {
			return nil, fmt.Errorf("%s: malformed line %q", path, line)
		}
		a := Annotation{File: cols[0], Delimiter: cols[2], Quote: cols[3]}
		if len(cols) > 6 {
			a.Normal = cols[6]
		}
		out = append(out, a)
	}
	return out, sc.Err()
}

// Split names.
const (
	Dev     = "dev"
	HeldOut = "heldout"
	All     = "all"
)

// InSplit reports whether a file belongs to a split: the development half
// holds the files whose first byte of SHA-256("<set>/<file>") is even.
func InSplit(set, file, split string) bool {
	if split == All {
		return true
	}
	sum := sha256.Sum256([]byte(set + "/" + file))
	return (sum[0]%2 == 0) == (split == Dev)
}

// DelimiterName names a delimiter as the annotations do.
func DelimiterName(r rune) string {
	switch r {
	case ',':
		return "comma"
	case ';':
		return "semicolon"
	case '\t':
		return "tab"
	case ' ':
		return "space"
	case '|':
		return "vslash"
	case '#':
		return "nsign"
	case ':':
		return "colon"
	case '=':
		return "eqsign"
	case '*':
		return "star"
	}
	return fmt.Sprintf("%q", r)
}

// QuoteName names a quote as the annotations do; no quoting counts as the
// double quote, as in the reference benchmark.
func QuoteName(r rune) string {
	switch r {
	case '"', 0:
		return "doublequote"
	case '\'':
		return "singlequote"
	case '~':
		return "tilde"
	}
	return fmt.Sprintf("%q", r)
}

// FileResult is the outcome for one file.
type FileResult struct {
	Row        string
	File       string
	Expected   string
	Detected   string // "" when the file failed
	Pass       bool
	Failed     bool // no dialect: an error or a kind other than delimited
	Confidence float64
	Elapsed    time.Duration
}

// RowResult holds the figures of one row.
type RowResult struct {
	Row      string
	N        int // the denominator of the reference-compatible figures
	Run      int // files run
	Passes   int
	Failures int
}

// Accuracy counts a failure as wrong: passes over files run.
func (r RowResult) Accuracy() float64 { return ratio(r.Passes, r.Run) }

// SuccessRatio is the reference's figure: passes over N minus failures.
func (r RowResult) SuccessRatio() float64 { return ratio(r.Passes, r.N-r.Failures) }

// FailureRatio is failures over N.
func (r RowResult) FailureRatio() float64 { return ratio(r.Failures, r.N) }

// PRF returns precision, recall and F1 as the reference computes them:
// TP = passes, FP = N - passes - failures, FN = failures.
func (r RowResult) PRF() (p, rec, f1 float64) {
	tp, fp, fn := r.Passes, r.N-r.Passes-r.Failures, r.Failures
	p, rec = ratio(tp, tp+fp), ratio(tp, tp+fn)
	if p+rec > 0 {
		f1 = 2 * p * rec / (p + rec)
	}
	return p, rec, f1
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// WeightedF1 is Σ TPᵢ·F1ᵢ / Σ TPᵢ over the rows.
func WeightedF1(rows []RowResult) float64 {
	num, den := 0.0, 0
	for _, r := range rows {
		_, _, f1 := r.PRF()
		num += float64(r.Passes) * f1
		den += r.Passes
	}
	if den == 0 {
		return 0
	}
	return num / float64(den)
}

// Report is the result of a run.
type Report struct {
	Split string
	Rows  []RowResult
	Files []FileResult
}

// Run detects the dialect of every annotated file of the split.
func Run(dir, split string) (*Report, error) {
	rep := &Report{Split: split}
	for _, row := range Rows {
		anns, err := LoadAnnotations(filepath.Join(dir, row.Annotations))
		if err != nil {
			return nil, err
		}
		rr := RowResult{Row: row.Name}
		for _, a := range anns {
			if !InSplit(row.Set, a.File, split) {
				continue
			}
			rr.N++ // MESSY keeps the CODEC denominator, as the reference does
			if row.OnlyMessy && a.Normal != "no" {
				continue
			}
			fr, err := detectFile(filepath.Join(dir, row.Dir, a.File))
			if err != nil {
				return nil, err
			}
			fr.Row, fr.File = row.Name, a.File
			fr.Expected = a.Delimiter + "/" + a.Quote
			fr.Pass = !fr.Failed && fr.Detected == fr.Expected
			rr.Run++
			if fr.Failed {
				rr.Failures++
			} else if fr.Pass {
				rr.Passes++
			}
			rep.Files = append(rep.Files, fr)
		}
		rep.Rows = append(rep.Rows, rr)
	}
	return rep, nil
}

func detectFile(path string) (FileResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileResult{}, err
	}
	defer f.Close()
	start := time.Now()
	det, err := wildcsv.Detect(f, nil)
	elapsed := time.Since(start)
	if err != nil || det.Kind != wildcsv.KindDelimited {
		return FileResult{Failed: true, Elapsed: elapsed}, nil
	}
	return FileResult{
		Detected:   DelimiterName(det.Dialect.Delimiter) + "/" + QuoteName(det.Dialect.Quote),
		Confidence: det.Confidence,
		Elapsed:    elapsed,
	}, nil
}

// Percentile returns the p-th percentile of the detection times.
func (r *Report) Percentile(p float64) time.Duration {
	if len(r.Files) == 0 {
		return 0
	}
	d := make([]time.Duration, len(r.Files))
	for i, f := range r.Files {
		d[i] = f.Elapsed
	}
	slices.Sort(d)
	i := int(p/100*float64(len(d)-1) + 0.5)
	return d[i]
}

// Band is accuracy within a range of confidence.
type Band struct {
	Low, High float64
	Files     int
	Passes    int
}

// Bands groups the files of distinct rows by confidence. Each file counts once,
// in the first row that lists it.
func (r *Report) Bands() []Band {
	bands := []Band{{0, 0.2, 0, 0}, {0.2, 0.5, 0, 0}, {0.5, 0.8, 0, 0}, {0.8, 1.0001, 0, 0}}
	seen := map[string]bool{}
	for _, f := range r.Files {
		key := f.File
		if seen[key] {
			continue
		}
		seen[key] = true
		for i := range bands {
			if f.Confidence >= bands[i].Low && f.Confidence < bands[i].High {
				bands[i].Files++
				if f.Pass {
					bands[i].Passes++
				}
				break
			}
		}
	}
	return bands
}
