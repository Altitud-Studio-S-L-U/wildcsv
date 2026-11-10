package wildcsv

import (
	"io"
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
// U+FFFD and are counted.
type utf16Conv struct {
	bigEndian bool
	invalid   int64
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
				dst = append(dst, replacement...)
				c.invalid++
				i += 2
				continue
			}
			u2 := c.unit(src[i+2:])
			if u2 >= 0xDC00 && u2 < 0xE000 {
				dst = utf8.AppendRune(dst, 0x10000+(u-0xD800)<<10+(u2-0xDC00))
				i += 4
			} else {
				dst = append(dst, replacement...)
				c.invalid++
				i += 2
			}
		case u >= 0xDC00 && u < 0xE000:
			dst = append(dst, replacement...)
			c.invalid++
			i += 2
		default:
			dst = utf8.AppendRune(dst, u)
			i += 2
		}
	}
	if final && i < len(src) {
		dst = append(dst, replacement...)
		c.invalid++
		i = len(src)
	}
	return dst, i
}

// lateConv reads text whose sample was ASCII. At the first byte that is not
// ASCII it decides: a valid UTF-8 sequence means UTF-8 from there on,
// anything else means Windows-1252 from there on. Everything before that byte
// reads the same in both encodings.
type lateConv struct {
	decided Encoding
}

func (c *lateConv) convert(dst, src []byte, final bool) ([]byte, int) {
	switch c.decided {
	case UTF8:
		return append(dst, src...), len(src)
	case Windows1252:
		return cp1252Conv{}.convert(dst, src, final)
	}
	i := 0
	for i < len(src) && src[i] < 0x80 {
		i++
	}
	dst = append(dst, src[:i]...)
	if i == len(src) {
		return dst, i
	}
	rest := src[i:]
	if !utf8.FullRune(rest) && !final {
		return dst, i
	}
	if r, size := utf8.DecodeRune(rest); r == utf8.RuneError && size <= 1 {
		c.decided = Windows1252
	} else {
		c.decided = UTF8
	}
	dst, n := c.convert(dst, rest, final)
	return dst, i + n
}

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
