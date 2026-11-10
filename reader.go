package wildcsv

import (
	"errors"
	"io"
	"iter"
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
	sc        *scanner
	det       *Detection
	reuse     bool
	record    []string
	positions []fieldPos
	records   int64
	offset    int64
	late      *lateConv
	encoding  Encoding
	done      error
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
			rd.late = &lateConv{}
			body = newDecoder(body, rd.late)
		}
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
	if r.done != nil {
		return nil, r.done
	}
	err := r.sc.readRecord()
	if err != nil {
		var pe *ParseError
		if !errors.As(err, &pe) {
			r.done = err
		}
		return nil, err
	}
	sc := r.sc
	n := len(sc.ends)
	var rec []string
	if r.reuse && cap(r.record) >= n {
		rec = r.record[:n]
	} else {
		rec = make([]string, n)
	}
	str := string(sc.rec)
	prev := 0
	for i, e := range sc.ends {
		rec[i] = str[prev:e]
		prev = e
	}
	if r.reuse {
		r.record = rec
	}
	r.positions = append(r.positions[:0], sc.fpos...)
	r.records++
	r.offset = sc.abs()
	return rec, nil
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
	if field < 0 || field >= len(r.positions) {
		panic("wildcsv: FieldPos: field index out of range")
	}
	p := r.positions[field]
	return p.line, p.col
}

// InputOffset returns the byte offset in the text just after the record last
// returned, including its line break.
func (r *Reader) InputOffset() int64 { return r.offset }

// Stats returns the counts so far.
func (r *Reader) Stats() Stats {
	st := Stats{
		Records:     r.records,
		BareQuotes:  r.sc.bare,
		StrayQuotes: r.sc.stray,
		InvalidUTF8: r.sc.invalid,
		Encoding:    r.encoding,
	}
	if r.late != nil && r.late.decided != "" {
		st.Encoding = r.late.decided
	}
	return st
}
