package main

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func runCmd(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errOut)
	return code, out.String(), errOut.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestGolden(t *testing.T) {
	cases := []struct {
		name string
		args []string
		code int
	}{
		{"inspect-orders", []string{"inspect", "testdata/orders.csv"}, 0},
		{"inspect-orders-json", []string{"inspect", "--json", "testdata/orders.csv"}, 0},
		{"inspect-latin1", []string{"inspect", "testdata/latin1.csv"}, 0},
		{"inspect-jsonl-json", []string{"inspect", "--json", "testdata/lines.jsonl"}, 0},
		{"inspect-bzip2-json", []string{"inspect", "--json", "testdata/tabs.tsv.bz2"}, 0},
		{"check-orders", []string{"check", "testdata/orders.csv"}, 0},
		{"check-problems", []string{"check", "testdata/problems.csv"}, 1},
		{"check-problems-json", []string{"check", "--json", "testdata/problems.csv"}, 1},
		{"check-problems-strict-json", []string{"check", "--json", "--strict", "testdata/problems.csv"}, 1},
		{"check-jsonl-json", []string{"check", "--json", "testdata/lines.jsonl"}, 1},
		{"convert-orders", []string{"convert", "testdata/orders.csv"}, 0},
		{"convert-latin1-crlf", []string{"convert", "--crlf", "testdata/latin1.csv"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, _ := runCmd(t, "", c.args...)
			if code != c.code {
				t.Errorf("exit status %d, want %d", code, c.code)
			}
			golden(t, c.name, out)
		})
	}
}

func TestStdinAndCompression(t *testing.T) {
	data, _ := os.ReadFile("testdata/orders.csv")
	_, fromFile, _ := runCmd(t, "", "convert", "testdata/orders.csv")
	code, fromStdin, _ := runCmd(t, string(data), "convert")
	if code != 0 || fromStdin != fromFile {
		t.Errorf("standard input: status %d, output %q", code, fromStdin)
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write(data)
	w.Close()
	code, out, _ := runCmd(t, gz.String(), "inspect", "--json", "-")
	if code != 0 || !strings.Contains(out, `"compression": "gzip"`) || !strings.Contains(out, `"delimiter": ";"`) {
		t.Errorf("gzip: status %d, output %s", code, out)
	}
}

func TestExitStatuses(t *testing.T) {
	cases := []struct {
		args []string
		code int
	}{
		{[]string{}, 2},
		{[]string{"nope"}, 2},
		{[]string{"inspect", "testdata/missing.csv"}, 2},
		{[]string{"inspect", "--header", "maybe", "testdata/orders.csv"}, 2},
		{[]string{"inspect", "--delimiter", "ab", "testdata/orders.csv"}, 2},
		{[]string{"check", "--escape", "\\", "testdata/orders.csv"}, 2},
		{[]string{"check", "--encoding", "ebcdic", "testdata/orders.csv"}, 2},
		{[]string{"inspect", "a", "b"}, 2},
		{[]string{"inspect", "testdata/lines.jsonl"}, 0},
		{[]string{"convert", "testdata/lines.jsonl"}, 1},
		{[]string{"convert", "--json", "testdata/orders.csv"}, 2},
		{[]string{"version"}, 0},
		{[]string{"help"}, 0},
	}
	for _, c := range cases {
		if code, _, _ := runCmd(t, "", c.args...); code != c.code {
			t.Errorf("%q: exit status %d, want %d", c.args, code, c.code)
		}
	}
}

func TestJSONErrorDocument(t *testing.T) {
	code, out, stderr := runCmd(t, "", "inspect", "--json", "testdata/missing.csv")
	if code != 2 || stderr == "" {
		t.Fatalf("status %d, stderr %q", code, stderr)
	}
	var doc errorDoc
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil || doc.Schema != "wildcsv/error/v1" || doc.Error.Code != "io" {
		t.Errorf("error document %q: %v", out, err)
	}
}

func TestConvertOutputIsAtomic(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "clean.csv")
	os.WriteFile(target, []byte("previous\n"), 0o644)
	code, _, _ := runCmd(t, "", "convert", "--output", target, "testdata/problems.csv")
	if code != 1 {
		t.Fatalf("status %d, want 1", code)
	}
	if got, _ := os.ReadFile(target); string(got) != "previous\n" {
		t.Errorf("a failed run changed the target: %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("a failed run left files behind: %v", entries)
	}
	code, out, _ := runCmd(t, "", "convert", "--json", "--output", target, "testdata/orders.csv")
	if code != 0 || !strings.Contains(out, `"ok": true`) {
		t.Fatalf("status %d, output %s", code, out)
	}
	if got, _ := os.ReadFile(target); !strings.HasPrefix(string(got), "id,fecha,importe\n") {
		t.Errorf("target = %q", got)
	}
}

func TestWorkbookHint(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("xl/workbook.xml")
	f.Write([]byte("<workbook/>"))
	zw.Close()
	code, out, _ := runCmd(t, buf.String(), "inspect", "--json")
	if code != 0 || !strings.Contains(out, `"kind": "xlsx"`) || !strings.Contains(out, "Export the sheet as CSV") {
		t.Errorf("status %d, output %s", code, out)
	}
}

// keyPaths lists the member paths of a JSON value, with arrays as [].
func keyPaths(v any, prefix string, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			p := prefix + "." + k
			out[p] = true
			keyPaths(val, p, out)
		}
	case []any:
		for _, val := range x {
			keyPaths(val, prefix+"[]", out)
		}
	}
}

// TestDocumentedShapes compares the members of the JSON examples in
// docs/design.md with those of real output, so the two cannot drift apart.
func TestDocumentedShapes(t *testing.T) {
	design, err := os.ReadFile("../../docs/design.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```json\n(.*?)```").FindAllSubmatch(design, -1)
	documented := map[string]map[string]bool{}
	for _, b := range blocks {
		var v map[string]any
		if err := json.Unmarshal(b[1], &v); err != nil {
			t.Fatalf("design.md example does not parse: %v", err)
		}
		paths := map[string]bool{}
		keyPaths(v, "", paths)
		documented[v["schema"].(string)] = paths
	}
	real := map[string][]string{
		"wildcsv/inspect/v1": {"inspect", "--json", "testdata/orders.csv"},
		"wildcsv/check/v1":   {"check", "--json", "testdata/problems.csv"},
	}
	for schema, args := range real {
		want, ok := documented[schema]
		if !ok {
			t.Fatalf("design.md has no example of %s", schema)
		}
		_, out, _ := runCmd(t, "", args...)
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		keyPaths(v, "", got)
		var missing, extra []string
		for p := range want {
			if !got[p] && !strings.HasPrefix(p, ".problem_counts.") {
				missing = append(missing, p)
			}
		}
		for p := range got {
			if !want[p] && !strings.HasPrefix(p, ".problem_counts.") {
				extra = append(extra, p)
			}
		}
		slices.Sort(missing)
		slices.Sort(extra)
		if len(missing)+len(extra) > 0 {
			t.Errorf("%s: documented but not produced %v; produced but not documented %v", schema, missing, extra)
		}
	}
}
