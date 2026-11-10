# Performance

wildcsv does more per byte than `encoding/csv`: it validates UTF-8, enforces limits on every field and record, tracks lines and columns for every field, and handles multi-byte delimiters and an escape character. This page measures what that costs, including where wildcsv is slower.

## Reading

`BenchmarkRead` reads three generated inputs of about 8 MB each with wildcsv and with `encoding/csv`, both reusing the record slice, with the dialect given so that only reading is measured:

- **plain**: unquoted ASCII fields: numbers, identifiers, dates, decimals;
- **quoted**: quoted fields with commas, line breaks and doubled quotes;
- **multibyte**: unquoted fields of Spanish, Portuguese, German and Japanese text.

```sh
go test -run '^$' -bench 'Read' -benchtime 20x -count 3 .
```

windows/amd64, Go 1.27.3, 16 logical cores. Median of three runs, time to read the whole input:

| Input | wildcsv | `encoding/csv` | wildcsv / `encoding/csv` |
| --- | ---: | ---: | ---: |
| plain | 29.1 ms | 21.3 ms | 1.37 |
| quoted | 42.2 ms | 24.4 ms | 1.73 |
| multibyte | 33.2 ms | 18.1 ms | 1.84 |

Allocations are within 2% of `encoding/csv`'s: one string per record, which the fields share, and one slice per record unless `ReuseRecord` is set.

Where the difference comes from:

- **Quoted fields** stop the scan at every quote and line break to decide between a doubled quote, a closing quote and a stray quote, and to count lines.
- **Multi-byte text** is validated as UTF-8 in bulk for each stretch of field content; `encoding/csv` does not validate what it returns.
- **Every field** checks the field and record limits and records its line and column.

If you read trusted RFC 4180 files whose dialect you know, `encoding/csv` is faster and is the better choice.

## Detection

`BenchmarkDetect` runs `Detect` with default options on the same inputs, which examines the first 64 KiB, and on a sample built to produce many candidate dialects (five delimiters on every line):

```sh
go test -run '^$' -bench 'Detect' -benchtime 20x -count 3 .
```

| Input | Median time |
| --- | ---: |
| plain | 0.25 ms |
| quoted | 0.73 ms |
| multibyte | 0.45 ms |
| many candidates | 4.33 ms |

On the 816 file runs of the accuracy benchmark ([accuracy.md](accuracy.md)), the 95th percentile of `Detect` was 6.4 ms and the slowest file took 18.8 ms, including opening the file.

## Memory

A `Reader` holds one 64 KiB read buffer, one decode buffer of 32 KiB when the input is not UTF-8, and the current record, which `MaxRecordBytes` bounds. `Detect` holds the sample, at most `SampleBytes` + 1 bytes, and one table of at most 50 records per candidate dialect while it scores them.
