# wildcsv

[![Go Reference](https://pkg.go.dev/badge/github.com/Altitud-Studio-S-L-U/wildcsv.svg)](https://pkg.go.dev/github.com/Altitud-Studio-S-L-U/wildcsv)
[![CI](https://github.com/Altitud-Studio-S-L-U/wildcsv/actions/workflows/ci.yml/badge.svg)](https://github.com/Altitud-Studio-S-L-U/wildcsv/actions/workflows/ci.yml)
[![CodeQL](https://github.com/Altitud-Studio-S-L-U/wildcsv/actions/workflows/codeql.yml/badge.svg)](https://github.com/Altitud-Studio-S-L-U/wildcsv/actions/workflows/codeql.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Read delimited text you did not produce: detect what it is, read it under explicit limits, and say exactly where it is broken.**

Go's `encoding/csv` reads RFC 4180 CSV in UTF-8 when you already know the delimiter, and the Go project has decided it will stay that way ([#3150](https://go.dev/issue/3150)). Files that arrive from customers, spreadsheets, banks and other systems rarely fit: they come with semicolons and decimal commas, in Windows-1252 or UTF-16, with a byte order mark ([#9588](https://go.dev/issue/9588)), with single quotes ([#8458](https://go.dev/issue/8458)), or with one unbalanced quote that makes `encoding/csv` hold the rest of the file in memory as a single field ([#20169](https://go.dev/issue/20169)). Sometimes the "CSV" is an Excel workbook or a JSON export.

wildcsv is a Go library and a command for that intake step. It depends only on the standard library.

- **What is this?** It recognises workbooks, archives, JSON, XML, HTML, PDF and binary data from the first bytes and names them instead of misreading them.
- **How do I read it?** It detects the encoding and the dialect (delimiter, quote, escape, padding) from a 64 KiB sample, with a confidence margin and the runner-up dialects, and guesses the header and the kind of each column.
- **Where is it broken?** It streams records holding one at a time, fails a field, a record or a field count as soon as it exceeds its limit, recovers at the next line, and reports every problem with its record, line, column and byte offset.

## Install

```sh
go get github.com/Altitud-Studio-S-L-U/wildcsv
go install github.com/Altitud-Studio-S-L-U/wildcsv/cmd/wildcsv@latest
```

The library needs Go 1.26 or later.

## Read a file in Go

```go
r, err := wildcsv.NewReader(upload, nil) // detect kind, encoding and dialect
if err != nil {
	return err // a *wildcsv.KindError for a workbook or JSON, for example
}
det := r.Detection()
log.Printf("%s, %v, confidence %.2f, header %s", det.Encoding, det.Dialect, det.Confidence, det.Header)

for rec, err := range r.All() {
	if pe, ok := errors.AsType[*wildcsv.ParseError](err); ok {
		log.Printf("line %d, column %d: %v", pe.Line, pe.Column, pe.Err)
		continue // the reader goes on with the next line
	}
	if err != nil {
		return err
	}
	process(rec) // every string is valid UTF-8
}
```

Pass `Options.Dialect` and `Options.Encoding` when you know them; nothing is then guessed. Reading is lenient by default: a quote that does not open, double or close a field is read as data, as in `"He said "hi" to me"`, and `Options.Warn` receives each such defect with its position. `Options.Strict` turns them into errors. See the [package documentation](https://pkg.go.dev/github.com/Altitud-Studio-S-L-U/wildcsv) for runnable examples.

## Inspect a file from the command line

```text
$ wildcsv inspect orders.csv
file         orders.csv
kind         delimited
encoding     utf-8, from ascii
delimiter    ; (semicolon)
quote        " (double quote)
line ending  crlf
confidence   0.778
alternative  , (comma), relative score 0.222
header       present
columns      3

#  name     kind
1  id       integer
2  fecha    date
3  importe  number, decimal comma
```

`wildcsv check` reads the whole file and lists every problem with its line and column; `wildcsv convert` rewrites the file as standard UTF-8 CSV, replacing the target only if the whole input reads without error. Each command prints one bounded JSON object with `--json` and documents its exit statuses. See [docs/cli.md](docs/cli.md).

### For scripts and coding agents

An agent that is handed a file can find out what it is before writing a parser for it. The command never prompts or opens a network connection, its JSON is versioned and bounded whatever the size of the file, and problem codes are stable. Paste this into your agent instructions:

```text
When you are handed a tabular text file you did not create, run `wildcsv inspect --json FILE` first.
If "kind" is not "delimited", do what "hint" says. Otherwise pass the reported delimiter, quote and
encoding to your parser, or run `wildcsv convert --output clean.csv FILE` and read clean.csv as
standard UTF-8 CSV. Before importing, run `wildcsv check --json FILE`: exit status 1 means the file
has errors, listed under "problems" with line and column.
```

## What it detects

| | |
| --- | --- |
| Kinds | delimited text, empty, `xlsx`, `ods`, `xls`, `zip`, `gzip`, `bzip2`, `xz`, `zstd`, `pdf`, `parquet`, `json`, `jsonl`, `html`, `xml`, binary |
| Encodings | UTF-8 (with or without a byte order mark), UTF-16 LE and BE (with a mark or by their zero bytes), Windows-1252; any other through `Options.Decode`, such as a decoder from `golang.org/x/text` |
| Dialects | comma, semicolon, tab, vertical bar, colon, equals sign, space, number sign and asterisk; double quote, apostrophe or none; backslash escapes; spaces after delimiters |
| Columns | header present, absent or unknown; per column: integer, number (with decimal comma), percent, currency, date, time, date-time, boolean, URL, e-mail, IPv4, UUID, path, text |

When a large file's sample is plain ASCII, the choice between UTF-8 and Windows-1252 waits for the first byte that is not, so an export whose first `é` sits a megabyte in is still read correctly.

## Limits

| Limit | Default | Meaning |
| --- | ---: | --- |
| `SampleBytes` | 65,536 | Bytes examined by detection |
| `MaxFieldBytes` | 1,048,576 | Bytes of one field |
| `MaxRecordBytes` | 4,194,304 | Bytes of one record, with its quotes and delimiters |
| `MaxFields` | 16,384 | Fields in one record |

A reader holds one record and fixed buffers, so its memory is bounded by `MaxRecordBytes` plus a constant whatever the input.

## How accurate detection is

Detection follows the table uniformity method of García (2024), measured on the 816 annotated runs of the POLLOCK, W3C CSV on the Web and CSV Wrangling benchmark sets that other tools publish results for. The method was tuned on half of the files; the other half was run once, afterwards:

| | POLLOCK | W3C-CSVW | CSV Wrangling | Weighted F1 |
| --- | ---: | ---: | ---: | ---: |
| Held-out half, accuracy | 98.5% | 92.2% | 86.5% | 0.937 |
| All files, accuracy | 98.0% | 92.8% | 89.9% | 0.948 |

The best published figure of the Python reference implementation is a weighted F1 of 0.938, on all files. Detection takes a few milliseconds per file. The method, the protocol, the comparison with other tools and the kinds of file that still fail are in [docs/accuracy.md](docs/accuracy.md); the costs of reading against `encoding/csv` are in [docs/performance.md](docs/performance.md).

## When to use encoding/csv instead

If you produce the files yourself, or they are trusted RFC 4180 in UTF-8 with a known delimiter, use `encoding/csv`: it is faster (wildcsv takes 1.5 to 1.8 times as long to read) and part of the standard library. wildcsv is for the files you cannot vouch for.

## Documentation

- [docs/design.md](docs/design.md): the contract: kinds, encodings, dialects, detection, reading rules, errors, API and command
- [docs/cli.md](docs/cli.md): the command, its JSON and its exit statuses
- [docs/accuracy.md](docs/accuracy.md): how detection is measured, and the results
- [docs/performance.md](docs/performance.md): reading and detection costs

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE) © Altitud Studio SLU
