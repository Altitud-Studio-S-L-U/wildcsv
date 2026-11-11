# Changelog

All notable changes are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.1.0] - 2026-11-11

### Added

- `Detect`: the kind of an input (delimited text, empty, workbooks, archives, compressed files, PDF, Parquet, JSON, JSON Lines, HTML, XML, binary), its encoding and the basis of that decision, its dialect with a confidence margin and up to three runner-up dialects, its line ending, a header guess and the kind of each column, from a sample of at most `Limits.SampleBytes` + 1 bytes.
- `NewReader` and `Reader`: streaming records of UTF-8, UTF-16 and Windows-1252 text, or of any encoding through `Options.Decode`, under limits on field size, record size and field count, with every problem reported as a `*ParseError` with its record, line, column and byte offset, and recovery at the next line.
- Reading eight bytes at a time for records that lie wholly in the buffer, with the general path, which defines every result, taking any record the fast path does not handle.
- `Reader.ReadSlices`, which returns the fields without copying them and allocates nothing once warmed up.
- Parallel reading behind the same API: `Options.Concurrency` sets the goroutines, and its default, 0, hands over to min(GOMAXPROCS, 8) goroutines once an input passes its first 128 KiB. Results are those of one goroutine. `Reader.Close` and `ErrClosed` stop a parallel reader early.
- Lenient reading of quotes that neither open, double nor close a field, reported through `Options.Warn` and `Stats`; `Options.Strict` turns them and invalid UTF-8 into errors.
- The `wildcsv` command with `inspect`, `check` and `convert`, versioned JSON output and documented exit statuses.
- Detection lets the table of one column compete in samples of fewer than five line feeds, and ignores a delimiter that only ever starts lines.
- `docs/design.md`, the contract; `docs/accuracy.md`, the measurement of dialect detection on the POLLOCK, W3C CSV on the Web and CSV Wrangling sets with a held-out half; `docs/performance.md`; `docs/cli.md`.

[Unreleased]: https://github.com/Altitud-Studio-S-L-U/wildcsv/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Altitud-Studio-S-L-U/wildcsv/releases/tag/v0.1.0
