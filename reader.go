package wildcsv

import (
	"errors"
	"io"
	"iter"
	"runtime"
)

// Stats counts what a [Reader] has read and the defects it accepted.
type Stats struct {
	Records     int64    // records returned
	BareQuotes  int64    // quote characters read as data in unquoted fields
	StrayQuotes int64    // quote characters read as data inside quoted fields
	InvalidUTF8 int64    // invalid sequences replaced with U+FFFD
	Encoding    Encoding // the encoding finally used
}

// Reader streams the records of delimited text.
//
// A Reader is not safe for concurrent use. Readers do not share state.
type Reader struct {
	sc       *scanner  // reads the text, unless par does
	par      *parallel // reads the text with Options.Concurrency goroutines
	det      *Detection
	reuse    bool
	record   []string
	slices   [][]byte
	fields   []field // of the record last returned, when sequential
	records  int64
	offset   int64
	late     *lateReader
	encoding Encoding
	done     error
}

// NewReader detects the kind, encoding and dialect of r from a sample, then
// returns a Reader that streams the records of the whole input. The sample is
// read once; r need not be seekable.
//
// It returns a [*KindError] when the input is not delimited text, unless
// Options.Dialect declares the dialect. An empty input gives a Reader whose
// first Read returns io.EOF.
func NewReader(r io.Reader, opts *Options) (*Reader, error) {
	s, err := opts.settings()
	if err != nil {
		return nil, err
	}
	head, complete, err := readHead(r, s.lim.SampleBytes)
	if err != nil {
		return nil, err
	}
	det, dec, err := detect(head, complete, s)
	if err != nil {
		return nil, err
	}
	if s.Dialect == nil && det.Kind != KindDelimited && det.Kind != KindEmpty {
		return nil, &KindError{Kind: det.Kind}
	}
	rd := &Reader{det: det, reuse: s.ReuseRecord, encoding: dec.enc}
	var body io.Reader
	if dec.enc == CustomDecoding {
		body = s.Decode(io.MultiReader(&sliceReader{b: head}, r))
	} else {
		body = io.MultiReader(&sliceReader{b: head[dec.bomLen:]}, r)
		switch {
		case dec.enc == UTF16LE || dec.enc == UTF16BE:
			body = newDecoder(body, &utf16Conv{bigEndian: dec.enc == UTF16BE})
		case dec.enc == Windows1252:
			body = newDecoder(body, cp1252Conv{})
		case dec.basis == BasisASCII:
			rd.late = &lateReader{src: body}
			body = rd.late
		}
	}
	if s.Concurrency > 1 && !complete {
		rd.par = newParallel(body, det.Dialect, s, s.Concurrency)
		runtime.AddCleanup(rd, func(p *parallel) { p.close() }, rd.par)
		return rd, nil
	}
	rd.sc = newScanner(body, det.Dialect, s.lim, s.Strict)
	rd.sc.warn = s.Warn
	rd.sc.skipLines = s.SkipLines
	return rd, nil
}

// readHead reads up to n+1 bytes, so that the caller knows whether n bytes
// are the whole input.
func readHead(r io.Reader, n int) ([]byte, bool, error) {
	head := make([]byte, n+1)
	m, err := io.ReadFull(r, head)
	switch {
	case err == io.EOF || err == io.ErrUnexpectedEOF:
		return head[:m], true, nil
	case err != nil:
		return nil, false, err
	}
	return head, false, nil
}

// Detection returns the report of the sample the Reader was built from.
func (r *Reader) Detection() *Detection { return r.det }

// Read returns the next record. It returns io.EOF at the end of the input and
// a [*ParseError] for a problem in the data, after which it can be called
// again. Errors from the underlying reader are returned as they are.
//
// Unless Options.ReuseRecord is set, each call returns a new slice.
func (r *Reader) Read() ([]string, error) {
	if err := r.advance(true); err != nil {
		return nil, err
	}
	var str string
	var fields []field
	if p := r.par; p != nil {
		if rec := p.lastStrings(); rec != nil {
			r.accept()
			return rec, nil
		}
		if views := p.lastViews(); views != nil {
			rec := r.recordSlice(len(views))
			for i, v := range views {
				rec[i] = string(v)
			}
			r.accept()
			return rec, nil
		}
		fields, _ = p.lastFields()
		if p.fbIsStr {
			str = p.fbStr
		} else {
			str = string(p.fbBytes[:fields[len(fields)-1].end])
		}
	} else {
		str, fields = r.sc.recordString(), r.sc.fields
	}
	rec := r.recordSlice(len(fields))
	for i, f := range fields {
		rec[i] = str[f.start:f.end]
	}
	r.accept()
	return rec, nil
}

