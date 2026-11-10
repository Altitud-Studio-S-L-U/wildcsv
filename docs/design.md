# Design

wildcsv reads delimited text that the calling program did not produce: a customer upload, a spreadsheet export, a bank statement, a file handed to a coding agent. This document is the contract. The code, the tests and the command-line output follow it; when they disagree, one of them has a bug.

## 1. Purpose

Go's `encoding/csv` reads RFC 4180 CSV in UTF-8 when the caller already knows the delimiter. By decision of the Go project it stays there: the package "is going to focus only on RFC 4180" ([#3150](https://go.dev/issue/3150)). It does not remove a byte order mark ([#9588](https://go.dev/issue/9588)), its quote character cannot be changed ([#8458](https://go.dev/issue/8458)), and a field has no size limit, so one unbalanced quote can make the reader hold the rest of the file in memory ([#20169](https://go.dev/issue/20169), a proposal on hold).

Files that arrive from outside rarely meet those conditions. wildcsv answers three questions about bytes of unknown origin:

1. Is this delimited text at all, or a workbook, an archive, JSON, a PDF?
2. Which encoding and which dialect does it use, and how sure is that answer?
3. What are its records, and where exactly is each problem?

Three rules hold for every answer:

- **Bounded.** Detection examines a sample of fixed size. Reading holds at most one record, and every field and record has a size limit. No input makes memory grow without limit.
- **Located.** Every error carries the record number, the line, the column and the byte offset.
- **Deterministic.** The same bytes and options give the same answer. There is no randomness, no clock and no network.

## 2. Scope

In scope for 0.1:

- Recognising what a file is from its first bytes (section 4).
- Detecting UTF-8, UTF-16 and Windows-1252, with a hook for any other encoding (section 5).
- Detecting the dialect: delimiter, quote character, escape character and whether spaces after a delimiter are padding (sections 6 and 7).
- Guessing whether the first record is a header and what kind of value each column holds (section 7).
- Reading records as a stream under limits, leniently or strictly (section 8).
- A command, `wildcsv`, that reports all of the above as text or as JSON, checks a whole file and rewrites it as standard CSV (section 10).

Out of scope for 0.1:

- Writing delimited text. `encoding/csv`'s writer does it well, and `wildcsv convert` uses it.
- Mapping records to structs and converting cells to Go values. wildcsv reports the kind of each column; it does not convert.
- Reading workbooks, JSON, fixed-width text or files with several tables. wildcsv recognises the first three and says so.
- Delimiters longer than one character.
- Detecting comment lines or lines of preamble before the table. The caller can skip a number of lines.

## 3. Pipeline

```text
bytes ──> kind ──> encoding ──> UTF-8 text ──> dialect ──> records
          §4       §5                          §7          §8
```

`Detect` runs the first four stages on a sample and returns a report. `NewReader` runs them on the same sample and then streams the records of the whole input. Both read the sample once; nothing is read twice and the input does not need to be seekable.

## 4. Kinds

The kind says what the input looks like. Only `delimited` and `empty` inputs can be read; for every other kind `NewReader` returns a `*KindError` and `Detect` returns a report that names the kind.

Checks run in this order on the raw sample, and the first match wins.

| Kind | Recognised by |
| --- | --- |
| `empty` | No bytes. |
| `xlsx` | ZIP local file header (`PK\x03\x04`) and an entry name in the sample that starts with `xl/`. |
| `ods` | ZIP local file header whose first entry is `mimetype` with the content `application/vnd.oasis.opendocument.spreadsheet`. |
| `zip` | Any other ZIP signature (`PK\x03\x04`, `PK\x05\x06`, `PK\x07\x08`). |
| `xls` | OLE2 compound file signature `D0 CF 11 E0 A1 B1 1A E1`. |
| `gzip` | `1F 8B`. |
| `bzip2` | `BZh` followed by a digit from 1 to 9. |
| `xz` | `FD 37 7A 58 5A 00`. |
| `zstd` | `28 B5 2F FD`. |
| `pdf` | `%PDF-`. |
| `parquet` | `PAR1`. |
| `binary` | After the encoding is decided (section 5): a NUL character, or more than 1% of characters that are C0 controls other than tab, line feed, carriage return, form feed and substitute (`0x1A`). |
| `html` | The text starts, after white space, with `<!doctype html`, `<html`, `<head`, `<body` or `<table`, in any letter case. |
| `xml` | The text starts, after white space, with `<?xml`, or with `<`, a tag name and then a space, `/` or `>`. |
| `jsonl` | The text starts, after white space, with `{` or `[`, has at least two non-empty lines, and every complete non-empty line is a valid JSON object or array. |
| `json` | The text starts, after white space, with `{` or `[`, and either the whole sample is valid JSON, or the sample is incomplete and tokenises as JSON up to its end. |
| `delimited` | Everything else. |

