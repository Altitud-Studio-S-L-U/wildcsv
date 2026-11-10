package wildcsv

import (
	"bytes"
	"io"
	"unicode/utf8"
)

// fieldPos is the line and byte column where a field starts: its first
// character, or its opening quote.
type fieldPos struct {
	line, col int
}

// scanner reads records of one dialect from UTF-8 text. It is the only
// parser: the Reader uses it on the decoded stream and detection uses it on
// the sample, so both read a file the same way.
type scanner struct {
	r    io.Reader
	buf  []byte
	pos  int
	end  int
	base int64 // offset of buf[0] in the text
	eof  bool
	rerr error // a read error other than io.EOF

	delim     []byte
	quote     []byte // nil: fields are never quoted
	esc       []byte // nil: no escape character
	skipSpace bool
	strict    bool
	lim       Limits
	warn      func(*ParseError)

	special  [256]bool // bytes that stop the scan of an unquoted field
	qspecial [256]bool // bytes that stop the scan of a quoted field

	// A single-byte delimiter, quote or escape is compared directly.
	delimB, quoteB, escB byte
	delim1, quote1, esc1 bool

	line      int
	lineStart int64
	skipLines int

	// The current record.
	recNo        int
	recStart     int64
	recLine      int
	recLineStart int64
	rec          []byte // field contents, concatenated
	ends         []int  // end of each field in rec
	fpos         []fieldPos
	quoted       []bool

	bare, stray, invalid int64
	lastInvalid          int64
}

func newScanner(r io.Reader, d Dialect, lim Limits, strict bool) *scanner {
	s := &scanner{r: r, buf: make([]byte, 64<<10), lim: lim, strict: strict, line: 1, lastInvalid: -1}
	s.setDialect(d)
	return s
}

// newSliceScanner scans text held in memory without copying it.
func newSliceScanner(text []byte, d Dialect, lim Limits) *scanner {
	s := &scanner{buf: text, end: len(text), eof: true, lim: lim, line: 1, lastInvalid: -1}
	s.setDialect(d)
	return s
}

func (s *scanner) setDialect(d Dialect) {
	s.delim = utf8.AppendRune(nil, d.Delimiter)
	if d.Quote != 0 {
		s.quote = utf8.AppendRune(nil, d.Quote)
		if d.Escape != 0 && d.Escape != d.Quote {
			s.esc = utf8.AppendRune(nil, d.Escape)
		}
	}
	s.skipSpace = d.SkipInitialSpace
	s.delimB, s.delim1 = s.delim[0], len(s.delim) == 1
	if s.quote != nil {
		s.quoteB, s.quote1 = s.quote[0], len(s.quote) == 1
	}
	if s.esc != nil {
		s.escB, s.esc1 = s.esc[0], len(s.esc) == 1
	}
	// Bytes of 0x80 and above do not stop the scan: each scanned stretch is
	// validated as UTF-8 at once, and only an invalid or cut sequence takes
	// the slow path.
	for _, t := range []*[256]bool{&s.special, &s.qspecial} {
		t['\r'] = true
		t['\n'] = true
	}
	s.special[s.delim[0]] = true
	if s.quote != nil {
		s.special[s.quote[0]] = true
		s.qspecial[s.quote[0]] = true
	}
	if s.esc != nil {
		s.qspecial[s.esc[0]] = true
	}
}

func (s *scanner) abs() int64 { return s.base + int64(s.pos) }

// fill reads more input, keeping the unread bytes. It reports whether bytes
// were added.
func (s *scanner) fill() bool {
	if s.eof || s.rerr != nil {
		return false
	}
	if s.pos > 0 {
		n := copy(s.buf, s.buf[s.pos:s.end])
		s.base += int64(s.pos)
		s.end = n
		s.pos = 0
	}
	for range 100 {
		n, err := s.r.Read(s.buf[s.end:])
		s.end += n
		if err != nil {
			if err == io.EOF {
				s.eof = true
			} else {
				s.rerr = err
			}
			return n > 0
		}
		if n > 0 {
			return true
		}
	}
	s.rerr = io.ErrNoProgress
	return false
}

// ensure makes n bytes available if the input has them.
func (s *scanner) ensure(n int) bool {
	for s.end-s.pos < n {
		if !s.fill() {
			return s.end-s.pos >= n
		}
	}
	return true
}

func (s *scanner) matchAt(seq []byte) bool {
	if !s.ensure(len(seq)) {
		return false
	}
	return bytes.HasPrefix(s.buf[s.pos:s.end], seq)
}

