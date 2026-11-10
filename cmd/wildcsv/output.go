package main

import (
	"fmt"
	"math"

	"github.com/Altitud-Studio-S-L-U/wildcsv"
)

type encodingJSON struct {
	Name  string `json:"name"`
	BOM   bool   `json:"bom"`
	Basis string `json:"basis"`
}

type dialectJSON struct {
	Delimiter        string  `json:"delimiter"`
	Quote            *string `json:"quote"`
	Escape           *string `json:"escape"`
	SkipInitialSpace bool    `json:"skip_initial_space"`
}

type alternativeJSON struct {
	dialectJSON
	RelativeScore float64 `json:"relative_score"`
}

func runeOrNil(r rune) *string {
	if r == 0 {
		return nil
	}
	s := string(r)
	return &s
}

func toDialectJSON(d wildcsv.Dialect) dialectJSON {
	return dialectJSON{
		Delimiter:        string(d.Delimiter),
		Quote:            runeOrNil(d.Quote),
		Escape:           runeOrNil(d.Escape),
		SkipInitialSpace: d.SkipInitialSpace,
	}
}

func toEncodingJSON(det *wildcsv.Detection) *encodingJSON {
	if det.Encoding == "" {
		return nil
	}
	return &encodingJSON{Name: string(det.Encoding), BOM: det.BOM, Basis: det.EncodingBasis}
}

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }

var charNames = map[rune]string{
	',': "comma", ';': "semicolon", '\t': "tab", '|': "pipe", ':': "colon", ' ': "space",
	'=': "equals sign", '#': "number sign", '*': "asterisk", '"': "double quote", '\'': "apostrophe",
	'\\': "backslash", '~': "tilde",
}

// describeChar shows a character with its name, because a tab or a space is
// invisible in a terminal.
func describeChar(r rune) string {
	if r == 0 {
		return "none"
	}
	shown := string(r)
	if r == '\t' {
		shown = `\t`
	}
	if name, ok := charNames[r]; ok {
		return fmt.Sprintf("%s (%s)", shown, name)
	}
	return fmt.Sprintf("%s (U+%04X)", shown, r)
}

var kindNames = map[wildcsv.Kind]string{
	wildcsv.KindJSON:  "JSON",
	wildcsv.KindJSONL: "JSON Lines",
	wildcsv.KindHTML:  "HTML",
	wildcsv.KindXML:   "XML",
}

// hint is one sentence about the next step, for a person or an agent.
func hint(det *wildcsv.Detection, declared bool) string {
	switch det.Kind {
	case wildcsv.KindXLSX, wildcsv.KindXLS, wildcsv.KindODS:
		return "This is a spreadsheet workbook, not delimited text. Export the sheet as CSV, or read it with a spreadsheet library."
	case wildcsv.KindZIP:
		return "This is a ZIP archive. Extract it and inspect the file inside."
	case wildcsv.KindXZ, wildcsv.KindZstd:
		return "This file is compressed. Decompress it and inspect the result."
	case wildcsv.KindPDF, wildcsv.KindParquet, wildcsv.KindBinary:
		return "This is not text. wildcsv reads delimited text only."
	case wildcsv.KindJSON, wildcsv.KindJSONL, wildcsv.KindHTML, wildcsv.KindXML:
		n := kindNames[det.Kind]
		return fmt.Sprintf("This is %s, not delimited text. Read it with a %s parser.", n, n)
	case wildcsv.KindEmpty:
		return "The input is empty."
	}
	switch {
	case !declared && det.Confidence < lowConfidence:
		return "The dialect is uncertain. Compare the alternatives, or pass --delimiter."
	case det.EncodingBasis == wildcsv.BasisFallback:
		return "The text is not valid UTF-8 and was read as Windows-1252. If characters look wrong, pass --encoding."
	}
	return ""
}

const lowConfidence = 0.2
