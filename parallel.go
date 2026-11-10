package wildcsv

import (
	"bytes"
	"errors"
	"io"
	"sync"
)

// A parallel Reader cuts the text into chunks, reads the chunks in worker
// goroutines with the same scanner as a sequential Reader, and returns their
// records in order.
//
// A chunk ends just after a line feed where the quotes before it balance, so
// the line feed most likely ends a record. That is a guess: a stray quote, an
// escaped quote or a line feed inside a quoted field can make it wrong. Each
// worker therefore reports whether its chunk ended between two records. When
// one did not, the next chunk did not start at a record either, and its
// results are set aside: a scanner reads on in the calling goroutine from the
// last record the worker completed, across as many chunks as it takes, until
// a record ends exactly where a later chunk starts. From there that chunk's
// results hold. Records, errors, warnings, counts, positions and offsets are
// therefore those of a sequential Reader; FuzzParallel checks it.
//
// Workers build each record as Read or ReadSlices will return it, so the
// goroutine that calls them does a constant amount of work per record and
// writes no pointer: it only remembers which record of which chunk it
// returned last.

// chunkBytes is the size the producer aims for. Tests make it small.
var chunkBytes = 256 << 10

// errChunkEnd is what a worker's scanner meets at the end of a chunk that
// does not end the input. The scanner treats it as a read error, so it stops
// wherever it is and says whether that was between two records.
var errChunkEnd = errors.New("wildcsv: end of chunk")

// ErrClosed is returned by the read methods of a Reader after Close.
var ErrClosed = errors.New("wildcsv: Reader closed")

type chunk struct {
	buf    []byte // the whole buffer text was cut from, kept for reuse
	text   []byte
	base   int64 // offset of text[0] in the text
	line   int   // the first chunk only: the line it starts on
	last   bool  // the chunk ends the input
	rerr   error // with last: the read error that ended the input, nil at its end
	forced bool  // cut where no line feed fitted; it never ends cleanly

	done chan struct{} // closed when the results below are complete

	recs    []chunkRec
	strs    []string     // the fields as strings, when the workers make strings and records may share
	owns    [][]string   // each record's own strings, when they may not share; nil for an error
	views   [][]byte     // the fields as views of text or arena, when the workers make no strings
	errs    []ParseError // positions relative to the chunk
	defects []ParseError
	arena   []byte    // records the general path copied, and records with doubled quotes
	clean   bool      // the chunk ended between two records
	end     scanState // after the last complete record or error
	final   scanState // at the end of the chunk, when it is clean
	fatal   error     // the read error that ended the input in this chunk
}

// chunkRec is one record, or one error, of a chunk. Its fields are
// strs[first:last] or views[first:last]. Where they start in the text is
// not kept: FieldPos reads the record again from start, which is a line
// start, on line line of the chunk.
type chunkRec struct {
	first, last int32
	defects     int32 // defects[:defects] come before it
	end         int32 // the offset after it, relative to the chunk
	err         int32 // errs[err-1] when it is an error
	start, line int32
}

// scanState is where a scanner stands, relative to the start of a chunk.
type scanState struct {
	off       int64
	line      int
	lineStart int64
	recNo     int
}

type parallel struct {
	src     io.Reader
	dialect Dialect
	lim     Limits
	strict  bool
	warn    func(*ParseError)
	skip    int
	workers int
	size    int  // chunkBytes when the Reader was made
	reuse   bool // records may share one array of strings

	// Where the text handed over starts, when a sequential Reader hands over
	// in the middle of the input.
	startBase  int64
	startLine  int
	startRecNo int

	started bool
	strs    bool // workers make strings
	jobs    chan *chunk
	order   chan *chunk
	free    chan *chunk // chunks the consumer is done with
	quit    chan struct{}
	once    sync.Once

	// The consumer, in the goroutine that calls Read.
	begun     bool
	ahead     []*chunk // taken from order and not finished; ahead[0] is current
	ready     bool     // ahead[0] has been read by its worker
	idx, didx int      // next record and next defect of ahead[0]
	baseLine  int      // the line ahead[0] starts on
	baseRecNo int      // records attempted before ahead[0]
	fb        *scanner // reads past a chunk that did not end cleanly
	fbPos     struct {
		chunk int // index in ahead of the chunk fb's reader is in
		at    int // offset in that chunk's text
	}
	fbCounts             [3]int64 // fb's counts already added
	bare, stray, invalid int64
	offset               int64
	done                 error

	// The record last returned: record lastRec of lastChunk, or, when
	// lastChunk is nil, the one the fallback read, in fbStr or fbBytes and
	// ownFields. A chunk the consumer has left is reused only once it is no
	// longer lastChunk.
	lastChunk    *chunk
	lastRec      int
	lastLineBase int
	lastLeft     bool   // the consumer has left lastChunk
	posChunk     *chunk // the record whose fields are in pos
	posRec       int
	pos          []field
	fbStr        string
	fbBytes      []byte
	fbIsStr      bool
	ownFields    []field
}