func (s *scanner) atDelim() bool {
	if s.delim1 {
		return (s.pos < s.end || s.ensure(1)) && s.buf[s.pos] == s.delimB
	}
	return s.matchAt(s.delim)
}

func (s *scanner) atQuote() bool {
	if s.quote == nil {
		return false
	}
	if s.quote1 {
		return (s.pos < s.end || s.ensure(1)) && s.buf[s.pos] == s.quoteB
	}
	return s.matchAt(s.quote)
}

func (s *scanner) atEscape() bool {
	if s.esc == nil {
		return false
	}
	if s.esc1 {
		return (s.pos < s.end || s.ensure(1)) && s.buf[s.pos] == s.escB
	}
	return s.matchAt(s.esc)
}

// validPrefix returns how much of a scanned stretch is valid UTF-8. A
// sequence cut by the end of the buffer counts as not valid yet, so the slow
// path completes it after a refill.
func validPrefix(b []byte) int {
	if utf8.Valid(b) {
		return len(b)
	}
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size <= 1 {
			return i
		}
		i += size
	}
	return len(b)
}

func (s *scanner) newline() {
	s.line++
	s.lineStart = s.abs()
}

// consumeLineBreak consumes a line feed, a carriage return, or both, at pos.
func (s *scanner) consumeLineBreak() {
	if s.buf[s.pos] == '\r' {
		s.pos++
		if s.ensure(1) && s.buf[s.pos] == '\n' {
			s.pos++
		}
	} else {
		s.pos++
	}
	s.newline()
}

// discardLine skips input up to and including the next line break, without
// interpreting quotes.
func (s *scanner) discardLine() {
	for {
		if s.pos == s.end && !s.ensure(1) {
			return
		}
		i := bytes.IndexAny(s.buf[s.pos:s.end], "\r\n")
		if i < 0 {
			s.pos = s.end
			continue
		}
		s.pos += i
		s.consumeLineBreak()
		return
	}
}

func (s *scanner) endErr() error {
	if s.rerr != nil {
		return s.rerr
	}
	return io.EOF
}

func (s *scanner) parseError(err error, at int64, line int, lineStart int64, quoted bool) *ParseError {
	return &ParseError{Record: s.recNo, Line: line, Column: int(at-lineStart) + 1, Offset: at, Quoted: quoted, Err: err}
}

// fail returns a ParseError after discarding the rest of the line, so that
// the next call continues with the next line.
func (s *scanner) fail(err error, at int64, line int, lineStart int64, quoted bool) error {
	pe := s.parseError(err, at, line, lineStart, quoted)
	s.discardLine()
	return pe
}

// defect handles a quoting defect or invalid UTF-8 at pos: an error when
// strict, otherwise counted and reported to Warn.
func (s *scanner) defect(err error, quoted bool) error {
	at := s.abs()
	if s.strict {
		return s.fail(err, at, s.line, s.lineStart, quoted)
	}
	switch err {
	case ErrBareQuote:
		s.bare++
	case ErrStrayQuote:
		s.stray++
	case ErrInvalidUTF8:
		s.invalid++
	}
	if s.warn != nil {
		s.warn(s.parseError(err, at, s.line, s.lineStart, quoted))
	}
	return nil
}

func (s *scanner) overRecord() bool {
	return s.abs()-s.recStart > int64(s.lim.MaxRecordBytes)
}

// room is how many more bytes the record may take, plus one so that the scan
// can step over the limit and report it.
func (s *scanner) room() int {
	r := int64(s.lim.MaxRecordBytes) - (s.abs() - s.recStart) + 1
	if r < 1 {
		return 1
	}
	if r > int64(len(s.buf)) {
		return len(s.buf)
	}
	return int(r)
}

// check enforces the field and record limits after bytes were consumed.
func (s *scanner) check(fieldStart int, start int64, line int, lineStart int64, quoted bool) error {
	if len(s.rec)-fieldStart > s.lim.MaxFieldBytes {
		return s.fail(ErrFieldTooLarge, start, line, lineStart, quoted)
	}
	if s.overRecord() {
		return s.fail(ErrRecordTooLarge, s.recStart, s.recLine, s.recLineStart, false)
	}
	return nil
}

