package wildcsv

import (
	"errors"
	"fmt"
)

// Errors wrapped by [*ParseError]. The first three are accepted as data when
// reading leniently and are errors only with [Options].Strict.
var (
	ErrBareQuote         = errors.New("quote character in an unquoted field")
	ErrStrayQuote        = errors.New("quote character inside a quoted field that neither doubles nor closes it")
	ErrInvalidUTF8       = errors.New("invalid UTF-8")
	ErrUnterminatedQuote = errors.New("quoted field not closed before the end of the input")
	ErrFieldTooLarge     = errors.New("field exceeds MaxFieldBytes")
	ErrRecordTooLarge    = errors.New("record exceeds MaxRecordBytes")
	ErrTooManyFields     = errors.New("record exceeds MaxFields")
)

// Errors wrapped by the other error types, and the error for invalid options.
var (
	ErrNotDelimited        = errors.New("input is not delimited text")
	ErrUnsupportedEncoding = errors.New("unsupported encoding")
	ErrInvalidOptions      = errors.New("invalid options")
)

// ParseError reports a problem in the data and where it is.
//
// Lines count every line break, including those inside quoted fields and
// blank lines. Columns and offsets count bytes of the UTF-8 text after
// decoding and after a byte order mark is removed.
type ParseError struct {
	Record int   // 1-based number of the record being read
	Line   int   // 1-based line of the position
	Column int   // 1-based byte index in that line
	Offset int64 // 0-based byte offset in the text
	Quoted bool  // the position is inside a quoted field, or is its opening quote
	Err    error // one of ErrBareQuote, ErrStrayQuote, ErrInvalidUTF8, ErrUnterminatedQuote, ErrFieldTooLarge, ErrRecordTooLarge, ErrTooManyFields
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("wildcsv: record %d, line %d, column %d: %v", e.Record, e.Line, e.Column, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// KindError is returned by [NewReader] for input that is not delimited text.
type KindError struct {
	Kind Kind
}

func (e *KindError) Error() string {
	return fmt.Sprintf("wildcsv: input is %s, not delimited text", e.Kind)
}

func (e *KindError) Unwrap() error { return ErrNotDelimited }

// EncodingError is returned for an encoding that wildcsv recognises but does
// not read, such as UTF-32.
type EncodingError struct {
	Encoding string
}

func (e *EncodingError) Error() string {
	return fmt.Sprintf("wildcsv: %s is not supported; convert the input to UTF-8 or pass Options.Decode", e.Encoding)
}

func (e *EncodingError) Unwrap() error { return ErrUnsupportedEncoding }

func invalidOptions(format string, args ...any) error {
	return fmt.Errorf("wildcsv: %w: %s", ErrInvalidOptions, fmt.Sprintf(format, args...))
}
