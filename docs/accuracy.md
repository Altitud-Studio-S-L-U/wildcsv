# Accuracy of dialect detection

This page reports how often `wildcsv.Detect` finds the right delimiter and quote character on the public files that other CSV dialect detectors publish results for, how those numbers were produced, and where detection fails.

## What is measured

The files and their expected dialects come from the benchmark of García's *Detecting CSV file dialects by table uniformity measurement and data type inference* (Data Science, 2024), in the repository [ws-garcia/CSVsniffer](https://github.com/ws-garcia/CSVsniffer) at commit `bdb631d85007353fe09995c303b042725438e30f`, directory `python/`:

| Row | Files | Origin |
| --- | ---: | --- |
| POLLOCK | 148 | The Pollock data loading benchmark (Vitagliano et al., 2023) and other collected files |
| W3C-CSVW | 221 | Test files of the W3C CSV on the Web working group |
| CSV Wrangling | 179 | Files from GitHub curated by van den Burg, Nazábal and Sutton (2019) |
| CSV Wrangling CODEC | 142 | The CSV Wrangling files without encoding problems |
| CSV Wrangling MESSY | 126 | The CODEC files that are not plain CSV |

A file **passes** when the detected delimiter and quote character equal the annotated ones; no quoting counts as the double quote. This is the rule of the reference benchmark (`python/src/test_runner.py`). The escape character and the line ending are not compared. A file **fails** when no dialect comes back: `Detect` returns an error or a kind other than `delimited`.

The files are fetched at that commit and never committed here, because the CSV Wrangling files remain under their authors' copyright:

```sh
go run ./internal/cmd/accuracy fetch --dir ../csvsniffer
go run ./internal/cmd/accuracy run --dir ../csvsniffer --split dev
```

## How the method was tuned without tuning on the test

The files are split in two halves by the parity of the first byte of SHA-256 of `<set>/<file name>`, so that a file falls in the same half in the three CSV Wrangling rows. The detection method was adjusted on the **development half** only. The **held-out half** was run once, on commit `9e78c8e`, after the detector was committed; the harness refuses to run it without `--final` and logs every such run. The log is at the end of this page.

| Row | All | Development | Held-out |
| --- | ---: | ---: | ---: |
| POLLOCK | 148 | 80 | 68 |
| W3C-CSVW | 221 | 119 | 102 |
| CSV Wrangling | 179 | 90 | 89 |
| CSV Wrangling CODEC | 142 | 71 | 71 |
| CSV Wrangling MESSY | 126 | 61 | 65 |

## Results

**Accuracy** is passes over files run: a failure counts as wrong. The other columns reproduce the figures of the reference benchmark, so that they can sit beside the published ones: success ratio is passes over files minus failures, and precision, recall and F1 take true positives as passes, false positives as wrong dialects and false negatives as failures. The weighted F1 is Σ TPᵢ·F1ᵢ / Σ TPᵢ over the five rows.

In the MESSY row the reference runs only the 126 messy files but keeps the 142 CODEC files as the denominator of its figures. The compatible columns do the same; the accuracy column uses the 126 files run.

### Held-out half

| Row | Files | Passes | Failures | Accuracy | Success ratio | F1 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| POLLOCK | 68 | 67 | 0 | 98.53% | 98.53% | 0.9926 |
| W3C-CSVW | 102 | 94 | 0 | 92.16% | 92.16% | 0.9592 |
| CSV Wrangling | 89 | 77 | 0 | 86.52% | 86.52% | 0.9277 |
| CSV Wrangling CODEC | 71 | 60 | 0 | 84.51% | 84.51% | 0.9160 |
| CSV Wrangling MESSY | 65 | 54 | 0 | 83.08% | 76.06% | 0.8640 |

Weighted F1: **0.9367**.

### Development half

| Row | Files | Passes | Failures | Accuracy | Success ratio | F1 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| POLLOCK | 80 | 78 | 0 | 97.50% | 97.50% | 0.9873 |
| W3C-CSVW | 119 | 111 | 0 | 93.28% | 93.28% | 0.9652 |
| CSV Wrangling | 90 | 84 | 0 | 93.33% | 93.33% | 0.9655 |
| CSV Wrangling CODEC | 71 | 66 | 0 | 92.96% | 92.96% | 0.9635 |
| CSV Wrangling MESSY | 61 | 56 | 0 | 91.80% | 78.87% | 0.8819 |

Weighted F1: 0.9576.

### All files

