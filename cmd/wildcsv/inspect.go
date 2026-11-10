package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/Altitud-Studio-S-L-U/wildcsv"
)

type inspectDoc struct {
	Schema       string            `json:"schema"`
	File         string            `json:"file"`
	Kind         string            `json:"kind"`
	Compression  *string           `json:"compression"`
	Encoding     *encodingJSON     `json:"encoding"`
	Dialect      *dialectJSON      `json:"dialect"`
	LineEnding   string            `json:"line_ending"`
	Confidence   float64           `json:"confidence"`
	Alternatives []alternativeJSON `json:"alternatives"`
	Header       headerJSON        `json:"header"`
	Columns      []columnJSON      `json:"columns"`
	ColumnCount  int               `json:"column_count"`
	Sample       sampleJSON        `json:"sample"`
	Hint         string            `json:"hint"`
}

type headerJSON struct {
	Present *bool    `json:"present"`
	Names   []string `json:"names"`
}

type columnJSON struct {
	Index        int    `json:"index"`
	Name         string `json:"name,omitempty"`
	Kind         string `json:"kind"`
	DecimalComma bool   `json:"decimal_comma,omitempty"`
}

type sampleJSON struct {
	Bytes    int        `json:"bytes"`
	Complete bool       `json:"complete"`
	Records  [][]string `json:"records"`
	Cut      bool       `json:"cut"`
}

func (e *env) inspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	var c common
	c.register(fs)
	header := fs.String("header", "auto", "whether the first record is a header: yes, no or auto")
	rows := fs.Int("rows", 5, "sample records to show, at most 100")
	columns := fs.Int("columns", 50, "cells to show per record")
	cellBytes := fs.Int("cell-bytes", 64, "bytes to show per cell")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	doc, err := e.inspectDoc(fs, &c, *header, *rows, *columns, *cellBytes)
	if err != nil {
		return e.fail(c.json, err)
	}
	if c.json {
		if err := writeJSON(e.stdout, doc); err != nil {
			return e.fail(false, ioErr(err))
		}
		return 0
	}
	if err := printInspect(e.stdout, doc); err != nil {
		return e.fail(false, ioErr(err))
	}
	return 0
}

func (e *env) inspectDoc(fs *flag.FlagSet, c *common, header string, rows, columns, cellBytes int) (*inspectDoc, error) {
	switch header {
	case "auto", "yes", "no":
	default:
		return nil, usageErr("--header must be yes, no or auto, got %q", header)
	}
	if rows < 0 || rows > 100 || columns < 1 || cellBytes < 1 {
		return nil, usageErr("--rows must be 0 to 100, --columns and --cell-bytes at least 1")
	}
	opts, err := c.options()
	if err != nil {
		return nil, err
	}
	in, err := e.open(fs)
	if err != nil {
		return nil, err
	}
	defer in.close()

	// Detect on a copy of the head, then read the sample records from the
	// same bytes followed by the rest of the input: nothing is read twice.
	sampleBytes := opts.Limits.SampleBytes
	if sampleBytes <= 0 {
		sampleBytes = wildcsv.DefaultSampleBytes
	}
	head := make([]byte, sampleBytes+1)
	n, err := io.ReadFull(in.r, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, ioErr(fmt.Errorf("%s: %w", in.name, err))
	}
	head = head[:n]
	det, err := wildcsv.Detect(bytes.NewReader(head), opts)
	if err != nil {
		return nil, readError(in, err)
	}
	doc := &inspectDoc{
		Schema:       "wildcsv/inspect/v1",
		File:         in.name,
		Kind:         string(det.Kind),
		Encoding:     toEncodingJSON(det),
		LineEnding:   det.LineEnding,
		Alternatives: []alternativeJSON{},
		Columns:      []columnJSON{},
		Sample:       sampleJSON{Bytes: det.SampleBytes, Complete: det.Complete, Records: [][]string{}},
		Header:       headerJSON{Names: []string{}},
		Hint:         hint(det, opts.Dialect != nil),
	}
	if in.compression != "" {
		doc.Compression = &in.compression
	}
	if det.Kind != wildcsv.KindDelimited {
		return doc, nil
	}
	dj := toDialectJSON(det.Dialect)
	doc.Dialect = &dj
	doc.Confidence = round3(det.Confidence)
	for _, a := range det.Alternatives {
		doc.Alternatives = append(doc.Alternatives, alternativeJSON{dialectJSON: toDialectJSON(a.Dialect), RelativeScore: round3(a.RelativeScore)})
	}

	ropts := *opts
	ropts.Dialect = &det.Dialect
	r, err := wildcsv.NewReader(io.MultiReader(bytes.NewReader(head), in.r), &ropts)
	if err != nil {
		return nil, readError(in, err)
	}
	var first []string
	for len(doc.Sample.Records) < max(rows, 1) {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		var pe *wildcsv.ParseError
		if errors.As(err, &pe) {
			continue
		}
		if err != nil {
			return nil, readError(in, err)
		}
		if first == nil {
			first = rec
		}
		if len(doc.Sample.Records) < rows {
			doc.Sample.Records = append(doc.Sample.Records, cutRecord(rec, columns, cellBytes, &doc.Sample.Cut))
		}
	}

	present := det.Header == wildcsv.HeaderPresent
	switch header {
	case "yes":
		present = true
	case "no":
		present = false
	}
	if header != "auto" || det.Header != wildcsv.HeaderUnknown {
		doc.Header.Present = &present
	}
	doc.ColumnCount = len(det.Columns)
	for i, col := range det.Columns {
		cj := columnJSON{Index: i + 1, Kind: string(col.Kind), DecimalComma: col.DecimalComma}
		if present && i < len(first) {
			cj.Name = cutCell(strings.TrimSpace(first[i]), cellBytes, &doc.Sample.Cut)
		}
		if i < columns {
			doc.Columns = append(doc.Columns, cj)
		} else {
			doc.Sample.Cut = true
		}
		if present {
			doc.Header.Names = append(doc.Header.Names, cj.Name)
		}
	}
	return doc, nil
}

