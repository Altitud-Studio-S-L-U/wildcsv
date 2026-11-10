package wildcsv

import (
	"bytes"
	"unicode/utf8"
)

// Encoding names a character encoding. Its value is the name used in reports.
type Encoding string

// Encodings that wildcsv detects and reads.
const (
	UTF8        Encoding = "utf-8"
	UTF16LE     Encoding = "utf-16le"
	UTF16BE     Encoding = "utf-16be"
	Windows1252 Encoding = "windows-1252"

	// CustomDecoding is reported when Options.Decode converts the input.
	CustomDecoding Encoding = "custom"
)

// The basis of an encoding decision, as reported in [Detection].EncodingBasis.
const (
	BasisDeclared     = "declared"      // Options.Encoding
	BasisCustom       = "custom"        // Options.Decode
	BasisBOM          = "bom"           // a byte order mark
	BasisUTF16Pattern = "utf16-pattern" // zero bytes at alternate offsets
	BasisASCII        = "ascii"         // the sample is ASCII; decided at the first byte that is not
	BasisUTF8         = "utf8"          // the sample is valid UTF-8
	BasisFallback     = "fallback"      // nothing else fits: Windows-1252
)

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
	bomUTF32LE = []byte{0xFF, 0xFE, 0x00, 0x00}
	bomUTF32BE = []byte{0x00, 0x00, 0xFE, 0xFF}
)

type encodingDecision struct {
	enc    Encoding
	basis  string
	bomLen int
}

// decideEncoding applies the order of design §5 to the raw sample.
func decideEncoding(sample []byte, complete bool, s *settings) (encodingDecision, error) {
	if s.Decode != nil {
		return encodingDecision{enc: CustomDecoding, basis: BasisCustom}, nil
	}
	if s.encoding != "" {
		d := encodingDecision{enc: s.encoding, basis: BasisDeclared}
		switch {
		case s.encoding == UTF8 && bytes.HasPrefix(sample, bomUTF8):
			d.bomLen = 3
		case s.encoding == UTF16LE && bytes.HasPrefix(sample, bomUTF16LE):
			d.bomLen = 2
		case s.encoding == UTF16BE && bytes.HasPrefix(sample, bomUTF16BE):
			d.bomLen = 2
		}
		return d, nil
	}
	switch {
	case bytes.HasPrefix(sample, bomUTF32LE):
		return encodingDecision{}, &EncodingError{Encoding: "utf-32le"}
	case bytes.HasPrefix(sample, bomUTF32BE):
		return encodingDecision{}, &EncodingError{Encoding: "utf-32be"}
	case bytes.HasPrefix(sample, bomUTF8):
		return encodingDecision{enc: UTF8, basis: BasisBOM, bomLen: 3}, nil
	case bytes.HasPrefix(sample, bomUTF16LE):
		return encodingDecision{enc: UTF16LE, basis: BasisBOM, bomLen: 2}, nil
	case bytes.HasPrefix(sample, bomUTF16BE):
		return encodingDecision{enc: UTF16BE, basis: BasisBOM, bomLen: 2}, nil
	}
	if enc, ok := utf16Pattern(sample); ok {
		return encodingDecision{enc: enc, basis: BasisUTF16Pattern}, nil
	}
	if isASCII(sample) {
		return encodingDecision{enc: UTF8, basis: BasisASCII}, nil
	}
	if validUTF8Prefix(sample, complete) {
		return encodingDecision{enc: UTF8, basis: BasisUTF8}, nil
	}
	return encodingDecision{enc: Windows1252, basis: BasisFallback}, nil
}

// utf16Pattern recognises UTF-16 without a byte order mark by the zero bytes
// that Latin text leaves at every other offset.
func utf16Pattern(sample []byte) (Encoding, bool) {
	n := min(len(sample), 4096) &^ 1
	if n < 4 {
		return "", false
	}
	var evenZero, oddZero int
	for i := 0; i < n; i += 2 {
		if sample[i] == 0 {
			evenZero++
		}
		if sample[i+1] == 0 {
			oddZero++
		}
	}
	half := n / 2
	high := func(c int) bool { return c*100 >= half*40 }
	low := func(c int) bool { return c*100 <= half*5 }
	switch {
	case high(oddZero) && low(evenZero):
		return UTF16LE, true
	case high(evenZero) && low(oddZero):
		return UTF16BE, true
	}
	return "", false
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}

// validUTF8Prefix reports whether b is valid UTF-8, ignoring a sequence cut by
// the end of an incomplete sample.
func validUTF8Prefix(b []byte, complete bool) bool {
	if !complete {
		// Drop at most UTFMax-1 trailing bytes that could start a cut sequence.
		for i := 1; i < utf8.UTFMax && i <= len(b); i++ {
			c := b[len(b)-i]
			if c < 0x80 {
				break
			}
			if utf8.RuneStart(c) {
				if !utf8.FullRune(b[len(b)-i:]) {
					b = b[:len(b)-i]
				}
				break
			}
		}
	}
	return utf8.Valid(b)
}