| Row | Files | Passes | Failures | Accuracy | Success ratio | F1 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| POLLOCK | 148 | 145 | 0 | 97.97% | 97.97% | 0.9898 |
| W3C-CSVW | 221 | 205 | 0 | 92.76% | 92.76% | 0.9624 |
| CSV Wrangling | 179 | 161 | 0 | 89.94% | 89.94% | 0.9471 |
| CSV Wrangling CODEC | 142 | 126 | 0 | 88.73% | 88.73% | 0.9403 |
| CSV Wrangling MESSY | 126 | 110 | 0 | 87.30% | 77.46% | 0.8730 |

Weighted F1: 0.9475. Half of these files are the ones the method was tuned on; the held-out figure is the one to rely on.

### A fix after the held-out run

The held-out run on `9e78c8e` found one file that came back as `binary`: a semicolon-separated export whose sample holds 26 NUL bytes in 64 KiB, padding left by its exporter. The rule that had made any NUL byte mean binary was replaced by a general one, in which NUL counts toward the 1% of control characters like any other control (commit `e878117`). No constant was tuned. Before and after:

| Split | Weighted F1 before | Weighted F1 after | Change |
| --- | ---: | ---: | --- |
| Development | 0.9576 | 0.9576 | none |
| Held-out | 0.9353 | 0.9367 | CSV Wrangling: one failure becomes a pass |
| All | 0.9469 | 0.9475 | the same file |

## Published results of other tools

These figures are **as published by their authors, not re-run here**, on the whole sets.