// utf8Char copies the character at pos, which starts with a byte of 0x80 or
// above, replacing an invalid sequence with U+FFFD. A run of invalid bytes
// becomes one replacement and counts once.
func (s *scanner) utf8Char(quoted bool) error {
	s.ensure(utf8.UTFMax)
	r, size := utf8.DecodeRune(s.buf[s.pos:s.end])
	if r != utf8.RuneError || size > 1 {
		s.rec = append(s.rec, s.buf[s.pos:s.pos+size]...)
		s.pos += size
		return nil
	}
	if s.abs() != s.lastInvalid {
		if err := s.defect(ErrInvalidUTF8, quoted); err != nil {
			return err
		}
		s.rec = append(s.rec, replacement...)
	}
	s.pos++
	s.lastInvalid = s.abs()
	return nil
}

// skipLeadingLines discards the lines Options.SkipLines asks for.
func (s *scanner) skipLeadingLines() {
	for ; s.skipLines > 0; s.skipLines-- {
		if !s.ensure(1) {
			s.skipLines = 0
			return
		}
		s.discardLine()
	}
}

// readRecord reads the next record into rec and ends. It returns nil, io.EOF,
// a read error, or a *ParseError after which it can be called again.
func (s *scanner) readRecord() error {
	if s.skipLines > 0 {
		s.skipLeadingLines()
	}
	for {
		if !s.ensure(1) {
			return s.endErr()
		}
		b := s.buf[s.pos]
		if b != '\n' && b != '\r' {
			break
		}
		s.consumeLineBreak()
	}
	s.recNo++
	s.recStart = s.abs()
	s.recLine = s.line
	s.recLineStart = s.lineStart
	s.rec = s.rec[:0]
	s.ends = s.ends[:0]
	s.fpos = s.fpos[:0]
	s.quoted = s.quoted[:0]
	for {
		if len(s.ends) == s.lim.MaxFields {
			return s.fail(ErrTooManyFields, s.abs(), s.line, s.lineStart, false)
		}
		if s.skipSpace {
			for s.ensure(1) && s.buf[s.pos] == ' ' {
				s.pos++
				if s.overRecord() {
					return s.fail(ErrRecordTooLarge, s.recStart, s.recLine, s.recLineStart, false)
				}
			}
		}
		start, line, lineStart := s.abs(), s.line, s.lineStart
		s.fpos = append(s.fpos, fieldPos{line: line, col: int(start-lineStart) + 1})
		var more bool
		var err error
		if s.atQuote() {
			s.quoted = append(s.quoted, true)
			more, err = s.quotedField(start, line, lineStart)
		} else {
			s.quoted = append(s.quoted, false)
			more, err = s.unquotedField(start, line, lineStart)
		}
		if err != nil {
			return err
		}
		s.ends = append(s.ends, len(s.rec))
		if !more {
			return nil
		}
	}
}

// endField consumes what ends a field at pos: a delimiter, a line break or
// the end of the input. It reports whether another field follows.
func (s *scanner) endField() (bool, error) {
	if s.pos == s.end && !s.ensure(1) {
		if s.rerr != nil {
			return false, s.rerr
		}
		return false, nil
	}
	if b := s.buf[s.pos]; b == '\n' || b == '\r' {
		s.consumeLineBreak()
		return false, nil
	}
	s.pos += len(s.delim)
	if s.overRecord() {
		return false, s.fail(ErrRecordTooLarge, s.recStart, s.recLine, s.recLineStart, false)
	}
	return true, nil
}

func (s *scanner) atFieldEnd() bool {
	if s.pos == s.end && !s.ensure(1) {
		return true
	}
	if b := s.buf[s.pos]; b == '\n' || b == '\r' {
		return true
	}
	return s.atDelim()
}

// scan returns the end of the stretch from pos that holds no byte of table
// and is valid UTF-8, bounded by the record limit.
func (s *scanner) scan(table *[256]bool) int {
	i, lim := s.pos, min(s.end, s.pos+s.room())
	var high byte
	for i < lim {
		c := s.buf[i]
		if table[c] {
			break
		}
		high |= c
		i++
	}
	if high < 0x80 {
		return i
	}
	return s.pos + validPrefix(s.buf[s.pos:i])
}

