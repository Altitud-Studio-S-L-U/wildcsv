package wildcsv

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"unicode/utf8"
)

// The fast path reads a record that lies wholly in the buffer and has no
// defect: no bare or stray quote, no invalid UTF-8, no padding after a closing
// quote, no carriage return inside a quoted field, and nothing near a limit.
// It scans eight bytes at a time and finds the end of long quoted fields with
// bytes.IndexByte. Anything else makes it give up having changed nothing, and
// the general path reads the record from its start; that path defines every
// result, and FuzzFastPath checks that both give the same ones.

const (
	swarLo = 0x0101010101010101
	swarHi = 0x8080808080808080
	swar7F = 0x7f7f7f7f7f7f7f7f

	repLF = '\n' * swarLo
	repCR = '\r' * swarLo
)

// eqMask sets the high bit of each byte of x that equals the byte repeated in
// rep, and no other bit. No carry crosses from one byte to the next, so every
// set bit is a match.
func eqMask(x, rep uint64) uint64 {
	y := x ^ rep
	return ^(((y & swar7F) + swar7F) | y | swar7F)
}

// firstMask sets the high bit of the first byte of x that equals the byte
// repeated in rep, if any. Bits above it may be set wrongly, by the borrow from
// that byte, so only the lowest set bit of the result, or of an OR of such
// results, is meaningful.
func firstMask(x, rep uint64) uint64 {
	y := x ^ rep
	return (y - swarLo) &^ y & swarHi
}

// setFast decides whether the dialect can use the fast path.
func (s *scanner) setFast() {
	s.fast = s.delim1 && s.esc == nil && (s.quote == nil || s.quote1) && !(s.skipSpace && s.delimB == ' ')
	s.repDelim = uint64(s.delimB) * swarLo
	if s.quote1 {
		s.repQuote = uint64(s.quoteB) * swarLo
	} else {
		s.repQuote = repLF // matches nothing that does not already stop the scan
	}
}

// quotedStop returns the index of the first quote, line feed or carriage
// return in buf from j, or len(buf), and the bytes it looked at ORed
// together. Short stretches are scanned eight bytes at a time; past 32 bytes
// the quote is found with bytes.IndexByte, only the stretch before it is
// searched for line breaks, and the stretch counts as not ASCII.
func (s *scanner) quotedStop(buf []byte, j int) (int, uint64) {
	var seen uint64
	i, lim := j, min(len(buf), j+32)
	for ; i+8 <= lim; i += 8 {
		x := binary.LittleEndian.Uint64(buf[i:])
		seen |= x
		if m := firstMask(x, s.repQuote) | firstMask(x, repLF) | firstMask(x, repCR); m != 0 {
			return i + bits.TrailingZeros64(m)>>3, seen
		}
	}
	if lim < len(buf) {
		q := len(buf)
		if k := bytes.IndexByte(buf[i:], s.quoteB); k >= 0 {
			q = i + k
		}
		span := buf[i:q]
		if k := bytes.IndexByte(span, '\n'); k >= 0 {
			q = i + k
			span = span[:k]
		}
		if k := bytes.IndexByte(span, '\r'); k >= 0 {
			q = i + k
		}
		return q, swarHi
	}
	for ; i < len(buf); i++ {
		c := buf[i]
		if c == s.quoteB || c == '\n' || c == '\r' {
			return i, seen
		}
		seen |= uint64(c)
	}
	return len(buf), seen
}

// Results of fastRecord.
const (
	fastDone = iota // the record was read
	fastCut         // the record may go on past the end of the buffer
	fastNo          // the general path must read the record
)

