package main

import (
	"bufio"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Altitud-Studio-S-L-U/wildcsv"
)

type convertDoc struct {
	Schema   string        `json:"schema"`
	File     string        `json:"file"`
	Output   string        `json:"output"`
	Encoding *encodingJSON `json:"encoding"`
	Dialect  *dialectJSON  `json:"dialect"`
	Records  int64         `json:"records"`
	OK       bool          `json:"ok"`
	Error    *problem      `json:"error"`
}

func (e *env) convert(args []string) int {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	var c common
	c.register(fs)
	output := fs.String("output", "", "write here instead of standard output; replaced only if the whole input reads without error")
	crlf := fs.Bool("crlf", false, "end records with a carriage return and a line feed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if c.json && *output == "" {
		return e.fail(true, usageErr("--json needs --output: the converted records and the report cannot share standard output"))
	}
	doc, err := e.convertTo(fs, &c, *output, *crlf)
	if err != nil {
		return e.fail(c.json, err)
	}
	if c.json {
		if err := writeJSON(e.stdout, doc); err != nil {
			return e.fail(false, ioErr(err))
		}
	} else if doc.Error != nil {
		fmt.Fprintf(e.stderr, "wildcsv: line %d, column %d: %s: %s\n", doc.Error.Line, doc.Error.Column, doc.Error.Code, doc.Error.Message)
	}
	if !doc.OK {
		return 1
	}
	return 0
}

func (e *env) convertTo(fs *flag.FlagSet, c *common, output string, crlf bool) (*convertDoc, error) {
	opts, err := c.options()
	if err != nil {
		return nil, err
	}
	in, err := e.open(fs)
	if err != nil {
		return nil, err
	}
	defer in.close()
	doc := &convertDoc{Schema: "wildcsv/convert/v1", File: in.name, Output: output, OK: true}
	if output == "" {
		doc.Output = "-"
	}
	r, err := wildcsv.NewReader(in.r, opts)
	var ke *wildcsv.KindError
	if errors.As(err, &ke) {
		doc.OK = false
		doc.Error = &problem{Code: "not_delimited", Severity: "error", Message: fmt.Sprintf("the input is %s, not delimited text", ke.Kind)}
		return doc, nil
	}
	if err != nil {
		return nil, readError(in, err)
	}
	det := r.Detection()
	doc.Encoding = toEncodingJSON(det)
	dj := toDialectJSON(det.Dialect)
	doc.Dialect = &dj

	var dst io.Writer = e.stdout
	var tmp *os.File
	if output != "" {
		tmp, err = os.CreateTemp(filepath.Dir(output), "."+filepath.Base(output)+".*.tmp")
		if err != nil {
			return nil, ioErr(err)
		}
		defer func() {
			if tmp != nil {
				tmp.Close()
				os.Remove(tmp.Name())
			}
		}()
		dst = tmp
	}
	bw := bufio.NewWriterSize(dst, 64<<10)
	w := csv.NewWriter(bw)
	w.UseCRLF = crlf
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		var pe *wildcsv.ParseError
		if errors.As(err, &pe) {
			code := problemCodes[pe.Err]
			doc.OK = false
			doc.Error = &problem{Code: code, Severity: "error", Record: pe.Record, Line: pe.Line, Column: pe.Column, Message: problemMessages[code]}
			break
		}
		if err != nil {
			return nil, readError(in, err)
		}
		if err := w.Write(rec); err != nil {
			return nil, ioErr(err)
		}
	}
	doc.Records = r.Stats().Records
	if doc.Encoding != nil && r.Stats().Encoding != "" {
		doc.Encoding.Name = string(r.Stats().Encoding)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, ioErr(err)
	}
	if err := bw.Flush(); err != nil {
		return nil, ioErr(err)
	}
	if tmp == nil || !doc.OK {
		return doc, nil
	}
	if err := tmp.Close(); err != nil {
		return nil, ioErr(err)
	}
	if err := os.Rename(tmp.Name(), output); err != nil {
		return nil, ioErr(err)
	}
	tmp = nil
	return doc, nil
}
