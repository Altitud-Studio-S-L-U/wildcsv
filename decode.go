package wildcsv

import (
	"encoding/binary"
	"io"
	"sync/atomic"
	"unicode/utf8"
)

// converter turns raw bytes into UTF-8. It returns the extended dst and the
// number of src bytes consumed; unconsumed bytes are offered again with more
// input. With final set it must consume everything.
type converter interface {
	convert(dst, src []byte, final bool) ([]byte, int)
}

// decoder is an io.Reader that applies a converter to a source with fixed
// buffers.
type decoder struct {
	src    io.Reader
	conv   converter
	raw    []byte
	rawN   int
	out    []byte
	outPos int
	err    error
}

func newDecoder(src io.Reader, conv converter) *decoder {
	return &decoder{src: src, conv: conv, raw: make([]byte, 32<<10)}
}

func (d *decoder) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	empty := 0
	for d.outPos >= len(d.out) {
		if d.err != nil {
			return 0, d.err
		}
		n, err := d.src.Read(d.raw[d.rawN:])
		d.rawN += n
		var consumed int
		d.out, consumed = d.conv.convert(d.out[:0], d.raw[:d.rawN], err == io.EOF)
		d.outPos = 0
		copy(d.raw, d.raw[consumed:d.rawN])
		d.rawN -= consumed
		if err != nil {
			d.err = err
		} else if n == 0 {
			if empty++; empty >= 100 {
				d.err = io.ErrNoProgress
			}
		}
	}
	n := copy(p, d.out[d.outPos:])
	d.outPos += n
	return n, nil
}

var replacement = []byte(string(utf8.RuneError))

// invalidUnit stands for an unpaired surrogate or a trailing odd byte in
// UTF-16. It is three bytes that are not UTF-8, as long as the U+FFFD that
// replaces it, so the scanner replaces, counts and reports it where it
// reads it, as it does invalid UTF-8, and positions match the text returned.
var invalidUnit = []byte{0xFF, 0xFF, 0xFF}

// cp1252High maps bytes 0x80 to 0x9F as the WHATWG Encoding Standard does;
// the five bytes Windows-1252 leaves undefined map to the C1 control with the
// same value.
var cp1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

type cp1252Conv struct{}

func (cp1252Conv) convert(dst, src []byte, _ bool) ([]byte, int) {
	for _, c := range src {
		switch {
		case c < 0x80:
			dst = append(dst, c)
		case c < 0xA0:
			dst = utf8.AppendRune(dst, cp1252High[c-0x80])
		default:
			dst = utf8.AppendRune(dst, rune(c))
		}
	}
	return dst, len(src)
}

// utf16Conv decodes UTF-16; unpaired surrogates and a trailing odd byte become
// invalidUnit.
type utf16Conv struct {
	bigEndian bool
}

func (c *utf16Conv) unit(b []byte) rune {
	if c.bigEndian {
		return rune(b[0])<<8 | rune(b[1])
	}
	return rune(b[1])<<8 | rune(b[0])
}

func (c *utf16Conv) convert(dst, src []byte, final bool) ([]byte, int) {
	i := 0
	for i+1 < len(src) {
		u := c.unit(src[i:])
		switch {
		case u >= 0xD800 && u < 0xDC00:
			if i+3 >= len(src) {
				if !final {
					return dst, i
				}
				dst = append(dst, invalidUnit...)
				i += 2
				continue
			}
			u2 := c.unit(src[i+2:])
			if u2 >= 0xDC00 && u2 < 0xE000 {
				dst = utf8.AppendRune(dst, 0x10000+(u-0xD800)<<10+(u2-0xDC00))
				i += 4
			} else {
				dst = append(dst, invalidUnit...)
				i += 2
			}
		case u >= 0xDC00 && u < 0xE000:
			dst = append(dst, invalidUnit...)
			i += 2
		default:
			dst = utf8.AppendRune(dst, u)
			i += 2
		}
	}
	if final && i < len(src) {
		dst = append(dst, invalidUnit...)
		i = len(src)
	}
	return dst, i
}