// fastRecord reads one record by the fast path. Unless it returns fastDone it
// has changed nothing but the field slice, which the general path resets.
//
// Unquoted fields are read a run at a time: each eight-byte word is loaded
// once, every delimiter in it ends a field, and the first quote or line break
// ends the run.
func (s *scanner) fastRecord() int {
	buf := s.buf[:s.end]
	n := len(buf)
	start := s.pos
	base := s.base
	line, lineStart := s.line, s.lineStart
	colBase := int(base-lineStart) + 1 // the column of buf[i] is colBase+i on this line
	delimB, quoteB, quote1, skip := s.delimB, s.quoteB, s.quote1, s.skipSpace
	repDelim, repQuote := s.repDelim, s.repQuote
	maxFields, maxField, maxRec := s.lim.MaxFields, s.lim.MaxFieldBytes, s.lim.MaxRecordBytes
	fields := s.fields[:0]
	var seen uint64 // the bytes looked at, ORed together
	doubled := false
	i := start // where the next field starts
	end := -1  // where the record's text ends, at a line break or the end of the input
outer:
	for {
		if skip {
			for i < n && buf[i] == ' ' {
				i++
			}
		}
		if len(fields) == maxFields {
			return fastNo
		}
		if quote1 && i < n && buf[i] == quoteB {
			f := field{line: line, col: colBase + i, quoted: true}
			j := i + 1
			var q int
			for {
				var m uint64
				q, m = s.quotedStop(buf, j)
				seen |= m
				if q == n {
					return s.cut()
				}
				switch buf[q] {
				case '\n':
					line++
					lineStart = base + int64(q+1)
					colBase = int(base-lineStart) + 1
					j = q + 1
					continue
				case '\r':
					return fastNo
				}
				if q+1 < n && buf[q+1] == quoteB {
					doubled = true
					j = q + 2
					continue
				}
				break
			}
			if q-(i+1) > maxField {
				return fastNo
			}
			f.start, f.end = i+1-start, q-start
			fields = append(fields, f)
			after := q + 1
			if after+1-start > maxRec {
				return fastNo
			}
			if after == n {
				if !s.eof {
					return fastCut
				}
				end = n
				break
			}
			switch buf[after] {
			case delimB:
				i = after + 1
				continue
			case '\n', '\r':
				end = after
				break outer
			}
			return fastNo
		}

		// A run of unquoted fields from i.
		fs := i
		w := i
		stopAt := n
	scan:
		for {
			if w+8 > n {
				for ; w < n; w++ {
					c := buf[w]
					if c == delimB {
						if len(fields) == maxFields || w-fs > maxField || w+1-start > maxRec {
							return fastNo
						}
						fields = append(fields, field{start: fs - start, end: w - start, line: line, col: colBase + fs})
						fs = w + 1
						if skip && fs < n && buf[fs] == ' ' {
							i = fs
							continue outer
						}
						continue
					}
					if c == '\n' || c == '\r' || (quote1 && c == quoteB) {
						stopAt = w
						break scan
					}
					seen |= uint64(c)
				}
				break
			}
			x := binary.LittleEndian.Uint64(buf[w:])
			seen |= x
			md := eqMask(x, repDelim)
			ms := firstMask(x, repQuote) | firstMask(x, repLF) | firstMask(x, repCR)
			if ms != 0 {
				ms &= -ms // the first stop, which is exact
				md &= ms - 1
			}
			for md != 0 {
				p := w + bits.TrailingZeros64(md)>>3
				md &= md - 1
				if len(fields) == maxFields || p-fs > maxField || p+1-start > maxRec {
					return fastNo
				}
				fields = append(fields, field{start: fs - start, end: p - start, line: line, col: colBase + fs})
				fs = p + 1
				if skip && fs < n && buf[fs] == ' ' {
					i = fs
					continue outer
				}
			}
			if ms != 0 {
				stopAt = w + bits.TrailingZeros64(ms)>>3
				break
			}
			w += 8
		}
		if stopAt < n && quote1 && buf[stopAt] == quoteB {
			if stopAt != fs {
				return fastNo // a bare quote
			}
			i = fs
			continue
		}
		if len(fields) == maxFields || stopAt-fs > maxField || stopAt+1-start > maxRec {
			return fastNo
		}
		if stopAt == n && !s.eof {
			return fastCut
		}
		fields = append(fields, field{start: fs - start, end: stopAt - start, line: line, col: colBase + fs})
		end = stopAt
		break
	}
	next := end
	if end < n {
		next++
		if buf[end] == '\r' {
			if next == n && !s.eof {
				return fastCut
			}
			if next < n && buf[next] == '\n' {
				next++
			}
		}
		line++
		lineStart = base + int64(next)
	}
	s.fields = fields
	text := buf[start:end]
	if seen&swarHi != 0 && !utf8.Valid(text) {
		return fastNo
	}
	s.recQuoted = fields[0].quoted && fields[0].start == 1
	s.out, s.doubled, s.outAt = text, doubled, start
	s.recNo++
	s.recStart = base + int64(start)
	s.recLine, s.recLineStart = s.line, s.lineStart
	s.pos = next
	s.line, s.lineStart = line, lineStart
	return fastDone
}

// cut is the result when the buffer ends before the record can.
func (s *scanner) cut() int {
	if s.eof {
		return fastNo
	}
	return fastCut
}

// undouble copies the fields into rec with each doubled quote of a quoted
// field made single, and points out and the fields at the copy.
func (s *scanner) undouble() {
	s.rec = s.rec[:0]
	for i := range s.fields {
		f := &s.fields[i]
		content := s.out[f.start:f.end]
		f.start = len(s.rec)
		// In a quoted field read by the fast path every quote is the first
		// of a pair.
		for f.quoted {
			k := bytes.IndexByte(content, s.quoteB)
			if k < 0 {
				break
			}
			s.rec = append(s.rec, content[:k+1]...)
			content = content[k+2:]
		}
		s.rec = append(s.rec, content...)
		f.end = len(s.rec)
	}
	s.out, s.doubled, s.outAt = s.rec, false, -1
}

// collapse makes each doubled quote in the quoted fields of the current
// record single. When the buffer is the scanner's own it works in place: the
// record has been consumed and nothing reads those bytes again. A scanner over
// a caller's slice copies into rec instead.
func (s *scanner) collapse() {
	if !s.doubled {
		return
	}
	if !s.ownBuf {
		s.undouble()
		return
	}
	s.doubled = false
	out := s.out
	for i := range s.fields {
		f := &s.fields[i]
		if !f.quoted {
			continue
		}
		k := bytes.IndexByte(out[f.start:f.end], s.quoteB)
		if k < 0 {
			continue
		}
		w := f.start + k + 1 // keep the first quote of the pair
		r := w + 1
		for {
			k = bytes.IndexByte(out[r:f.end], s.quoteB)
			if k < 0 {
				w += copy(out[w:], out[r:f.end])
				break
			}
			w += copy(out[w:], out[r:r+k+1])
			r += k + 2
		}
		f.end = w
	}
}

// recordString returns one string holding every field of the current record,
// which the fields then index.
func (s *scanner) recordString() string {
	s.collapse()
	return string(s.out)
}