A CSV file whose first cell begins with a bracket, such as a header of `[id],[name]`, is not valid JSON and therefore stays `delimited`.

## 5. Encoding

All detection and reading work on UTF-8 text. The encoding is decided on the sample, in this order:

1. **Declared.** `Options.Decode` or `Options.Encoding` is set: wildcsv does not guess.
2. **Byte order mark.** `EF BB BF` is UTF-8; `FF FE` is UTF-16 little endian; `FE FF` is UTF-16 big endian. The mark is removed. `FF FE 00 00` and `00 00 FE FF` are UTF-32, which wildcsv does not read: it returns an `*EncodingError`.
3. **UTF-16 without a mark.** Among the first 4096 bytes, at least 40% of the bytes at even offsets are zero and at most 5% of those at odd offsets are, or the reverse. Zeros at odd offsets mean little endian. Text outside the Latin range has too few zero bytes for this test and needs a mark or a declaration.
4. **ASCII.** Every byte is below `0x80`. The file may be UTF-8 or Windows-1252; the sample cannot tell. See the late decision below.
5. **UTF-8.** The sample is valid UTF-8, ignoring a sequence cut by the end of an incomplete sample.
6. **Windows-1252.** Anything else. Bytes map as in the [WHATWG Encoding Standard](https://encoding.spec.whatwg.org/#windows-1252), so the five bytes Windows-1252 leaves undefined become the C1 control with the same value. The labels `latin1` and `iso-8859-1` select this encoding, as they do in browsers.

The report states the basis of the decision (`declared`, `custom`, `bom`, `utf16-pattern`, `ascii`, `utf8`, `fallback`), because a guess from a fallback deserves less trust than a byte order mark.

**Late decision.** When the sample is pure ASCII, a large file may still hold `é` as the single byte `E9` a megabyte later. The reader therefore decides at the first byte that is not ASCII: if that byte starts a valid UTF-8 sequence, the input is UTF-8 from there on; otherwise the input is Windows-1252 from there on. Everything before that byte reads the same in both encodings, so the result equals decoding the whole file with the chosen encoding. The decision uses only that first sequence, so it does not depend on how the input happens to be split into reads. `Reader.Stats` reports the encoding finally used.

**Guarantee.** Every string wildcsv returns is valid UTF-8. In UTF-8 input, an invalid sequence becomes U+FFFD and is counted, or is an error in strict mode. In UTF-16 input, an unpaired surrogate or a trailing odd byte becomes U+FFFD and is counted.

**Other encodings.** `Options.Decode` takes any function that turns the source into UTF-8, such as a decoder from `golang.org/x/text`. wildcsv itself depends only on the standard library.

## 6. Dialect

```go
type Dialect struct {
	Delimiter        rune // separates fields
	Quote            rune // surrounds a field that contains delimiters or line breaks; 0 when fields are never quoted
	Escape           rune // inside a quoted field, makes the next quote or escape character literal; 0 when unused
	SkipInitialSpace bool // spaces after a delimiter are padding, not data
}
```

- `Delimiter` is any code point except carriage return, line feed, U+FFFD and the quote or escape character. The same exclusions apply to `Quote` and `Escape`.
- A quote written twice inside a quoted field always stands for one quote. `Escape` adds a second way to write it, so an escape equal to the quote means the same as no escape and is read as such. An escape without a quote is not a valid dialect.
- `SkipInitialSpace` cannot be combined with the space as delimiter.
- Records end at a line feed, at a carriage return followed by a line feed, or at a lone carriage return. All three are accepted in the same input. The report states which occur in the sample: `lf`, `crlf`, `cr`, `mixed`, or `none` for a single line.
- A line with no characters is not a record. A line of spaces is a record with one field.

## 7. Detection

### 7.1 Sample

Detection reads at most `Limits.SampleBytes` bytes (64 KiB by default) and decodes them. Each candidate dialect reads the sample leniently, with the rules of section 8. If the input continues beyond the sample, the last, possibly incomplete record is dropped under each candidate. A record that a candidate cannot read, because of an unterminated quote or a limit, is left out of that candidate's table, and a candidate with no records scores zero. At most 50 records of the sample are scored.

### 7.2 Candidates

A candidate is a `Dialect`. Candidates are built from characters that occur in the sample:

- **Delimiters:** comma, semicolon, tab, vertical bar, colon, equals sign, space, number sign and asterisk. These nine are the candidates the published benchmarks use (section 11), which keeps results comparable. `Options.Delimiters` replaces the list.
- **Quotes:** the double quote always; the apostrophe if it occurs; no quoting if a double quote occurs. `Options.Quotes` replaces the list.
- **Escape:** none always; the backslash if a backslash occurs directly before a candidate quote.
- **SkipInitialSpace:** false always; true if a delimiter is followed by spaces and then a candidate quote.

Candidates are generated in a fixed order: delimiters in the order above, then double quote, no quote, apostrophe, then no escape before backslash, then `SkipInitialSpace` false before true. Between candidates with the same score, the one whose reading of the sample has fewer bare and stray quotes wins, and if they are still equal the earlier one in that order does. So a file whose double quotes are only inch marks is reported as unquoted, and a file that writes `\"` inside quoted fields is reported with the backslash as its escape.

If no candidate delimiter occurs in the sample, the input has one column and the dialect is the default: the first candidate delimiter, which is the comma unless `Options.Delimiters` says otherwise, and the double quote.

### 7.3 Cell kinds

Every cell of a candidate's table is classified after trimming spaces. The first match wins.

| Kind | Matches |
| --- | --- |
| `empty` | Nothing. |
| `null` | `NA`, `N/A`, `NaN`, `NULL`, `nil`, `none`, `-`, in any letter case. |
| `boolean` | `true`, `false`, `yes`, `no`, in any letter case. |
| `integer` | An optional sign and digits. |
| `number` | A decimal with a point or a comma, optionally with an exponent; or digits in groups of three separated by commas, points, spaces or apostrophes, with an optional decimal part that uses the other separator. |
| `percent` | A number or integer followed by `%`, with an optional space between. |
| `currency` | A number or integer with a currency symbol (Unicode category Sc) before or after it, an optional space between them and an optional sign in front. |
| `date` | Year, month and day in any of the orders Y-M-D, D-M-Y and M-D-Y, with the same separator twice (`-`, `/` or `.`), a year of four digits, or of two when it comes last, and a month and day in range. |
| `time` | `H:MM` or `HH:MM`, optionally with seconds, a fraction, `AM` or `PM`, and a zone (`Z`, `+HH:MM`, `+HHMM`). |
| `datetime` | A date and a time separated by a space or `T`. |
| `url` | `http://`, `https://` or `ftp://` followed by characters without spaces, or the same starting with `www.`. |
| `email` | One `@` with a local part before it and a domain with a dot after it, without spaces. |
| `ipv4` | Four numbers from 0 to 255 separated by points. |
| `uuid` | 32 hexadecimal digits in groups of 8, 4, 4, 4 and 12 separated by hyphens. |
| `path` | A Unix path: an optional `~` or `.`, then one or more `/name` parts. |
| `text` | Letters, marks, digits, spaces and `_ - . ( ) ? !`, with at least one letter or digit. A cell that was quoted may also contain commas. |
| `other` | Anything else. |

A cell longer than 256 bytes is `other`. A cell is *typed* when its kind is not `other`. The point of the classification is that the wrong delimiter leaves cells that look like nothing: `7;45;3.2` read as one cell is `other`, read with the semicolon it is three numbers.

### 7.4 Score

Each candidate parses the sample into a table of *n* records, record *i* having *kᵢ* fields. The score follows the table uniformity method of García [1], computed on wildcsv's own parser and cell kinds. It multiplies how typed the cells are by how regular the table is.

- **Weight.** A typed cell weighs 100, any other cell 0.1. λ is the sum of all weights divided by the largest *kᵢ*.
- **Consistency.** τ₀ = 1 / (1 + 2·MAD), where MAD is 1.4826 times the median of the distances of each *kᵢ* from their median. A table whose records all have the same number of fields has τ₀ = 1.
- **Dispersion.** *r* is the range of the *kᵢ*; α is the number of records whose field count differs from the previous record's; *s* is the length of the longest run of consecutive records with the same field count; *H* is the Shannon entropy, in bits, of the field counts. If α = 0, τ₁ = 0; otherwise τ₁ = 2·*r*·(α² + 1)·(1 − *s*/*n*)/*s*·(1 + *H*).
- **Column coherence.** *cⱼ* is the sum of the weights in column *j* divided by *n*, and *dⱼ* its distance from the mean of all *cⱼ*. τ₂ is the mean of 1 − *dⱼ*/max(*d*), or 1 when every *dⱼ* is zero.
- **Shape entropy.** τ₃ is the Shannon entropy, in bits, of the records' shapes, a shape being the list of the lengths of a record's cells.

score = (τ₀/δ + 1/(τ₁ + *n*) + τ₂/δ + 1/(τ₃ + *n*)) · λ, with δ = 50, the largest number of records scored.

The candidate with the highest score wins. Changes to this method, with the measured effect of each, are recorded in [accuracy.md](accuracy.md).

### 7.5 Confidence

`Confidence` is 1 − *s₂*/*s₁*, where *s₁* is the winning score and *s₂* the best score of a candidate with a different delimiter, or 0 if there is none. It is a margin, not a probability: [accuracy.md](accuracy.md) reports how often detection was right at each level of confidence. The report also lists up to three runner-up dialects with their score relative to the winner, so a program can offer the user a choice when the margin is small.

### 7.6 Header and columns

The header guess has three values, because a table made only of text gives no evidence either way.

- **Present:** the sample has at least two records, no cell of the first record is empty, and some column whose other cells are at least 80% of one kind among `integer`, `number`, `percent`, `currency`, `date`, `time`, `datetime` and `boolean` has a first cell that is `text`. For this test an `integer` cell also counts as a `number`.
- **Absent:** some column passes that 80% test and the first record's cell has the same kind, and no column qualifies for *present*.
- **Unknown:** every other case, including a single record.

The reader never drops the first record on its own. The guess is advice, and the caller decides.

The table has as many columns as the most common field count among the sampled records. A column's kind is the one shared by at least 90% of its cells that are neither `empty` nor `null`, not counting the first record when the header is present; `integer` cells count toward a `number` column. With no such kind the column is `text`; with no cells it is `empty`. A column of numbers, percentages or amounts also reports whether its decimal separator is a comma.

### 7.7 Cost

Detection parses the sample once per candidate. With the default lists there are at most 9 × 3 × 2 × 2 = 108 candidates, and usually fewer than 20, so the work is bounded by a constant times `SampleBytes`.

## 8. Reading

### 8.1 Records

The reader takes UTF-8 text and a dialect and yields one record at a time.

1. At the start of a field, if `SkipInitialSpace` is set, spaces are skipped.
2. If the next character is the quote, the field is quoted; otherwise it is unquoted.
3. An **unquoted field** runs to the next delimiter, record end or end of input, and is taken as it is. A quote character in it is data; this is a *bare quote*.
4. A **quoted field** starts after its opening quote. Inside it:
   - the escape character followed by a quote or by itself yields that second character; followed by anything else it is data, so `C:\dir` survives;
   - a quote followed by a quote yields one quote;
   - a quote followed, after optional spaces, by a delimiter, a record end or the end of input closes the field, and those spaces are dropped;
   - any other quote is data; this is a *stray quote*;
   - delimiters and line breaks are data, kept byte for byte.
5. After a field, a delimiter starts the next field and a record end completes the record. A delimiter right before a record end gives a final empty field.

A stray quote is the common defect of writers that wrap a value in quotes without doubling the quotes inside it, as in `"He said "hi" to me",x`. Rule 4 recovers that value whole.

### 8.2 Lenient and strict

By default the reader is lenient: bare quotes and stray quotes are data, and an invalid UTF-8 sequence becomes U+FFFD. Lenient does not mean silent: `Stats` counts each of these defects, and `Options.Warn`, when set, receives every one with its position while the records keep coming. With `Options.Strict` each of them is an error instead, and spaces between a closing quote and the delimiter are an error unless `SkipInitialSpace` is set.

One defect is an error in both modes: a quoted field that is still open at the end of the input. Nothing after its opening quote can be trusted, and returning it would merge every following record into one cell.

### 8.3 Limits

| Limit | Default | Meaning |
| --- | --- | --- |
| `SampleBytes` | 65,536 | Bytes examined by detection. |
| `MaxFieldBytes` | 1,048,576 | Bytes of one field's content. |
| `MaxRecordBytes` | 4,194,304 | Bytes of one record in the input, with its quotes and delimiters. |
| `MaxFields` | 16,384 | Fields in one record. This is the number of columns of an Excel worksheet. |

A zero value selects the default. A negative value, or a field limit above the record limit, is rejected. The reader holds one record and one read buffer, so its memory is bounded by `MaxRecordBytes` plus a constant.

An unbalanced quote in a large file therefore fails after `MaxFieldBytes` with an error that points at the opening quote, instead of reading the rest of the file into one field.

### 8.4 Errors and positions

```go
type ParseError struct {
	Record int   // 1-based number of the record being read
	Line   int   // 1-based line of the position
	Column int   // 1-based byte index in that line
	Offset int64 // 0-based byte offset in the text
	Quoted bool  // the position is inside a quoted field, or is its opening quote
	Err    error // one of the errors below
}
```

Lines count every line break, including those inside quoted fields and blank lines. Columns and offsets count bytes of the UTF-8 text after decoding and after the byte order mark is removed; for a UTF-8 file without a mark they are positions in the file itself.

| Error | Position | Modes |
| --- | --- | --- |
| `ErrBareQuote` | The quote. | Strict |
| `ErrStrayQuote` | The quote. | Strict |
| `ErrInvalidUTF8` | The first invalid byte. | Strict |
| `ErrUnterminatedQuote` | The opening quote. | Both |
| `ErrFieldTooLarge` | The start of the field; for a quoted field, its opening quote. | Both |
| `ErrRecordTooLarge` | The start of the record. | Both |
| `ErrTooManyFields` | The start of the first field over the limit. | Both |

### 8.5 Recovery

After an error the reader discards input up to the next line break, without interpreting quotes, and the next call continues from there. A program can therefore list every problem of a file in one pass. When the error fell inside a quoted field that spans several lines, the lines that follow may yield further errors or misaligned records until the quoting lines up again.

## 9. Go API

```go
package wildcsv

func Detect(r io.Reader, opts *Options) (*Detection, error)
func NewReader(r io.Reader, opts *Options) (*Reader, error)

func (r *Reader) Detection() *Detection
func (r *Reader) Read() ([]string, error)
func (r *Reader) All() iter.Seq2[[]string, error]
func (r *Reader) FieldPos(field int) (line, column int)
func (r *Reader) InputOffset() int64
func (r *Reader) Stats() Stats
```

```go
type Options struct {
	Dialect     *Dialect                  // use this dialect; do not detect one
	Delimiters  []rune                    // candidate delimiters for detection
	Quotes      []rune                    // candidate quotes for detection; 0 stands for no quoting
	Encoding    Encoding                  // use this encoding; do not detect one
	Decode      func(io.Reader) io.Reader // convert the source to UTF-8; wildcsv then does no decoding of its own
	SkipLines   int                       // lines to discard before the table
	Strict      bool                      // report quoting defects and invalid UTF-8 as errors
	Warn        func(*ParseError)         // called for each defect that lenient reading accepts
	ReuseRecord bool                      // Read may return a slice that shares its backing array with the previous one
	Limits      Limits
}

type Detection struct {
	Kind          Kind
	Encoding      Encoding
	EncodingBasis string
	BOM           bool
	Dialect       Dialect
	LineEnding    string
	Confidence    float64
	Alternatives  []Alternative
	Header        HeaderGuess
	Columns       []Column
	SampleBytes   int
	SampleRecords int
	Complete      bool // the sample is the whole input
}

type Stats struct {
	Records     int64
	BareQuotes  int64
	StrayQuotes int64
	InvalidUTF8 int64
	Encoding    Encoding
}

type Alternative struct {
	Dialect       Dialect
	RelativeScore float64 // its score divided by the winner's
}

type Column struct {
	Name         string    // the first record's cell, when the header is present
	Kind         ValueKind // a kind of section 7.3
	DecimalComma bool
}

type Limits struct {
	SampleBytes    int
	MaxFieldBytes  int
	MaxRecordBytes int
	MaxFields      int
}
```

`Kind`, `Encoding` and `ValueKind` are string types whose constants spell the names used in this document (`KindDelimited` is `"delimited"`, `UTF16LE` is `"utf-16le"`, `ValueInteger` is `"integer"`), so reports print and marshal without a lookup table. `HeaderGuess` is `HeaderUnknown`, `HeaderPresent` or `HeaderAbsent`.

Errors are compared with `errors.Is` and `errors.As`:

| Error | Returned when |
| --- | --- |
| `*ParseError` | The data has a problem (section 8.4). It wraps one of `ErrBareQuote`, `ErrStrayQuote`, `ErrInvalidUTF8`, `ErrUnterminatedQuote`, `ErrFieldTooLarge`, `ErrRecordTooLarge`, `ErrTooManyFields`. |
| `*KindError` | The input is not delimited text. It names the kind and wraps `ErrNotDelimited`. |
| `*EncodingError` | The encoding is recognised but not read. It names the encoding and wraps `ErrUnsupportedEncoding`. |
| `ErrInvalidOptions` | A limit is negative, the field limit exceeds the record limit, or the dialect is not valid. The returned error wraps it and says which. |

- A nil `*Options` and a zero `Options` both mean: detect everything, read leniently, default limits.
- `Detect` reads at most `SampleBytes` + 1 bytes. It returns a report for every kind, and an error only when reading fails, the options are invalid or the encoding is UTF-32.
- With `Options.Dialect` set, `Detect` still reports the kind, the encoding, the header and the columns; the confidence is 1 and there are no alternatives.
- With `Options.Decode` set, the report names the encoding `custom` (`CustomDecoding`) with the basis `custom`.
- `SkipLines` discards lines before the table. Detection skips them within the sample. Positions still count from the first line of the input.
- `NewReader` returns a `*KindError`, which wraps `ErrNotDelimited`, for any kind other than `delimited` and `empty`, unless `Options.Dialect` is set: a caller who declares the dialect declares the input to be delimited text, and `Detection().Kind` still says what the sample looked like. An empty input gives a reader whose first `Read` returns `io.EOF`.
- `Read` returns `io.EOF` at the end of the input and a `*ParseError` for a problem in the data; after a `*ParseError` it can be called again. Errors from the underlying reader are returned as they are.
- `All` yields each record or error in turn and stops at the end of the input or at an error that is not a `*ParseError`.
- `FieldPos` gives the position of a field of the record last returned. For a quoted field that is its opening quote.
- A `Reader` is not safe for concurrent use. Readers do not share state.

## 10. Command line

```text
wildcsv inspect [flags] [file]    what the file is and how to read it
wildcsv check   [flags] [file]    read all of it and list every problem
wildcsv convert [flags] [file]    rewrite it as RFC 4180 CSV in UTF-8
wildcsv version
```

With no file, or with `-`, the command reads standard input. A gzip or bzip2 file is decompressed as it is read, and the report says so.

Flags common to the three commands: `--json`, `--delimiter`, `--quote`, `--escape`, `--encoding`, `--skip-lines`, `--sample-bytes`, `--max-field-bytes`, `--max-record-bytes`, `--max-fields`. `inspect` adds `--header yes|no|auto`, `--rows` (5; at most 100), `--columns` (50) and `--cell-bytes` (64). `check` adds `--strict` and `--max-problems` (100). `convert` adds `--output` and `--crlf`.

### 10.1 Contract for programs and agents

- With `--json`, standard output holds exactly one JSON object, also when the command fails. Its `schema` member names the shape and its version, such as `wildcsv/inspect/v1`.
- Output is bounded. `inspect` prints at most `--rows` records of at most `--columns` cells, each cut to `--cell-bytes`; `check` prints at most `--max-problems` problems and always the total per code. A flag in the object says when something was cut.
- Problems have a stable `code`, a `severity`, the record, line and column, and a message in plain words.
- The command never prompts, never opens a network connection and never writes outside the file given to `--output`.

| Exit status | Meaning |
| --- | --- |
| 0 | The command did its work and found no errors. `inspect` exits 0 for every kind it can name. |
| 1 | `check` found errors, or `check` or `convert` was given input that is not delimited text or that failed to parse. |
| 2 | The arguments are invalid, or the input could not be read or the output written. |

### 10.2 `inspect`

```json
{
  "schema": "wildcsv/inspect/v1",
  "file": "orders.csv",
  "kind": "delimited",
  "compression": null,
  "encoding": { "name": "windows-1252", "bom": false, "basis": "fallback" },
  "dialect": { "delimiter": ";", "quote": "\"", "escape": null, "skip_initial_space": false },
  "line_ending": "crlf",
  "confidence": 0.93,
  "alternatives": [{ "delimiter": ",", "quote": "\"", "escape": null, "skip_initial_space": false, "relative_score": 0.07 }],
  "header": { "present": true, "names": ["id", "fecha", "importe"] },
  "columns": [
    { "index": 1, "name": "id", "kind": "integer" },
    { "index": 2, "name": "fecha", "kind": "date" },
    { "index": 3, "name": "importe", "kind": "number", "decimal_comma": true }
  ],
  "column_count": 3,
  "sample": {
    "bytes": 65536,
    "complete": false,
    "records": [["1", "05/01/2026", "12,50"]],
    "cut": false
  },
  "hint": ""
}
```

`header.present` is `true`, `false` or `null` for unknown. `hint` is one sentence for a person or an agent about the next step; for an `xlsx` input it says that the file is an Excel workbook and must be exported as CSV or read with a spreadsheet library.

### 10.3 `check`

```json
{
  "schema": "wildcsv/check/v1",
  "file": "orders.csv",
  "kind": "delimited",
  "encoding": { "name": "utf-8", "bom": true, "basis": "bom" },
  "dialect": { "delimiter": ",", "quote": "\"", "escape": null, "skip_initial_space": false },
  "records": 120034,
  "fields": { "min": 7, "max": 8, "mode": 7 },
  "ok": false,
  "problem_counts": { "ragged_record": 3, "bare_quote": 12 },
  "problems": [
    {
      "code": "ragged_record",
      "severity": "error",
      "record": 1200,
      "line": 1201,
      "column": 1,
      "message": "record has 8 fields; the sampled records have 7"
    }
  ],
  "problems_cut": false
}
```

| Code | Severity | Raised when |
| --- | --- | --- |
| `not_delimited` | error | The input is another kind. |
| `unterminated_quote` | error | A quoted field is open at the end of the input. |
| `field_too_large`, `record_too_large`, `too_many_fields` | error | A limit is exceeded. |
| `ragged_record` | error | A record's field count differs from the number of columns, which is the most common field count in the sample. `fields` gives the smallest, the largest and the most common count of the whole input. |
| `bare_quote`, `stray_quote` | warning; error with `--strict` | See section 8. |
| `invalid_utf8` | warning; error with `--strict` | A sequence was replaced with U+FFFD. |
| `low_confidence` | warning | The dialect was detected with a confidence below 0.2. |

`ok` is true when no problem has the severity `error`.

### 10.4 `convert`

`convert` writes comma-separated, double-quoted CSV in UTF-8 without a byte order mark, with line feeds or, with `--crlf`, carriage return and line feed. With `--output` it writes to a temporary file beside the target and renames it only after the whole input was read without error, so a failed run leaves no partial file. On standard output it stops at the first error.

## 11. Evidence

How each claim is established:

- **Reader rules.** Table-driven tests for every rule of section 8, with positions.
- **Agreement with `encoding/csv`.** On valid UTF-8 input without carriage returns, a strict reader with the comma and double quote dialect returns the records `encoding/csv` returns, checked by a differential fuzz target. Outside that domain the two differ on purpose: wildcsv keeps a carriage return inside a quoted field, which `encoding/csv` removes; it rejects invalid UTF-8 in strict mode; and it does not require every record to have the same number of fields.
- **Robustness.** Fuzz targets check that no input makes `Detect` or the reader panic, exceed a limit, return an invalid UTF-8 string or report a position outside the input, and that detection is deterministic.
- **Round trip.** Tables generated with a known dialect are written, detected and read back.
- **Accuracy.** Dialect detection is measured on three public sets that other tools publish results for: POLLOCK [3], the W3C CSV on the Web test files [4] and the CSV Wrangling files [2], with the dialect annotations and the pass rule of García's benchmark [1]: a file passes when the delimiter and the quote character match. The files are fetched from their repository at a fixed commit and are not redistributed here.
- **No tuning on the test.** The files are split in two halves by a hash of their names. The method is adjusted on the development half only. The other half is run once, on a commit made before that run, and the result is published as measured. [accuracy.md](accuracy.md) gives both halves, the whole, the published results of other tools and every run of the held-out half.

## 12. Compatibility

- The module follows semantic versioning. Before 1.0 a minor version may change the API.
- Reading with an explicit dialect is stable: the same input, dialect and options give the same records in every release of a major version.
- Detection may improve between releases, so the dialect detected for a given file can change. A program that needs the same answer over time stores the dialect it accepted and passes it back.
- JSON shapes are versioned by their `schema` member. Members are only added within a version.

## 13. References

1. W. García. *Detecting CSV file dialects by table uniformity measurement and data type inference.* Data Science, 2024. [doi:10.3233/DS-240062](https://doi.org/10.3233/DS-240062). Benchmark and annotations: [ws-garcia/CSVsniffer](https://github.com/ws-garcia/CSVsniffer).
2. G. J. J. van den Burg, A. Nazábal, C. Sutton. *Wrangling messy CSV files by detecting row and type patterns.* Data Mining and Knowledge Discovery, 2019. [arXiv:1811.11242](https://arxiv.org/abs/1811.11242). Data: [alan-turing-institute/CSV_Wrangling](https://github.com/alan-turing-institute/CSV_Wrangling).
3. G. Vitagliano et al. *Pollock: A Data Loading Benchmark.* Proceedings of the VLDB Endowment, 2023. [HPI-Information-Systems/Pollock](https://github.com/HPI-Information-Systems/Pollock).
4. W3C. *CSV on the Web* test suite. [w3c/csvw](https://github.com/w3c/csvw).
5. Y. Shafranovich. *Common Format and MIME Type for Comma-Separated Values (CSV) Files.* [RFC 4180](https://www.rfc-editor.org/rfc/rfc4180).
6. WHATWG. *Encoding Standard.* [encoding.spec.whatwg.org](https://encoding.spec.whatwg.org/).