func newParallel(src io.Reader, d Dialect, s *settings, workers int) *parallel {
	return &parallel{
		src: src, dialect: d, lim: s.lim, strict: s.Strict, warn: s.Warn,
		skip: s.SkipLines, workers: workers, reuse: s.ReuseRecord, size: chunkBytes, startLine: 1,
		quit: make(chan struct{}),
	}
}

// start launches the goroutines; strs says whether the first call wants
// strings.
func (p *parallel) start(strs bool) {
	p.started, p.strs = true, strs
	p.jobs = make(chan *chunk, p.workers)
	p.order = make(chan *chunk, p.workers+1)
	p.free = make(chan *chunk, 2*p.workers+4)
	go p.produce()
	for range p.workers {
		go func() {
			for c := range p.jobs {
				p.parse(c)
			}
		}()
	}
}

// close stops the goroutines. A producer blocked in a Read of the source
// stops when that Read returns.
func (p *parallel) close() {
	p.once.Do(func() { close(p.quit) })
}

// getChunk returns an empty chunk, reusing one the consumer is done with
// when it can.
func (p *parallel) getChunk() *chunk {
	var c *chunk
	select {
	case c = <-p.free:
		*c = chunk{
			buf: c.buf[:0], recs: c.recs[:0], strs: c.strs[:0], owns: c.owns[:0],
			views: c.views[:0], errs: c.errs[:0], defects: c.defects[:0], arena: c.arena[:0],
		}
	default:
		c = &chunk{}
	}
	if cap(c.buf) < 2*p.size {
		c.buf = make([]byte, 0, 2*p.size)
	}
	c.done = make(chan struct{})
	return c
}

// release offers a chunk for reuse.
func (p *parallel) release(c *chunk) {
	select {
	case p.free <- c:
	default:
	}
}

// send hands a chunk to the consumer and to a worker.
func (p *parallel) send(c *chunk) bool {
	select {
	case p.order <- c:
	case <-p.quit:
		return false
	}
	select {
	case p.jobs <- c:
		return true
	case <-p.quit:
		close(c.done)
		return false
	}
}

// produce reads the text and cuts it into chunks.
func (p *parallel) produce() {
	defer close(p.jobs)
	defer close(p.order)
	cur := p.getChunk()
	buf := cur.buf
	base := p.startBase
	var rerr error
	ended := false
	// fill reads until buf holds at least n bytes or the input ends.
	fill := func(n int) {
		for len(buf) < n && !ended {
			if cap(buf)-len(buf) < 4096 {
				nb := make([]byte, len(buf), max(2*cap(buf), n+p.size))
				copy(nb, buf)
				buf = nb
			}
			for empty := 0; ; empty++ {
				m, err := p.src.Read(buf[len(buf):cap(buf)])
				buf = buf[:len(buf)+m]
				if err != nil {
					ended = true
					if err != io.EOF {
						rerr = err
					}
					break
				}
				if m > 0 {
					break
				}
				if empty >= 100 {
					ended, rerr = true, io.ErrNoProgress
					break
				}
			}
		}
	}
	// Lines to skip are discarded here, as a sequential Reader discards
	// them: up to and including a line feed, a carriage return, or both.
	line := p.startLine
	for skip := p.skip; skip > 0; {
		fill(1)
		if len(buf) == 0 {
			break
		}
		i := bytes.IndexAny(buf, "\r\n")
		if i < 0 {
			base += int64(len(buf))
			buf = buf[:0]
			continue
		}
		if buf[i] == '\r' && i+1 == len(buf) && !ended {
			fill(len(buf) + 1)
			continue
		}
		n := i + 1
		if buf[i] == '\r' && n < len(buf) && buf[n] == '\n' {
			n++
		}
		base += int64(n)
		buf = buf[n:]
		line++
		skip--
	}
	odd := false // an odd number of quotes before buf
	for first := true; ; first = false {
		fill(p.size)
		c := cur
		c.base = base
		if first {
			c.line = line
		}
		if ended {
			c.buf, c.text, c.last, c.rerr = buf, buf, true, rerr
			p.send(c)
			return
		}
		cut, oddAtEnd := p.cut(buf, odd)
		for cut < 0 && !ended && len(buf) < 4*p.size {
			fill(len(buf) + p.size)
			cut, oddAtEnd = p.cut(buf, odd)
		}
		if ended && cut < 0 {
			c.buf, c.text, c.last, c.rerr = buf, buf, true, rerr
			p.send(c)
			return
		}
		// A chunk is cut where the quotes balance, so the next one starts
		// with none open; a forced cut keeps the count.
		odd = false
		if cut < 0 {
			cut, c.forced, odd = len(buf), true, oddAtEnd
		}
		c.buf, c.text = buf, buf[:cut]
		cur = p.getChunk()
		buf = append(cur.buf, buf[cut:]...)
		base += int64(cut)
		if !p.send(c) {
			return
		}
	}
}

