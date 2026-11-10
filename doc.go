// Package wildcsv reads delimited text that the calling program did not
// produce: a customer upload, a spreadsheet export, a bank statement, a file
// handed to a coding agent.
//
// It answers three questions about bytes of unknown origin:
//
//  1. Is this delimited text at all, or a workbook, an archive, JSON, a PDF?
//     [Detect] reports the [Kind].
//  2. Which encoding and which dialect does it use, and how sure is that
//     answer? [Detect] reports the [Encoding], the [Dialect], a confidence
//     margin and the runner-up dialects.
//  3. What are its records, and where exactly is each problem? [NewReader]
//     returns a [Reader] that streams records under explicit [Limits] and
//     reports every problem as a [*ParseError] with its record, line, column
//     and byte offset.
//
// Detection examines a sample of fixed size, reading holds at most one record,
// and every field and record has a size limit, so no input makes memory grow
// without limit. The same bytes and options always give the same answer.
//
// The package depends only on the standard library. Its contract, including
// the detection method and the reading rules, is in docs/design.md.
package wildcsv
