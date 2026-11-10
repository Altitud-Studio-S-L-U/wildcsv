# The wildcsv command

```text
wildcsv inspect [flags] [file]    what the file is and how to read it
wildcsv check   [flags] [file]    read all of it and list every problem
wildcsv convert [flags] [file]    rewrite it as RFC 4180 CSV in UTF-8
wildcsv version [--json]
```

With no file, or with `-`, a command reads standard input. A file compressed with gzip or bzip2 is decompressed as it is read, and `inspect` and `check` say so in `compression`.

## Flags

Every reading command takes these:

| Flag | Meaning |
| --- | --- |
| `--json` | Print exactly one JSON object on standard output, also when the command fails. |
| `--delimiter C` | Use this delimiter instead of detecting one: a single character, or `comma`, `semicolon`, `tab`, `pipe`, `colon`, `space`. |
| `--quote C` | Quote character, or `none`. Without `--delimiter` it limits detection to this quote. |
| `--escape C` | Escape character inside quoted fields, or `none`. Needs `--delimiter`. |
| `--encoding E` | `utf-8`, `utf-16le`, `utf-16be` or `windows-1252` (also `latin1`, `iso-8859-1`, `cp1252`). |
| `--skip-lines N` | Discard N lines before the table. Line numbers still count from the start of the file. |
| `--sample-bytes N` | Bytes examined by detection (65,536). |
| `--max-field-bytes N` | Bytes of one field (1,048,576). |
| `--max-record-bytes N` | Bytes of one record (4,194,304). |
| `--max-fields N` | Fields of one record (16,384). |

`inspect` adds `--header yes|no|auto` (auto), `--rows N` (5, at most 100), `--columns N` (50) and `--cell-bytes N` (64). `check` adds `--strict` and `--max-problems N` (100). `convert` adds `--output FILE` and `--crlf`.

## Exit status

| Status | Meaning |
| --- | --- |
| 0 | The command did its work and found no errors. `inspect` exits 0 for every kind it can name, including workbooks and JSON. |
| 1 | `check` found errors, or `check` or `convert` was given input that is not delimited text or that failed to parse. |
| 2 | The arguments are invalid, or the input could not be read or the output written. |

## `inspect`

Reads at most the sample and the records it shows. Text output:

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

"id" "fecha" "importe"
"1" "05/01/2026" "12,50"
"2" "06/01/2026" "7,00"
"3" "07/01/2026" "1.234,00"
```

JSON output, schema `wildcsv/inspect/v1`:

| Member | Type | Meaning |
| --- | --- | --- |
| `schema` | string | `wildcsv/inspect/v1` |
| `file` | string | The path given, or `-` |
| `kind` | string | `delimited`, `empty`, `xlsx`, `ods`, `zip`, `xls`, `gzip`, `bzip2`, `xz`, `zstd`, `pdf`, `parquet`, `binary`, `html`, `xml`, `jsonl`, `json` |
| `compression` | string or null | `gzip` or `bzip2` when the command decompressed the input |
| `encoding` | object or null | `name`, `bom` (a byte order mark was removed) and `basis` (`declared`, `custom`, `bom`, `utf16-pattern`, `ascii`, `utf8`, `fallback`) |
| `dialect` | object or null | `delimiter`, `quote` (null when unquoted), `escape` (null when none), `skip_initial_space`; null when the input is not delimited text |
| `line_ending` | string | `lf`, `crlf`, `cr`, `mixed` or `none` |
| `confidence` | number | 0 to 1, rounded to three decimals: the margin over the best dialect with another delimiter |
| `alternatives` | array | Up to three runner-up dialects, each with `relative_score` |
| `header` | object | `present`: true, false, or null when unknown; `names`: the header cells when present |
| `columns` | array | `index` (1-based), `name` when the header is present, `kind`, and `decimal_comma` when true |
| `column_count` | number | The most common number of fields in the sample |
| `sample` | object | `bytes` examined, `complete` (the sample is the whole input), `records` shown, and `cut`: true when rows, columns or cells were cut to the limits |
| `hint` | string | One sentence about the next step, or empty |

## `check`

Reads the whole input and lists every problem with its position. The text output for a file with three kinds of problem:

```text
$ wildcsv check problems.csv
file      problems.csv
kind      delimited
records   3
fields    min 3, max 4, most common 3
result    errors found
problems  ragged_record: 1
problems  stray_quote: 2
problems  unterminated_quote: 1

line 2, column 14: warning stray_quote: quote character inside a quoted field neither doubles nor closes it; it was read as data
line 2, column 17: warning stray_quote: quote character inside a quoted field neither doubles nor closes it; it was read as data
line 3, column 1: error ragged_record: record has 4 fields; the sampled records have 3
line 4, column 5: error unterminated_quote: the quoted field that starts here is not closed before the end of the input
```

JSON output, schema `wildcsv/check/v1`: `schema`, `file`, `kind`, `compression`, `encoding`, `dialect` as for `inspect`, then:

| Member | Type | Meaning |
| --- | --- | --- |
| `records` | number | Records read |
| `fields` | object | `min`, `max` and `mode` of the field counts of the whole input |
| `ok` | boolean | No problem has the severity `error` |
| `problem_counts` | object | Number of problems per code, all of them |
| `problems` | array | The first `--max-problems` problems: `code`, `severity`, `record`, `line`, `column`, `message`. A problem of the whole input has 0 for the last three. |
| `problems_cut` | boolean | More problems were counted than listed |

Problem codes:

| Code | Severity | Raised when |
| --- | --- | --- |
| `not_delimited` | error | The input is another kind. |
| `unterminated_quote` | error | A quoted field is open at the end of the input. |
| `field_too_large`, `record_too_large`, `too_many_fields` | error | A limit is exceeded; the rest of that line is skipped. |
| `ragged_record` | error | A record's field count differs from the number of columns of the sample. |
| `bare_quote`, `stray_quote` | warning; error with `--strict` | A quote that does not open, double or close a field was read as data. |
| `invalid_utf8` | warning; error with `--strict` | A sequence was replaced with U+FFFD. |
| `low_confidence` | warning | The dialect was detected with a confidence below 0.2. |

## `convert`

Writes comma-separated, double-quoted CSV in UTF-8 without a byte order mark. With `--output` it writes to a temporary file in the target's directory and renames it only after the whole input was read without error, so a failed run never leaves a partial file. On standard output it stops at the first error. With `--json` it needs `--output` and prints a `wildcsv/convert/v1` report: `file`, `output`, `encoding`, `dialect`, `records`, `ok`, and `error`, null or the first problem.

## For scripts and coding agents

The command never prompts, never opens a network connection, and writes only to standard output, standard error and the file given to `--output`. Its JSON output is bounded by the flags above, so it fits in a model's context whatever the size of the file. A team can paste this into its agent instructions:

```text
When you are handed a tabular text file you did not create, run `wildcsv inspect --json FILE` first.
If "kind" is not "delimited", do what "hint" says. Otherwise pass the reported delimiter, quote and
encoding to your parser, or run `wildcsv convert --output clean.csv FILE` and read clean.csv as
standard UTF-8 CSV. Before importing, run `wildcsv check --json FILE`: exit status 1 means the file
has errors, listed under "problems" with line and column.
```
