package wildcsv

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Invisible and control characters in sources and documents change meaning
// without showing in a review, so the repository rejects them by code point.
func TestSourceHygiene(t *testing.T) {
	checked := map[string]bool{".go": true, ".md": true, ".yml": true, ".yaml": true, ".json": true, ".rb": true}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "testdata" || name == "dist" || (strings.HasPrefix(name, ".") && name != "." && name != ".github") {
				return filepath.SkipDir
			}
			return nil
		}
		if !checked[filepath.Ext(path)] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checkHygiene(t, path, data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func checkHygiene(t *testing.T, path string, data []byte) {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Bytes()
		if !utf8.Valid(text) {
			t.Errorf("%s:%d: invalid UTF-8", path, line)
			continue
		}
		for _, r := range string(text) {
			if forbiddenRune(r) {
				t.Errorf("%s:%d: forbidden character U+%04X", path, line, r)
			}
		}
	}
	if err := sc.Err(); err != nil {
		t.Errorf("%s: %v", path, err)
	}
}

func forbiddenRune(r rune) bool {
	switch {
	case r == '\t':
		return false
	case r < 0x20 || r == 0x7F:
		return true // includes the carriage return
	case r == 0xFEFF || r == utf8.RuneError:
		return true
	case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x2064:
		return true
	}
	return false
}