func cutCell(s string, limit int, cut *bool) string {
	if len(s) <= limit {
		return s
	}
	*cut = true
	i := limit
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

func cutRecord(rec []string, columns, cellBytes int, cut *bool) []string {
	if len(rec) > columns {
		rec = rec[:columns]
		*cut = true
	}
	out := make([]string, len(rec))
	for i, c := range rec {
		out[i] = cutCell(c, cellBytes, cut)
	}
	return out
}

func printInspect(w io.Writer, doc *inspectDoc) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	line := func(k, v string) { fmt.Fprintf(tw, "%s\t%s\n", k, v) }
	line("file", doc.File)
	kind := doc.Kind
	if doc.Compression != nil {
		kind += " (" + *doc.Compression + " compressed)"
	}
	line("kind", kind)
	if doc.Encoding != nil {
		enc := doc.Encoding.Name + ", from " + doc.Encoding.Basis
		if doc.Encoding.BOM {
			enc += ", byte order mark removed"
		}
		line("encoding", enc)
	}
	if doc.Dialect != nil {
		d := doc.Dialect
		r, _ := utf8.DecodeRuneInString(d.Delimiter)
		line("delimiter", describeChar(r))
		line("quote", describeOptional(d.Quote))
		if d.Escape != nil {
			line("escape", describeOptional(d.Escape))
		}
		if d.SkipInitialSpace {
			line("spaces", "spaces after a delimiter are skipped")
		}
		line("line ending", doc.LineEnding)
		line("confidence", fmt.Sprintf("%.3f", doc.Confidence))
		for _, a := range doc.Alternatives {
			r, _ := utf8.DecodeRuneInString(a.Delimiter)
			line("alternative", fmt.Sprintf("%s, relative score %.3f", describeChar(r), a.RelativeScore))
		}
		header := "unknown"
		if doc.Header.Present != nil {
			header = map[bool]string{true: "present", false: "absent"}[*doc.Header.Present]
		}
		line("header", header)
		line("columns", fmt.Sprint(doc.ColumnCount))
	}
	if doc.Hint != "" {
		line("hint", doc.Hint)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(doc.Columns) > 0 {
		fmt.Fprintln(w)
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "#\tname\tkind")
		for _, c := range doc.Columns {
			kind := c.Kind
			if c.DecimalComma {
				kind += ", decimal comma"
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\n", c.Index, c.Name, kind)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if len(doc.Sample.Records) > 0 {
		fmt.Fprintln(w)
		for _, rec := range doc.Sample.Records {
			quoted := make([]string, len(rec))
			for i, c := range rec {
				quoted[i] = fmt.Sprintf("%q", c)
			}
			fmt.Fprintln(w, strings.Join(quoted, " "))
		}
	}
	return nil
}

func describeOptional(s *string) string {
	if s == nil {
		return "none"
	}
	r, _ := utf8.DecodeRuneInString(*s)
	return describeChar(r)
}
