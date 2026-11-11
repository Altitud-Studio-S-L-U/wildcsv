# wildcsv

**CSV parsing for Go with automatic dialect detection, encoding conversion and bounded memory.**

[![Go Reference](https://pkg.go.dev/badge/github.com/Altitud-Studio-S-L-U/wildcsv.svg)](https://pkg.go.dev/github.com/Altitud-Studio-S-L-U/wildcsv)
[![CI](https://github.com/Altitud-Studio-S-L-U/wildcsv/actions/workflows/ci.yml/badge.svg)](https://github.com/Altitud-Studio-S-L-U/wildcsv/actions/workflows/ci.yml)
[![CodeQL](https://github.com/Altitud-Studio-S-L-U/wildcsv/actions/workflows/codeql.yml/badge.svg)](https://github.com/Altitud-Studio-S-L-U/wildcsv/actions/workflows/codeql.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

wildcsv is a streaming CSV library for backend services, data imports and ETL pipelines. It reads from any `io.Reader`, detects the input format, and returns UTF-8 records with precise errors and configurable limits. It depends only on the Go standard library.

- **Automatic detection:** delimiter, quoting, encoding, header and column kinds.
- **Multiple encodings:** UTF-8, UTF-16 LE/BE and Windows-1252, with custom decoder support.
- **Controlled resource use:** limits on sample size, field size, record size and field count.
- **Parallel parsing:** large inputs are processed across goroutines while preserving record order.
- **Structured errors:** record, line, column and byte offset, with strict and lenient modes.

[Quick start](#quick-start) · [API](#api) · [Performance](#performance) · [Documentation](#documentation)

## Installation

Requires **Go 1.26 or later**.

```sh
go get github.com/Altitud-Studio-S-L-U/wildcsv
```

## Quick start

Use the library directly with a file, an upload, an HTTP body or another `io.Reader`. This example passes each record to your application and stops on the first error:

```go
package ingest

import (
	"io"

	"github.com/Altitud-Studio-S-L-U/wildcsv"
)

func ReadCSV(input io.Reader, consume func([]string) error) error {
	r, err := wildcsv.NewReader(input, nil)
	if err != nil {
		return err
	}
	defer r.Close()

	for record, err := range r.All() {
		if err != nil {
			return err
		}
		if err := consume(record); err != nil {
			return err
		}
	}
	return nil
}
```

Passing `nil` selects automatic detection, default limits and lenient parsing. Header detection is descriptive: the header is still returned as the first record. `Close` releases parser resources; the caller owns and closes the underlying input.

### Explicit configuration

Declare the dialect and encoding when your input contract is known:

```go
opts := &wildcsv.Options{
	Dialect:     &wildcsv.Dialect{Delimiter: ';', Quote: '"'},
	Encoding:    wildcsv.UTF8,
	Strict:      true,
	Concurrency: 1,
	Limits: wildcsv.Limits{
		MaxFieldBytes:  1 << 20,
		MaxRecordBytes: 4 << 20,
		MaxFields:      256,
	},
}
```

Pass `opts` as the second argument to `NewReader`. `Strict` reports quoting defects and invalid UTF-8 as errors. In lenient mode, `Options.Warn` receives accepted defects. `Concurrency: 1` keeps parsing sequential; the default `0` enables adaptive parallel parsing after 128 KiB, with up to eight parser goroutines.

## API

| API | Purpose |
| --- | --- |
| `NewReader(input, options)` | Detect the format and create a streaming reader. |
| `Detect(input, options)` | Inspect a sample without constructing a record reader; consumes bytes from the input. |
| `Reader.Read()` | Read the next record as `[]string`; returns `io.EOF` at the end. |
| `Reader.All()` | Iterate over records and errors using a Go range loop. |
| `Reader.ReadSlices()` | Read fields as `[][]byte` backed by parser buffers; consume or copy them before the next read. |
| `Reader.Detection()` | Get the detected dialect, encoding, confidence, header and column kinds. |
| `Reader.FieldPos()` / `Reader.InputOffset()` | Locate fields and track the current position. |
| `Reader.Stats()` | Inspect record counts, accepted defects and the encoding used. |
| `Reader.Close()` | Release parser resources when reading ends early. |

`*wildcsv.ParseError` carries the location and cause of a parsing problem. Applications can stop or continue reading after a parse error; underlying I/O errors end the stream. `*wildcsv.KindError` identifies input that is not delimited text.

A reader must be used by one caller at a time, including when its internal parallel parser is enabled. Separate readers are independent. See the [API reference](https://pkg.go.dev/github.com/Altitud-Studio-S-L-U/wildcsv) and [runnable examples](example_test.go).

## Supported input

| Capability | Support |
| --- | --- |
| Delimiters | Comma, semicolon, tab, pipe, colon, equals, space, `#` and `*` detection. |
| Quoting | Double quotes, single quotes or no quoting; doubled quotes and backslash escapes. |
| Encodings | UTF-8 with or without BOM, UTF-16 LE/BE, Windows-1252, and custom conversion through `Options.Decode`. |
| Metadata | Header detection and column-kind inference, including numbers, dates, booleans, URLs and identifiers. |
| File identification | Recognises spreadsheets, archives, JSON, XML, HTML, PDF, Parquet and binary data. These formats are identified, not parsed as CSV. |

Column kinds are metadata; record fields remain strings. Detection is heuristic. Applications with a fixed input contract can provide the dialect and encoding explicitly. The held-out benchmark reports a weighted F1 of **0.948** across POLLOCK, W3C-CSVW and CSV Wrangling; see the [evaluation protocol and results](docs/accuracy.md).

## Performance

Repository benchmarks on generated **32 MiB** inputs, Windows/amd64, Go 1.27.3, Intel Core i9-9900K. Median of three runs, dialect supplied, with `ReuseRecord` enabled for both libraries. The parallel columns use wildcsv's default concurrency.

| Input | `encoding/csv` | wildcsv `Read` | wildcsv `ReadSlices` | wildcsv `Read`, sequential |
| --- | ---: | ---: | ---: | ---: |
| Plain ASCII | 390 MB/s | 1,959 MB/s | 2,119 MB/s | 435 MB/s |
| Quoted fields and line breaks | 340 MB/s | 1,352 MB/s | 1,493 MB/s | 334 MB/s |
| Accented and Japanese text | 445 MB/s | 1,479 MB/s | 1,938 MB/s | 357 MB/s |

Parallel `Read` achieves **3.3–5.0×** the throughput of `encoding/csv` on these inputs, using multiple cores. These figures measure parsing with a known dialect; they do not represent automatic detection or end-to-end import throughput. Sequential parsing of the multibyte input is about 20% slower than `encoding/csv`. See [methodology, small-file results and memory measurements](docs/performance.md).

## Resource limits

| Limit | Default |
| --- | ---: |
| Detection sample | 64 KiB |
| Field size | 1 MiB |
| Record size | 4 MiB |
| Fields per record | 16,384 |

Override limits through `Options.Limits`; zero selects the default. The parser streams input without loading the entire file. Memory use depends on the configured limits and concurrency; records retained by the application add to that usage.

## Command-line tool

The optional CLI inspects, validates and converts files:

```sh
go install github.com/Altitud-Studio-S-L-U/wildcsv/cmd/wildcsv@latest

wildcsv inspect --json orders.csv
wildcsv check orders.csv
wildcsv convert --output clean.csv orders.csv
```

See the [CLI reference](docs/cli.md) for options, JSON output and exit codes.

## Documentation

- [API reference](https://pkg.go.dev/github.com/Altitud-Studio-S-L-U/wildcsv)
- [Design and parsing contract](docs/design.md)
- [Detection accuracy](docs/accuracy.md)
- [Performance benchmarks](docs/performance.md)
- [Changelog](CHANGELOG.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development and testing. CI runs tests with the race detector on Linux, macOS and Windows, plus static analysis, vulnerability checks and fuzzing.

Report vulnerabilities privately using the [security policy](SECURITY.md).

## License

[MIT](LICENSE) © Altitud Studio SLU.
