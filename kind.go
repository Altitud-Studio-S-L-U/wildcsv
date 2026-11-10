package wildcsv

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// Kind says what an input looks like. Only delimited and empty inputs can be
// read.
type Kind string

// Kinds that [Detect] recognises.
const (
	KindEmpty     Kind = "empty"
	KindDelimited Kind = "delimited"
	KindXLSX      Kind = "xlsx"
	KindODS       Kind = "ods"
	KindZIP       Kind = "zip"
	KindXLS       Kind = "xls"
	KindGzip      Kind = "gzip"
	KindBzip2     Kind = "bzip2"
	KindXZ        Kind = "xz"
	KindZstd      Kind = "zstd"
	KindPDF       Kind = "pdf"
	KindParquet   Kind = "parquet"
	KindBinary    Kind = "binary"
	KindHTML      Kind = "html"
	KindXML       Kind = "xml"
	KindJSONL     Kind = "jsonl"
	KindJSON      Kind = "json"
)

var (
	zipLocal = []byte("PK\x03\x04")
	odsType  = []byte("application/vnd.oasis.opendocument.spreadsheet")
)

// rawKind recognises formats by their signature in the raw bytes. It returns
// "" when none matches.
func rawKind(b []byte) Kind {
	switch {
	case len(b) == 0:
		return KindEmpty
	case bytes.HasPrefix(b, zipLocal):
		if isODS(b) {
			return KindODS
		}
		if zipHasXL(b) {
			return KindXLSX
		}
		return KindZIP
	case bytes.HasPrefix(b, []byte("PK\x05\x06")), bytes.HasPrefix(b, []byte("PK\x07\x08")):
		return KindZIP
	case bytes.HasPrefix(b, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}):
		return KindXLS
	case bytes.HasPrefix(b, []byte{0x1F, 0x8B}):
		return KindGzip
	case len(b) >= 4 && bytes.HasPrefix(b, []byte("BZh")) && b[3] >= '1' && b[3] <= '9':
		return KindBzip2
	case bytes.HasPrefix(b, []byte{0xFD, '7', 'z', 'X', 'Z', 0x00}):
		return KindXZ
	case bytes.HasPrefix(b, []byte{0x28, 0xB5, 0x2F, 0xFD}):
		return KindZstd
	case bytes.HasPrefix(b, []byte("%PDF-")):
		return KindPDF
	case bytes.HasPrefix(b, []byte("PAR1")):
		return KindParquet
	}
	return ""
}

// zipEntryName returns the name of the local file header at b[0:].
func zipEntryName(b []byte) ([]byte, int, bool) {
	if len(b) < 30 || !bytes.HasPrefix(b, zipLocal) {
		return nil, 0, false
	}
	nameLen := int(binary.LittleEndian.Uint16(b[26:]))
	extraLen := int(binary.LittleEndian.Uint16(b[28:]))
	if len(b) < 30+nameLen {
		return nil, 0, false
	}
	return b[30 : 30+nameLen], 30 + nameLen + extraLen, true
}

func isODS(b []byte) bool {
	name, data, ok := zipEntryName(b)
	if !ok || string(name) != "mimetype" || data > len(b) {
		return false
	}
	return bytes.HasPrefix(b[data:], odsType)
}

func zipHasXL(b []byte) bool {
	for i := 0; ; {
		j := bytes.Index(b[i:], zipLocal)
		if j < 0 {
			return false
		}
		i += j
		if name, _, ok := zipEntryName(b[i:]); ok && bytes.HasPrefix(name, []byte("xl/")) {
			return true
		}
		i += len(zipLocal)
	}
}

// textKind recognises formats in decoded text. It returns KindDelimited when
// none matches.
func textKind(text []byte, complete bool) Kind {
	if len(text) == 0 {
		return KindEmpty
	}
	if isBinaryText(text) {
		return KindBinary
	}
	t := bytes.TrimLeft(text, " \t\r\n\f")
	if len(t) == 0 {
		return KindDelimited
	}
	switch t[0] {
	case '<':
		if isHTML(t) {
			return KindHTML
		}
		if isXML(t) {
			return KindXML
		}
	case '{', '[':
		if isJSONL(t, complete) {
			return KindJSONL
		}
		if isJSON(t, complete) {
			return KindJSON
		}
	}
	return KindDelimited
}

// isBinaryText reports more than 1% of control characters. A NUL counts like
// any other control: legacy exports pad fields with a few of them, while a
// binary format has far more, or a signature rawKind recognises.
func isBinaryText(text []byte) bool {
	controls := 0
	for _, c := range text {
		if c < 0x20 && c != '\t' && c != '\n' && c != '\r' && c != '\f' && c != 0x1A {
			controls++
		}
	}
	return controls > 0 && controls*100 > utf8.RuneCount(text)
}

func isHTML(t []byte) bool {
	lower := bytes.ToLower(t[:min(len(t), 16)])
	for _, p := range []string{"<!doctype html", "<html", "<head", "<body", "<table"} {
		if bytes.HasPrefix(lower, []byte(p)) {
			return true
		}
	}
	return false
}

func isXML(t []byte) bool {
	if bytes.HasPrefix(t, []byte("<?xml")) {
		return true
	}
	if len(t) < 3 || !isASCIILetter(t[1]) {
		return false
	}
	for _, c := range t[2:] {
		switch {
		case isASCIILetter(c) || c >= '0' && c <= '9' || c == '-' || c == '_' || c == ':' || c == '.':
			continue
		case c == ' ' || c == '/' || c == '>' || c == '\t' || c == '\n' || c == '\r':
			return true
		default:
			return false
		}
	}
	return false
}

func isASCIILetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

func isJSONL(t []byte, complete bool) bool {
	lines := bytes.Split(t, []byte("\n"))
	if !complete && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	n := 0
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if (line[0] != '{' && line[0] != '[') || !json.Valid(line) {
			return false
		}
		n++
	}
	return n >= 2
}

func isJSON(t []byte, complete bool) bool {
	if json.Valid(t) {
		return true
	}
	if complete {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(t))
	for {
		_, err := dec.Token()
		if err == nil {
			continue
		}
		return err == io.EOF || err == io.ErrUnexpectedEOF
	}
}
