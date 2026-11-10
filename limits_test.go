package wildcsv

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestDocumentedLimits keeps the limits table of docs/design.md equal to the
// defaults in the code.
func TestDocumentedLimits(t *testing.T) {
	design, err := os.ReadFile("docs/design.md")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"SampleBytes":    DefaultSampleBytes,
		"MaxFieldBytes":  DefaultMaxFieldBytes,
		"MaxRecordBytes": DefaultMaxRecordBytes,
		"MaxFields":      DefaultMaxFields,
	}
	row := regexp.MustCompile("(?m)^\\| `(\\w+)` \\| ([0-9,]+) \\|")
	found := 0
	for _, m := range row.FindAllStringSubmatch(string(design), -1) {
		def, ok := want[m[1]]
		if !ok {
			continue
		}
		found++
		got, err := strconv.Atoi(strings.ReplaceAll(m[2], ",", ""))
		if err != nil || got != def {
			t.Errorf("design.md documents %s = %s, the code uses %d", m[1], m[2], def)
		}
	}
	if found != len(want) {
		t.Errorf("found %d of %d limits in design.md", found, len(want))
	}
}