// cut returns where to end a chunk taken from buf: just after the last line
// feed at which the quotes balance, counting from a start where they balance
// unless odd; or -1 when there is none. It also returns whether the quotes are
// odd at the end of buf, which a forced cut carries on.
func (p *parallel) cut(buf []byte, odd bool) (int, bool) {
	q := p.dialect.Quote
	end := bytes.LastIndexByte(buf, '\n')
	if q == 0 {
		if end < 0 {
			return -1, false
		}
		return end + 1, false
	}
	oddAtEnd := odd != (countQuotes(buf, q)%2 == 1)
	if end < 0 {
		return -1, oddAtEnd
	}
	at := oddAtEnd != (countQuotes(buf[end+1:], q)%2 == 1) // odd just after end
	for {
		if !at {
			return end + 1, oddAtEnd
		}
		prev := bytes.LastIndexByte(buf[:end], '\n')
		if prev < 0 {
			return -1, oddAtEnd
		}
		at = at != (countQuotes(buf[prev+1:end+1], q)%2 == 1)
		end = prev
	}
}

func countQuotes(b []byte, q rune) int {
	if q < 0x80 {
		return bytes.Count(b, []byte{byte(q)})
	}
	return bytes.Count(b, []byte(string(q)))
}

// newlineByte is a line feed, for bytes.Count.
var newlineByte = []byte{'\n'}

// parse reads one chunk.
func (p *parallel) parse(c *chunk) {
	defer close(c.done)
	select {
	case <-p.quit:
		return
	default:
	}
	sc := &scanner{buf: c.text, end: len(c.text), lim: p.lim, strict: p.strict, line: 1, lastInvalid: -1}
	sc.setDialect(p.dialect)
	switch {
	case !c.last:
		sc.rerr = errChunkEnd
	case c.rerr != nil:
		sc.rerr = c.rerr
	default:
		sc.eof = true
	}
	sc.warn = func(pe *ParseError) { c.defects = append(c.defects, *pe) }
	c.end = scanState{line: 1}
	lines := bytes.Count(c.text, newlineByte) + 1
	if cap(c.recs) < lines {
		c.recs = make([]chunkRec, 0, lines)
	}
	for {
		sc.hitEnd = false
		err := sc.readRecord()
		pe, isPE := errors.AsType[*ParseError](err)
		if err != nil && !isPE {
			switch {
			case err == errChunkEnd:
				c.clean = sc.between && !c.forced
				c.final = scanState{sc.abs(), sc.line, sc.lineStart, sc.recNo}
			case err != io.EOF:
				c.fatal = err
			}
			return
		}
		if sc.hitEnd {
			// The record or error needed bytes past the chunk, so it may
			// read differently in the whole text: the fallback reads it.
			return
		}
		rec := chunkRec{defects: int32(len(c.defects)), start: int32(sc.recStart), line: int32(sc.recLine)}
		if isPE {
			c.errs = append(c.errs, *pe)
			rec.err = int32(len(c.errs))
			if p.strs && !p.reuse {
				c.owns = append(c.owns, nil)
			}
		} else {
			rec.first, rec.last = p.build(c, sc, lines)
		}
		rec.end = int32(sc.abs())
		c.recs = append(c.recs, rec)
		c.end = scanState{sc.abs(), sc.line, sc.lineStart, sc.recNo}
	}
}