// recordSlice returns a slice of n strings for Read to fill: the previous one
// again when records may share it.
func (r *Reader) recordSlice(n int) []string {
	if r.reuse && cap(r.record) >= n {
		r.record = r.record[:n]
		return r.record
	}
	rec := make([]string, n)
	if r.reuse {
		r.record = rec
	}
	return rec
}

// ReadSlices is Read without copies: like the line bufio.Reader.ReadSlice
// returns, the fields point into the Reader's buffers and are valid only
// until the next call to a read method, and the slice holding them is reused.
// Once the Reader has warmed up it allocates nothing, which makes it the
// fastest way to read when each field is parsed or copied right away.
func (r *Reader) ReadSlices() ([][]byte, error) {
	if err := r.advance(false); err != nil {
		return nil, err
	}
	r.slices = r.slices[:0]
	if p := r.par; p != nil {
		if views := p.lastViews(); views != nil {
			r.accept()
			return views, nil
		}
		if strs := p.lastStrings(); strs != nil {
			for _, s := range strs {
				r.slices = append(r.slices, []byte(s))
			}
		} else {
			fields, _ := p.lastFields()
			for _, f := range fields {
				if p.fbIsStr {
					r.slices = append(r.slices, []byte(p.fbStr[f.start:f.end]))
				} else {
					r.slices = append(r.slices, p.fbBytes[f.start:f.end:f.end])
				}
			}
		}
	} else {
		sc := r.sc
		sc.collapse()
		for _, f := range sc.fields {
			r.slices = append(r.slices, sc.out[f.start:f.end:f.end])
		}
	}
	r.accept()
	return r.slices, nil
}

// advance reads the next record. strs says whether the caller wants strings,
// which a parallel Reader then has its workers make.
func (r *Reader) advance(strs bool) error {
	if r.done != nil {
		return r.done
	}
	var err error
	if p := r.par; p != nil {
		if !p.started {
			p.start(strs)
		}
		err = p.next()
	} else {
		err = r.sc.readRecord()
	}
	if err != nil {
		if _, ok := errors.AsType[*ParseError](err); !ok {
			r.done = err
			if r.par != nil {
				r.par.close()
			}
		}
	}
	return err
}

// accept makes the record just read the one last returned.
func (r *Reader) accept() {
	if p := r.par; p != nil {
		r.offset = p.offset
	} else {
		sc := r.sc
		r.fields, sc.fields = sc.fields, r.fields[:0]
		r.offset = sc.abs()
	}
	r.records++
}

// Close stops the goroutines of a Reader whose Options.Concurrency is above
// one; for other Readers it only marks them closed. Reading to io.EOF, or to
// an error that is not a [*ParseError], stops the goroutines as well, and so
// does the garbage collector once the Reader is unreachable. After Close the
// read methods return [ErrClosed], or the error that ended the input if one
// had. Close does not close the underlying reader, and always returns nil.
func (r *Reader) Close() error {
	if r.par != nil {
		r.par.close()
	}
	if r.done == nil {
		r.done = ErrClosed
	}
	return nil
}

// All yields each record or error in turn. It stops at the end of the input
// or after an error that is not a [*ParseError].
func (r *Reader) All() iter.Seq2[[]string, error] {
	return func(yield func([]string, error) bool) {
		for {
			rec, err := r.Read()
			if err == io.EOF {
				return
			}
			if !yield(rec, err) {
				return
			}
			if err != nil {
				var pe *ParseError
				if !errors.As(err, &pe) {
					return
				}
			}
		}
	}
}

// FieldPos returns the line and byte column where the given field of the
// record last returned starts; for a quoted field, its opening quote. It
// panics if field is out of range.
func (r *Reader) FieldPos(field int) (line, column int) {
	fields, lineBase := r.fields, 0
	if r.par != nil {
		fields, lineBase = r.par.lastFields()
	}
	if field < 0 || field >= len(fields) {
		panic("wildcsv: FieldPos: field index out of range")
	}
	f := fields[field]
	return f.line + lineBase, f.col
}

// InputOffset returns the byte offset in the text just after the record last
// returned, including its line break.
func (r *Reader) InputOffset() int64 { return r.offset }

// Stats returns the counts so far.
func (r *Reader) Stats() Stats {
	st := Stats{Records: r.records, Encoding: r.encoding}
	if p := r.par; p != nil {
		st.BareQuotes, st.StrayQuotes, st.InvalidUTF8 = p.bare, p.stray, p.invalid
	} else {
		st.BareQuotes, st.StrayQuotes, st.InvalidUTF8 = r.sc.bare, r.sc.stray, r.sc.invalid
	}
	if r.late != nil {
		if e := r.late.encoding(); e != "" {
			st.Encoding = e
		}
	}
	return st
}
