package wildcsv

import (
	"fmt"
	"unicode/utf8"
)

// Dialect says how a delimited text separates fields and quotes them.
//
// Records end at a line feed, at a carriage return followed by a line feed,
// or at a lone carriage return; all three are accepted in the same input.
type Dialect struct {
	Delimiter        rune // separates fields
	Quote            rune // surrounds a field that contains delimiters or line breaks; 0 when fields are never quoted
	Escape           rune // inside a quoted field, makes the next quote or escape character literal; 0 when unused
	SkipInitialSpace bool // spaces after a delimiter are padding, not data
}

// String describes the dialect in words, such as `delimiter ";" quote "\""`.
func (d Dialect) String() string {
	s := fmt.Sprintf("delimiter %q", d.Delimiter)
	if d.Quote == 0 {
		s += " no quote"
	} else {
		s += fmt.Sprintf(" quote %q", d.Quote)
	}
	if d.Escape != 0 {
		s += fmt.Sprintf(" escape %q", d.Escape)
	}
	if d.SkipInitialSpace {
		s += " skip-initial-space"
	}
	return s
}

func validSpecial(r rune) bool {
	return r > 0 && r != '\r' && r != '\n' && r != utf8.RuneError && utf8.ValidRune(r)
}

func (d Dialect) validate() error {
	if !validSpecial(d.Delimiter) {
		return invalidOptions("delimiter %q cannot be used", d.Delimiter)
	}
	if d.Quote != 0 {
		if !validSpecial(d.Quote) {
			return invalidOptions("quote %q cannot be used", d.Quote)
		}
		if d.Quote == d.Delimiter {
			return invalidOptions("quote and delimiter are both %q", d.Quote)
		}
	}
	if d.Escape != 0 {
		if d.Quote == 0 {
			return invalidOptions("an escape character needs a quote character")
		}
		if !validSpecial(d.Escape) {
			return invalidOptions("escape %q cannot be used", d.Escape)
		}
		if d.Escape == d.Delimiter {
			return invalidOptions("escape and delimiter are both %q", d.Escape)
		}
	}
	if d.SkipInitialSpace && d.Delimiter == ' ' {
		return invalidOptions("SkipInitialSpace cannot be combined with the space as delimiter")
	}
	return nil
}