// build stores the record the scanner holds in the chunk, as the consumer
// will hand it out, and returns where its fields are in strs or views.
func (p *parallel) build(c *chunk, sc *scanner, lines int) (int32, int32) {
	n := len(sc.fields)
	first := len(c.recs) == 0
	switch {
	case p.strs && p.reuse:
		if first && cap(c.strs) < lines*n {
			c.strs = make([]string, 0, lines*n+16)
		}
		str := sc.recordString()
		at := len(c.strs)
		for _, f := range sc.fields {
			c.strs = append(c.strs, str[f.start:f.end])
		}
		return int32(at), int32(len(c.strs))
	case p.strs:
		str := sc.recordString()
		own := make([]string, n)
		for i, f := range sc.fields {
			own[i] = str[f.start:f.end]
		}
		c.owns = append(c.owns, own)
		return 0, int32(n)
	default:
		if first && cap(c.views) < lines*n {
			c.views = make([][]byte, 0, lines*n+16)
		}
		sc.collapse()
		text := sc.out
		if sc.outAt < 0 {
			at := len(c.arena)
			c.arena = append(c.arena, sc.out...)
			text = c.arena[at:]
		}
		at := len(c.views)
		for _, f := range sc.fields {
			c.views = append(c.views, text[f.start:f.end:f.end])
		}
		return int32(at), int32(len(c.views))
	}
}

// next makes the next record the last returned. It returns nil, io.EOF, a
// *ParseError, or the error that ended the input.
func (p *parallel) next() error {
	if p.done != nil {
		return p.done
	}
	for {
		if p.fb != nil {
			return p.fallbackNext()
		}
		if len(p.ahead) == 0 {
			c, ok := <-p.order
			if !ok {
				p.done = ErrClosed
				return p.done
			}
			p.ahead = append(p.ahead, c)
			if !p.begun {
				p.begun = true
				p.baseLine, p.baseRecNo = c.line, p.startRecNo
			}
		}
		c := p.ahead[0]
		if !p.ready {
			<-c.done
			select {
			case <-p.quit:
				p.done = ErrClosed
				return p.done
			default:
			}
			p.ready = true
		}
		if i := p.idx; i < len(c.recs) {
			rec := &c.recs[i]
			p.idx++
			if p.didx < int(rec.defects) {
				p.replayDefects(c, int(rec.defects))
			}
			p.offset = c.base + int64(rec.end)
			if rec.err != 0 {
				return p.abs(c, &c.errs[rec.err-1])
			}
			if p.lastChunk != c {
				p.setLastChunk(c)
			}
			p.lastRec = i
			p.lastLineBase = p.baseLine - 1
			return nil
		}
		if c.last {
			p.replayDefects(c, len(c.defects))
			p.done = c.fatal
			if p.done == nil {
				p.done = io.EOF
			}
			return p.done
		}
		if c.clean {
			p.advance(c.final)
			continue
		}
		p.startFallback(c)
	}
}

// setLastChunk notes that the record about to be returned is in c, or comes
// from the fallback when c is nil, and releases the chunk of the previous one
// if the consumer has left it.
func (p *parallel) setLastChunk(c *chunk) {
	if p.lastChunk != nil && p.lastLeft {
		p.release(p.lastChunk)
	}
	p.lastChunk, p.lastLeft = c, false
}

// advance moves from the finished current chunk to the next one, which
// starts where st says the current one ended.
func (p *parallel) advance(st scanState) {
	p.baseLine += st.line - 1
	p.baseRecNo += st.recNo
	if c := p.ahead[0]; c == p.lastChunk {
		p.lastLeft = true
	} else {
		p.release(c)
	}
	p.ahead = p.ahead[1:]
	p.idx, p.didx, p.ready = 0, 0, false
}

func (p *parallel) replayDefects(c *chunk, upTo int) {
	for ; p.didx < upTo; p.didx++ {
		d := &c.defects[p.didx]
		switch d.Err {
		case ErrBareQuote:
			p.bare++
		case ErrStrayQuote:
			p.stray++
		case ErrInvalidUTF8:
			p.invalid++
		}
		if p.warn != nil {
			p.warn(p.abs(c, d))
		}
	}
}

// abs returns a copy of a chunk's error with absolute positions.
func (p *parallel) abs(c *chunk, pe *ParseError) *ParseError {
	e := *pe
	e.Record += p.baseRecNo
	e.Line += p.baseLine - 1
	e.Offset += c.base
	return &e
}

