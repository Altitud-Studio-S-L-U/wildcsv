package accuracy

import (
	"os"
	"testing"
)

func TestMetricsMatchTheReference(t *testing.T) {
	// The best published MESSY row: 114 passes and no failures over the 142
	// files of the CODEC annotations.
	r := RowResult{N: 142, Run: 126, Passes: 114}
	if got := r.SuccessRatio(); got < 0.8028 || got > 0.8029 {
		t.Errorf("success ratio = %v, want 0.8028", got)
	}
	if got := r.Accuracy(); got < 0.9047 || got > 0.9048 {
		t.Errorf("accuracy = %v, want 0.9048", got)
	}
}

func TestNames(t *testing.T) {
	for r, want := range map[rune]string{',': "comma", ';': "semicolon", '\t': "tab", ' ': "space", '|': "vslash", '#': "nsign", ':': "colon", '=': "eqsign", '*': "star"} {
		if got := DelimiterName(r); got != want {
			t.Errorf("DelimiterName(%q) = %s, want %s", r, got, want)
		}
	}
	if QuoteName(0) != "doublequote" || QuoteName('"') != "doublequote" || QuoteName('\'') != "singlequote" {
		t.Error("quote names")
	}
}

// TestSplitSizes needs the benchmark files; set WILDCSV_CORPUS to the
// directory made by `accuracy fetch`.
func TestSplitSizes(t *testing.T) {
	dir := os.Getenv("WILDCSV_CORPUS")
	if dir == "" {
		t.Skip("WILDCSV_CORPUS is not set")
	}
	want := map[string][3]int{
		"POLLOCK":             {148, 80, 68},
		"W3C-CSVW":            {221, 119, 102},
		"CSV Wrangling":       {179, 90, 89},
		"CSV Wrangling CODEC": {142, 71, 71},
		"CSV Wrangling MESSY": {126, 61, 65},
	}
	for _, row := range Rows {
		anns, err := LoadAnnotations(dir + "/" + row.Annotations)
		if err != nil {
			t.Fatal(err)
		}
		var got [3]int
		for _, a := range anns {
			if row.OnlyMessy && a.Normal != "no" {
				continue
			}
			got[0]++
			if InSplit(row.Set, a.File, Dev) {
				got[1]++
			} else {
				got[2]++
			}
		}
		if got != want[row.Name] {
			t.Errorf("%s: all/dev/held-out = %v, want %v", row.Name, got, want[row.Name])
		}
	}
}
