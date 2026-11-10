package wildcsv

import (
	"io"
	"strings"
)

// Default limits. A zero field in [Limits] selects the default.
const (
	DefaultSampleBytes    = 64 << 10
	DefaultMaxFieldBytes  = 1 << 20
	DefaultMaxRecordBytes = 4 << 20
	DefaultMaxFields      = 16384
)

// Limits bounds what detection examines and what one record may hold.
type Limits struct {
	SampleBytes    int // bytes examined by detection
	MaxFieldBytes  int // bytes of one field's content
	MaxRecordBytes int // bytes of one record in the input, with its quotes and delimiters
	MaxFields      int // fields in one record
}

func (l Limits) withDefaults() (Limits, error) {
	if l.SampleBytes < 0 || l.MaxFieldBytes < 0 || l.MaxRecordBytes < 0 || l.MaxFields < 0 {
		return l, invalidOptions("limits must not be negative")
	}
	if l.SampleBytes == 0 {
		l.SampleBytes = DefaultSampleBytes
	}
	if l.MaxFieldBytes == 0 {
		l.MaxFieldBytes = DefaultMaxFieldBytes
	}
	if l.MaxRecordBytes == 0 {
		l.MaxRecordBytes = DefaultMaxRecordBytes
	}
	if l.MaxFields == 0 {
		l.MaxFields = DefaultMaxFields
	}
	if l.MaxFieldBytes > l.MaxRecordBytes {
		return l, invalidOptions("MaxFieldBytes (%d) exceeds MaxRecordBytes (%d)", l.MaxFieldBytes, l.MaxRecordBytes)
	}
	return l, nil
}

// Options controls detection and reading. A nil *Options and a zero Options
// both mean: detect everything, read leniently, default limits.
type Options struct {
	Dialect     *Dialect                  // use this dialect; do not detect one
	Delimiters  []rune                    // candidate delimiters for detection
	Quotes      []rune                    // candidate quotes for detection; 0 stands for no quoting
	Encoding    Encoding                  // use this encoding; do not detect one
	Decode      func(io.Reader) io.Reader // convert the source to UTF-8; wildcsv then does no decoding of its own
	SkipLines   int                       // lines to discard before the table
	Strict      bool                      // report quoting defects and invalid UTF-8 as errors
	Warn        func(*ParseError)         // called for each defect that lenient reading accepts
	ReuseRecord bool                      // Read may return a slice that shares its backing array with the previous one
	Limits      Limits
}

// settings is Options after validation, with defaults applied.
type settings struct {
	Options
	lim      Limits
	encoding Encoding // normalised; "" when not declared
}

func (o *Options) settings() (*settings, error) {
	var s settings
	if o != nil {
		s.Options = *o
	}
	lim, err := s.Limits.withDefaults()
	if err != nil {
		return nil, err
	}
	s.lim = lim
	if s.SkipLines < 0 {
		return nil, invalidOptions("SkipLines must not be negative")
	}
	if s.Dialect != nil {
		if err := s.Dialect.validate(); err != nil {
			return nil, err
		}
	}
	for _, d := range s.Delimiters {
		if !validSpecial(d) {
			return nil, invalidOptions("delimiter %q cannot be used", d)
		}
	}
	for _, q := range s.Quotes {
		if q != 0 && !validSpecial(q) {
			return nil, invalidOptions("quote %q cannot be used", q)
		}
	}
	if s.Encoding != "" {
		e, ok := normalizeEncoding(string(s.Encoding))
		if !ok {
			return nil, invalidOptions("unknown encoding %q", s.Encoding)
		}
		s.encoding = e
	}
	return &s, nil
}

// normalizeEncoding maps an encoding name or alias to an [Encoding].
func normalizeEncoding(name string) (Encoding, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "utf-8", "utf8":
		return UTF8, true
	case "utf-16le", "utf16le":
		return UTF16LE, true
	case "utf-16be", "utf16be":
		return UTF16BE, true
	case "windows-1252", "cp1252", "latin1", "latin-1", "iso-8859-1", "iso8859-1":
		return Windows1252, true
	}
	return "", false
}