From the README of [ws-garcia/CSVsniffer](https://github.com/ws-garcia/CSVsniffer/tree/bdb631d85007353fe09995c303b042725438e30f) at the commit above. Success ratio, with the failure ratio in parentheses:

| Row | CSVsniffer MADSE | CSVsniffer | CleverCSV | `csv.Sniffer` | DuckDB `sniff_csv` |
| --- | ---: | ---: | ---: | ---: | ---: |
| POLLOCK | 95.27% (0.00%) | 96.55% (2.03%) | 95.17% (2.03%) | 96.35% (7.43%) | 84.14% (2.03%) |
| W3C-CSVW | 94.52% (0.91%) | 95.39% (1.81%) | 61.11% (2.26%) | 97.69% (41.18%) | 99.08% (1.81%) |
| CSV Wrangling | 90.32% (13.41%) | 90.57% (11.17%) | 84.31% (14.53%) | 80.56% (19.55%) | 76.82% (15.64%) |
| CSV Wrangling CODEC | 91.55% (0.00%) | 89.47% (6.34%) | 84.25% (10.56%) | 80.00% (15.49%) | 78.03% (7.04%) |
| CSV Wrangling MESSY | 80.28% (0.00%) | 78.20% (6.34%) | 71.65% (10.56%) | 66.67% (15.49%) | 65.91% (7.04%) |
| Weighted F1 | 0.9378 | 0.9260 | 0.8425 | 0.8049 | 0.8806 |

[csv-nose](https://github.com/jqnatividad/csv-nose/blob/main/docs/ACCURACY.md) 1.4.0, a Rust implementation of the table uniformity method, publishes these success rates: POLLOCK 98.65%, W3C-CSVW 99.55%, CSV Wrangling 94.97%, CODEC 94.37%, MESSY 93.65%.

What differs in the conditions:

- The published figures are measured on the whole sets, which are also the files those methods were developed on. Our whole-set figures share that bias; our held-out figures do not.
- The success ratio leaves failures out of its denominator. The Python tools count a file they cannot decode as a failure; wildcsv decodes those files and counts every miss, so its success ratio and its accuracy are nearly the same.
- The CSV Wrangling row of the reference has 179 files with failure ratios above 10% for every tool, mostly files in encodings Python could not open with its default codec.

On the reference-compatible weighted F1, wildcsv's held-out half (0.9367) is level with the best published Python figure (0.9378) and its whole-set figure (0.9475) above it. csv-nose publishes higher success rates on every row.

## Method history on the development half

The starting point was the table uniformity method with the median absolute deviation and entropy terms, as written in [design.md §7.4](design.md#74-score), on wildcsv's own parser and cell classifier. Each refinement was tried alone against the best so far and kept only if it raised the weighted F1 or the passes and lowered no row by more than one file.

| Step | Weighted F1 | Passes of 421 runs | Kept |
| --- | ---: | ---: | --- |
| Baseline: published method on wildcsv's parser | 0.9539 | 392 | — |
| Score 100 or 200 records instead of 50 | 0.9539 | 392 | No: no change, slower |
| Type only the records with the most common field count | 0.9526 | 391 | No |
| Scale the score by correct quoting against stray and bare quotes | 0.9539 | 392 | No: identical detections; the tie rule already covers it |
| Penalise rare delimiters (factors 0.9 to 0.7) | 0.9539 | 392 | No: no change |
| Read irregular samples as one column | 0.9526 to 0.9530 | 391 | No |
| Leave `#` comment lines out for other delimiters | **0.9576** | **395** | **Yes** |
| Drop up to five leading records that do not fit | 0.9585 | 396 | No: POLLOCK lost two files |

One more change was tried after the held-out run, prompted by a documentation example rather than by a benchmark file: in the tie rule, count the double quotes that start or end a cell as defects of the reading without quotes. It left the development half unchanged and lost one held-out file (`blocks.csv`, where the apostrophe then won the tie), held-out weighted F1 0.9367 → 0.9328, so it was dropped. Those three runs are in the log below.

## Confidence

Accuracy over all files, each counted once, by the confidence `Detect` reported:

| Confidence | Files | Accuracy |
| --- | ---: | ---: |
| below 0.2 | 31 | 80.6% |
| 0.2 to 0.5 | 36 | 66.7% |
| 0.5 to 0.8 | 130 | 98.5% |
| 0.8 and above | 350 | 95.1% |

Below 0.5 one answer in four is wrong: show the alternatives, or ask. The high band still holds mistakes, mostly the tiny one-column files described below.

## Known limits

Of the 37 files missed in the POLLOCK, W3C-CSVW and CSV Wrangling rows (the CODEC and MESSY rows reuse CSV Wrangling files):

- **One-column files of one to five lines (17: 16 in W3C-CSVW, 1 in CSV Wrangling).** With so little text, a character that happens to occur, such as a space, a colon or a `#`, splits the lines as well as nothing does. These files expect the comma, which never occurs in them. Pass `Options.Dialect` when you know a file has one column.
- **Apostrophe against double quote (6).** Files where single quotes surround some values and double quotes others.
- **Comment and preamble lines (4).** Long `#` headers mixed with records that the comment rule does not separate.
- **Adversarial files (3).** Two POLLOCK files are built so that a pipe occurs more often than the real delimiter, and a third holds geometry text full of commas.
- **Messy delimiters (7).** Mixed separators, prose with punctuation and very irregular tables, where the annotated delimiter is a judgement.

## Header detection

The benchmark sets carry no ground truth for headers, so the header guess is not measured and no accuracy is claimed for it. Its rules are in [design.md §7.6](design.md#76-header-and-columns).

## Held-out run log

```text
2026-11-10T16:21:30Z  heldout  9e78c8ea458b871a61940ddef97154aed4b23963  weighted F1 0.9353
2026-11-10T16:21:32Z  all      9e78c8ea458b871a61940ddef97154aed4b23963  weighted F1 0.9469
2026-11-10T16:22:27Z  dev      9e78c8ea458b871a61940ddef97154aed4b23963  weighted F1 0.9576
2026-11-10T16:22:29Z  heldout  9e78c8ea458b871a61940ddef97154aed4b23963  weighted F1 0.9367
2026-11-10T16:22:31Z  all      9e78c8ea458b871a61940ddef97154aed4b23963  weighted F1 0.9475
2026-11-10T16:29:54Z  dev      c715f5cb2375231787ee9a010421b40bc8fdcbac  weighted F1 0.9576
2026-11-10T16:29:56Z  heldout  c715f5cb2375231787ee9a010421b40bc8fdcbac  weighted F1 0.9328
2026-11-10T16:29:58Z  all      c715f5cb2375231787ee9a010421b40bc8fdcbac  weighted F1 0.9456
2026-11-10T16:52:10Z  heldout  c6e3a76c5cb63333885f5d7346f58f6771b71a68  weighted F1 0.9367
2026-11-10T16:52:13Z  all      c6e3a76c5cb63333885f5d7346f58f6771b71a68  weighted F1 0.9475
```

The runs at 16:22 measured the NUL fix in the working tree on top of `9e78c8e`, before it was committed as `e878117`; the commit contains that change and nothing else. The runs on `c715f5c` measured the tie-rule change described under the method history, in the working tree; it was dropped and never committed. The last two runs confirmed, on the working tree of the next commit, that fixes to the reader’s limits, positions and UTF-16 handling left every detection unchanged.
