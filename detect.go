package wildcsv

import (
	"io"
)

// Detection reports what a sample of the input is and how to read it.
type Detection struct {
	Kind          Kind
	Encoding      Encoding
	EncodingBasis string // how the encoding was decided; one of the Basis constants
	BOM           bool   // a byte order mark was found and removed
	Dialect       Dialect
	LineEnding    string        // lf, crlf, cr, mixed, or none
	Confidence    float64       // margin over the best dialect with another delimiter: 0 to 1
	Alternatives  []Alternative // up to three runner-up dialects
	Header        HeaderGuess
	Columns       []Column
	SampleBytes   int  // bytes examined
	SampleRecords int  // records of the sample the report rests on
	Complete      bool // the sample is the whole input
}

// Alternative is a runner-up dialect.
type Alternative struct {
	Dialect       Dialect
	RelativeScore float64 // its score divided by the winner's
}

// Detect reads a sample of r and reports its kind, encoding and dialect, the
// header guess and the kind of each column. It reads at most
// Limits.SampleBytes + 1 bytes.
//
// It returns a report for every kind, and an error only when reading fails,
// the options are invalid or the encoding is not supported.
func Detect(r io.Reader, opts *Options) (*Detection, error) {
	s, err := opts.settings()
	if err != nil {
		return nil, err
	}
	head, complete, err := readHead(r, s.lim.SampleBytes)
	if err != nil {
		return nil, err
	}
	det, _, err := detect(head, complete, s)
	return det, err
}

func defaultDialect(s *settings) Dialect {
	if s.Dialect != nil {
		return *s.Dialect
	}
	return Dialect{Delimiter: ',', Quote: '"'}
}

func detect(head []byte, complete bool, s *settings) (*Detection, encodingDecision, error) {
	sample := head
	if !complete {
		sample = head[:s.lim.SampleBytes]
	}
	det := &Detection{
		SampleBytes: len(sample),
		Complete:    complete,
		Dialect:     defaultDialect(s),
		Header:      HeaderUnknown,
		LineEnding:  "none",
	}
	dec, err := decideEncoding(sample, complete, s)
	if err != nil {
		return nil, dec, err
	}
	det.Encoding, det.EncodingBasis, det.BOM = dec.enc, dec.basis, dec.bomLen > 0
	if k := rawKind(sample); k != "" {
		det.Kind = k
		return det, dec, nil
	}
	text, err := decodeSample(dec, sample, complete, s)
	if err != nil {
		return nil, dec, err
	}
	text = skipLines(text, s.SkipLines)
	det.Kind = textKind(text, complete)
	if det.Kind != KindDelimited {
		return det, dec, nil
	}
	det.LineEnding = lineEnding(text)

	var table *sampleTable
	if s.Dialect != nil {
		det.Confidence = 1
		table = readTable(text, *s.Dialect, s.lim, complete, maxScoredRecords)
	} else {
		det.Dialect, det.Confidence, det.Alternatives, table = detectDialect(text, complete, s)
	}
	det.SampleRecords = len(table.records)
	if len(table.records) > 0 {
		ncols := columnCount(table)
		det.Header = guessHeader(table, ncols)
		det.Columns = describeColumns(table, ncols, det.Header)
	}
	return det, dec, nil
}

// skipLines drops the first n lines of the sample.
func skipLines(text []byte, n int) []byte {
	for ; n > 0 && len(text) > 0; n-- {
		i := 0
		for i < len(text) && text[i] != '\n' && text[i] != '\r' {
			i++
		}
		if i < len(text) && text[i] == '\r' {
			i++
		}
		if i < len(text) && text[i] == '\n' {
			i++
		}
		text = text[i:]
	}
	return text
}

func lineEnding(text []byte) string {
	var lf, crlf, cr bool
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			lf = true
		case '\r':
			if i+1 < len(text) && text[i+1] == '\n' {
				crlf = true
				i++
			} else if i+1 < len(text) {
				cr = true
			} else {
				cr = true
			}
		}
	}
	n := 0
	name := "none"
	for _, e := range []struct {
		seen bool
		name string
	}{{lf, "lf"}, {crlf, "crlf"}, {cr, "cr"}} {
		if e.seen {
			n++
			name = e.name
		}
	}
	if n > 1 {
		return "mixed"
	}
	return name
}