// lateReader reads text whose sample was ASCII. It passes the text through
// untouched up to the first byte that is not ASCII, and there decides: a valid
// UTF-8 sequence means UTF-8 from there on, still passed through without a
// copy; anything else means Windows-1252 from there on. Everything before that
// byte reads the same in both encodings.
type lateReader struct {
	src       io.Reader
	passed    int64                    // bytes passed through before the decision
	decidedAt atomic.Int64             // offset of the byte the decision was made on
	decided   atomic.Pointer[Encoding] // read by Stats, possibly from another goroutine
	rest      io.Reader                // what to read once decided
}

// encoding returns the encoding decided on, or "", and the offset in the
// text of the byte it was decided on.
func (l *lateReader) encoding() (Encoding, int64) {
	if e := l.decided.Load(); e != nil {
		return *e, l.decidedAt.Load()
	}
	return "", 0
}

func (l *lateReader) Read(p []byte) (int, error) {
	if l.rest != nil {
		return l.rest.Read(p)
	}
	n, err := l.src.Read(p)
	k := asciiPrefix(p[:n])
	if k == n {
		l.passed += int64(n)
		return n, err
	}
	l.decidedAt.Store(l.passed + int64(k))
	// Decide on the sequence at k, reading the rest of it if the read cut it.
	var look [utf8.UTFMax]byte
	m := copy(look[:], p[k:n])
	var extra []byte
	for empty := 0; m < utf8.UTFMax && !utf8.FullRune(look[:m]) && err == nil; {
		var c int
		c, err = l.src.Read(look[m:])
		extra = append(extra, look[m:m+c]...)
		m += c
		if c == 0 && err == nil {
			if empty++; empty >= 100 {
				err = io.ErrNoProgress
			}
		}
	}
	after := l.src
	if err != nil {
		after = errReader{err}
	}
	if r, size := utf8.DecodeRune(look[:m]); r == utf8.RuneError && size <= 1 {
		l.decided.Store(&windows1252)
		pending := append(append([]byte(nil), p[k:n]...), extra...)
		l.rest = newDecoder(io.MultiReader(&sliceReader{b: pending}, after), cp1252Conv{})
		if k > 0 {
			return k, nil
		}
		return l.rest.Read(p)
	}
	l.decided.Store(&utf8Enc)
	l.rest = io.MultiReader(&sliceReader{b: extra}, after)
	return n, nil
}

var windows1252, utf8Enc = Windows1252, UTF8

// asciiPrefix returns the length of the longest prefix of b that is ASCII.
func asciiPrefix(b []byte) int {
	i := 0
	for ; i+8 <= len(b); i += 8 {
		if binary.LittleEndian.Uint64(b[i:])&swarHi != 0 {
			break
		}
	}
	for i < len(b) && b[i] < utf8.RuneSelf {
		i++
	}
	return i
}

// errReader returns err from every Read.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// decodeSample converts a raw sample for detection. A sequence cut by the end
// of an incomplete sample is dropped.
func decodeSample(dec encodingDecision, sample []byte, complete bool, s *settings) ([]byte, error) {
	sample = sample[dec.bomLen:]
	switch dec.enc {
	case CustomDecoding:
		r := s.Decode(&sliceReader{b: sample})
		limit := int64(4*len(sample)) + 4096
		b, err := io.ReadAll(io.LimitReader(r, limit))
		if err != nil {
			return nil, err
		}
		return b, nil
	case UTF16LE, UTF16BE:
		c := &utf16Conv{bigEndian: dec.enc == UTF16BE}
		b, _ := c.convert(nil, sample, complete)
		return b, nil
	case Windows1252:
		b, _ := cp1252Conv{}.convert(nil, sample, true)
		return b, nil
	}
	return sample, nil
}

// sliceReader is a minimal io.Reader over a byte slice.
type sliceReader struct {
	b []byte
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}