func (s *scanner) unquotedField(start int64, line int, lineStart int64) (bool, error) {
	fieldStart := len(s.rec)
	for {
		if s.pos == s.end && !s.ensure(1) {
			return s.endField()
		}
		if i := s.scan(&s.special); i > s.pos {
			s.rec = append(s.rec, s.buf[s.pos:i]...)
			s.pos = i
			if err := s.check(fieldStart, start, line, lineStart, false); err != nil {
				return false, err
			}
			if s.pos == s.end {
				continue
			}
		}
		b := s.buf[s.pos]
		switch {
		case s.delim1 && b == s.delimB:
			s.pos++
			if s.overRecord() {
				return false, s.fail(ErrRecordTooLarge, s.recStart, s.recLine, s.recLineStart, false)
			}
			return true, nil
		case b == '\n' || b == '\r' || s.atDelim():
			return s.endField()
		case s.atQuote():
			if err := s.defect(ErrBareQuote, false); err != nil {
				return false, err
			}
			s.rec = append(s.rec, s.quote...)
			s.pos += len(s.quote)
		case b >= 0x80:
			if err := s.utf8Char(false); err != nil {
				return false, err
			}
		default:
			s.rec = append(s.rec, b)
			s.pos++
		}
		if err := s.check(fieldStart, start, line, lineStart, false); err != nil {
			return false, err
		}
	}
}

func (s *scanner) quotedField(start int64, line int, lineStart int64) (bool, error) {
	s.pos += len(s.quote)
	fieldStart := len(s.rec)
	if err := s.check(fieldStart, start, line, lineStart, true); err != nil {
		return false, err
	}
	for {
		if s.pos == s.end && !s.ensure(1) {
			if s.rerr != nil {
				return false, s.rerr
			}
			return false, s.fail(ErrUnterminatedQuote, start, line, lineStart, true)
		}
		if i := s.scan(&s.qspecial); i > s.pos {
			s.rec = append(s.rec, s.buf[s.pos:i]...)
			s.pos = i
			if err := s.check(fieldStart, start, line, lineStart, true); err != nil {
				return false, err
			}
			if s.pos == s.end {
				continue
			}
		}
		b := s.buf[s.pos]
		switch {
		case b == '\n':
			s.rec = append(s.rec, b)
			s.pos++
			s.newline()
		case b == '\r':
			s.rec = append(s.rec, b)
			s.pos++
			if s.ensure(1) && s.buf[s.pos] == '\n' {
				s.rec = append(s.rec, '\n')
				s.pos++
			}
			s.newline()
		case s.atEscape():
			s.pos += len(s.esc)
			switch {
			case s.atQuote():
				s.rec = append(s.rec, s.quote...)
				s.pos += len(s.quote)
			case s.atEscape():
				s.rec = append(s.rec, s.esc...)
				s.pos += len(s.esc)
			default:
				s.rec = append(s.rec, s.esc...)
			}
		case s.atQuote():
			more, closed, err := s.afterQuote()
			if err != nil || closed {
				return more, err
			}
		case b >= 0x80:
			if err := s.utf8Char(true); err != nil {
				return false, err
			}
		default:
			s.rec = append(s.rec, b)
			s.pos++
		}
		if err := s.check(fieldStart, start, line, lineStart, true); err != nil {
			return false, err
		}
	}
}

// afterQuote handles a quote inside a quoted field: a doubled quote, the
// closing quote, or a stray quote that is data.
func (s *scanner) afterQuote() (more, closed bool, err error) {
	qAt, qLine, qLineStart := s.abs(), s.line, s.lineStart
	s.pos += len(s.quote)
	if s.atQuote() {
		s.rec = append(s.rec, s.quote...)
		s.pos += len(s.quote)
		return false, false, nil
	}
	if s.atFieldEnd() {
		more, err = s.endField()
		return more, true, err
	}
	spaces := 0
	if s.delimB != ' ' {
		for s.ensure(1) && s.buf[s.pos] == ' ' {
			s.pos++
			spaces++
			if s.overRecord() {
				return false, true, s.fail(ErrRecordTooLarge, s.recStart, s.recLine, s.recLineStart, false)
			}
		}
		if spaces > 0 && s.atFieldEnd() {
			if s.strict && !s.skipSpace {
				return false, true, s.fail(ErrStrayQuote, qAt, qLine, qLineStart, true)
			}
			more, err = s.endField()
			return more, true, err
		}
	}
	if s.strict {
		return false, true, s.fail(ErrStrayQuote, qAt, qLine, qLineStart, true)
	}
	s.stray++
	if s.warn != nil {
		s.warn(s.parseError(ErrStrayQuote, qAt, qLine, qLineStart, true))
	}
	s.rec = append(s.rec, s.quote...)
	for range spaces {
		s.rec = append(s.rec, ' ')
	}
	return false, false, nil
}