// lastFields returns the fields of the record last returned, with where each
// starts, and what to add to their lines. A record from a chunk is read again
// for it, once.
func (p *parallel) lastFields() ([]field, int) {
	c := p.lastChunk
	if c == nil {
		return p.ownFields, 0
	}
	if p.posChunk != c || p.posRec != p.lastRec {
		rec := &c.recs[p.lastRec]
		sc := &scanner{buf: c.text, pos: int(rec.start), end: len(c.text), eof: true, lim: p.lim, strict: p.strict, lastInvalid: -1}
		sc.setDialect(p.dialect)
		sc.line, sc.lineStart = int(rec.line), int64(rec.start)
		sc.readRecord()
		p.pos = append(p.pos[:0], sc.fields...)
		p.posChunk, p.posRec = c, p.lastRec
	}
	return p.pos, p.lastLineBase
}

// lastStrings returns the record last returned as the workers made it, or
// nil when they made views.
func (p *parallel) lastStrings() []string {
	c := p.lastChunk
	switch {
	case c == nil || !p.strs:
		return nil
	case p.reuse:
		rec := &c.recs[p.lastRec]
		return c.strs[rec.first:rec.last:rec.last]
	}
	return c.owns[p.lastRec]
}

// lastViews returns the record last returned as views the workers made, or
// nil when they made strings.
func (p *parallel) lastViews() [][]byte {
	c := p.lastChunk
	if c == nil || p.strs {
		return nil
	}
	rec := &c.recs[p.lastRec]
	return c.views[rec.first:rec.last:rec.last]
}

// startFallback starts reading sequentially from the last complete record of
// c, which did not end cleanly.
func (p *parallel) startFallback(c *chunk) {
	st := c.end
	sc := newScanner(&fallbackReader{p: p}, p.dialect, p.lim, p.strict)
	sc.base = c.base + st.off
	sc.line = p.baseLine + st.line - 1
	sc.lineStart = c.base + st.lineStart
	sc.recNo = p.baseRecNo + st.recNo
	sc.warn = p.warn
	p.fb = sc
	p.fbPos.chunk, p.fbPos.at = 0, int(st.off)
	p.fbCounts = [3]int64{}
	// The defects of the record the worker did not finish are read again.
	p.didx = len(c.defects)
}

func (p *parallel) fallbackNext() error {
	sc := p.fb
	err := sc.readRecord()
	p.bare += sc.bare - p.fbCounts[0]
	p.stray += sc.stray - p.fbCounts[1]
	p.invalid += sc.invalid - p.fbCounts[2]
	p.fbCounts = [3]int64{sc.bare, sc.stray, sc.invalid}
	if err != nil {
		if _, ok := errors.AsType[*ParseError](err); !ok {
			p.done = err
			return err
		}
	} else {
		if p.strs {
			p.fbStr, p.fbIsStr = sc.recordString(), true
		} else {
			sc.collapse()
			p.fbBytes, p.fbIsStr = sc.out, false
		}
		if p.lastChunk != nil {
			p.setLastChunk(nil)
		}
		p.ownFields = append(p.ownFields[:0], sc.fields...)
	}
	p.offset = sc.abs()
	p.resync()
	return err
}

// resync drops the fallback scanner when it stands exactly where a later
// chunk starts, and makes that chunk current.
func (p *parallel) resync() {
	at := p.fb.abs()
	for i := 1; i < len(p.ahead); i++ {
		c := p.ahead[i]
		if c.base > at {
			break
		}
		if c.base == at {
			p.baseLine = p.fb.line
			p.baseRecNo = p.fb.recNo
			p.ahead = p.ahead[i:]
			p.idx, p.didx, p.ready = 0, 0, false
			p.fb = nil
			return
		}
	}
	// Chunks the scanner has read past and that start before it are done.
	for p.fbPos.chunk > 0 && len(p.ahead) > 1 && p.ahead[1].base < at {
		p.ahead = p.ahead[1:]
		p.fbPos.chunk--
	}
}

// fallbackReader serves the text of the chunks to the fallback scanner,
// taking them from the producer as it goes.
type fallbackReader struct {
	p *parallel
}

func (r *fallbackReader) Read(b []byte) (int, error) {
	p := r.p
	for {
		c := p.ahead[p.fbPos.chunk]
		if p.fbPos.at < len(c.text) {
			n := copy(b, c.text[p.fbPos.at:])
			p.fbPos.at += n
			return n, nil
		}
		if c.last {
			if c.rerr != nil {
				return 0, c.rerr
			}
			return 0, io.EOF
		}
		if p.fbPos.chunk+1 == len(p.ahead) {
			next, ok := <-p.order
			if !ok {
				return 0, ErrClosed
			}
			p.ahead = append(p.ahead, next)
		}
		p.fbPos.chunk++
		p.fbPos.at = 0
	}
}
