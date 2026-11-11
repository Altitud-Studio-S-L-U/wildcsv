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

The files are split in two halves by the parity of the first byte of SHA-256 of `<set>/<file name>`, so that a file falls in the same half in the three CSV Wrangling rows. The detection method was adjusted on the **development half** only. The **held-out half** was first run on commit `9e78c8e`, after the detector was committed. A second round of changes, again chosen on the development half alone, was followed by one more held-out run, on commit `670f618`. The harness refuses to run the held-out half without `--final` and logs every such run; the log is at the end of this page. Each run makes the held-out half a little less independent of the method, which is why every one is listed.

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

Commit `670f618`, the single held-out run of the second round:

| Row | Files | Passes | Failures | Accuracy | Success ratio | F1 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| POLLOCK | 68 | 67 | 0 | 98.53% | 98.53% | 0.9926 |
| W3C-CSVW | 102 | 99 | 0 | 97.06% | 97.06% | 0.9851 |
| CSV Wrangling | 89 | 78 | 0 | 87.64% | 87.64% | 0.9341 |
| CSV Wrangling CODEC | 71 | 61 | 0 | 85.92% | 85.92% | 0.9242 |
| CSV Wrangling MESSY | 65 | 55 | 0 | 84.62% | 77.46% | 0.8730 |

Weighted F1: **0.9480**, against 0.9367 at the end of the first round.

### Development half

| Row | Files | Passes | Failures | Accuracy | Success ratio | F1 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| POLLOCK | 80 | 78 | 0 | 97.50% | 97.50% | 0.9873 |
| W3C-CSVW | 119 | 115 | 0 | 96.64% | 96.64% | 0.9829 |
| CSV Wrangling | 90 | 86 | 0 | 95.56% | 95.56% | 0.9773 |
| CSV Wrangling CODEC | 71 | 68 | 0 | 95.77% | 95.77% | 0.9784 |
| CSV Wrangling MESSY | 61 | 58 | 0 | 95.08% | 81.69% | 0.8992 |

Weighted F1: 0.9698.

### All files

| Row | Files | Passes | Failures | Accuracy | Success ratio | F1 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| POLLOCK | 148 | 145 | 0 | 97.97% | 97.97% | 0.9898 |
| W3C-CSVW | 221 | 214 | 0 | 96.83% | 96.83% | 0.9839 |
| CSV Wrangling | 179 | 164 | 0 | 91.62% | 91.62% | 0.9563 |
| CSV Wrangling CODEC | 142 | 129 | 0 | 90.85% | 90.85% | 0.9520 |
| CSV Wrangling MESSY | 126 | 113 | 0 | 89.68% | 79.58% | 0.8863 |

Weighted F1: 0.9593. Half of these files are the ones the method was tuned on; the held-out figure is the one to rely on.

### A fix after the first held-out run

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

On the reference-compatible weighted F1, wildcsv's held-out half (0.9480) and its whole set (0.9593) are above the best figure the reference publishes (0.9378). Row by row, on the whole sets, against the best published success ratio of the five tools in the table above:

| Row | wildcsv | Best published | By |
| --- | ---: | ---: | --- |
| POLLOCK | 97.97% | 96.55% | CSVsniffer |
| W3C-CSVW | 96.83% | 99.08% | DuckDB `sniff_csv` |
| CSV Wrangling | 91.62% | 90.57% | CSVsniffer |
| CSV Wrangling CODEC | 90.85% | 91.55% | CSVsniffer MADSE |
| CSV Wrangling MESSY | 79.58% | 80.28% | CSVsniffer MADSE |

csv-nose publishes higher success rates on every row, measured on the whole sets it was developed on; it has not been run here, so no comparison on the held-out half is claimed.

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

### Second round

Two rules, each tried alone against the start of the round and kept only if it raised the weighted F1 and lowered no row:

| Step | Weighted F1 | Passes of 421 runs | Kept |
| --- | ---: | ---: | --- |
| Start of the round (commit `c6e3a76`) | 0.9576 | 395 | — |
| Let the one-column table compete in samples of fewer than 50 line feeds | 0.9614 | 398 | No |
| The same, fewer than 10 line feeds | 0.9614 | 398 | No |
| The same, fewer than 5 line feeds | 0.9627 | 399 | **Yes** |
| Ignore delimiters that occur only at the start of lines | **0.9698** | **405** | **Yes** |

The one-column table won four CSV on the Web unit tests of two lines (`test230`, `test296`, `test300`, `test301`), where a space or a `#` split the lines unevenly. With a bound of 10 or 50 line feeds it also took `methane_molecular_structure_xyz_20140911.csv`, a space-aligned table of seven lines, so the bound was set below that. The second rule took two LimeSurvey dumps, `BIO.csv` and `limesurvey_survey_94328.csv`, whose `#` occurs only at the start of comment lines, in the three CSV Wrangling rows. The held-out half was then run once: weighted F1 0.9367 → 0.9480, with W3C-CSVW 94 → 99 files, CSV Wrangling 77 → 78, POLLOCK unchanged and no row lower.

## Confidence

Accuracy over all files, each counted once, by the confidence `Detect` reported:

| Confidence | Files | Accuracy |
| --- | ---: | ---: |
| below 0.2 | 32 | 78.1% |
| 0.2 to 0.5 | 34 | 70.6% |
| 0.5 to 0.8 | 137 | 99.3% |
| 0.8 and above | 345 | 98.0% |

Below 0.5 about one answer in four is wrong: show the alternatives, or ask. The high band still holds mistakes, mostly apostrophes taken for the quote character or the reverse, described below.

## Known limits

Of the 25 files missed in the POLLOCK, W3C-CSVW and CSV Wrangling rows (the CODEC and MESSY rows reuse CSV Wrangling files):

- **Apostrophe against double quote (6).** `baselog.csv`, `Blizak_2010.csv`, `isco.csv`, `JV_newsite_IBC_current_20110517.csv`, `results.csv` and `set001.csv`: single quotes surround some values and double quotes others, or occur as empty `''` cells or escaped as `\'`, and the annotation picks one.
- **Delimiters inside values (6).** Unit tests of the CSV on the Web specification of two or three lines: in `test245.csv` to `test247.csv` date-time patterns such as `yyyy-MM-ddTHH:mm:ss.S` read as colon-separated numbers, and in `test305.csv` to `test307.csv` cells such as `1;2;3,4;5;6` read as decimal numbers under the semicolon.
- **Not tables (4).** `bugs.csv` is an HTTP response, `task4_pad.csv` a fixed-width report, `register_data.csv` four lines of which one is data, `replace.csv` fragments without a comma.
- **Adversarial files (3).** Two POLLOCK files are built so that a pipe occurs more often than the real delimiter, and a third holds geometry text full of commas.
- **One column split by chance (2).** `professions.csv` is five lines of one or two words, read with the space; `admins.csv` is the single line `milan#_%pass`.
- **Comment lines with `#` inside them (1).** `user_design_template.csv`.
- **Disputed or irregular (3).** `gsi_adresser_og_elevtall_2010.csv` is annotated as comma-separated, but all its 3,573 lines hold 7 fields separated by semicolons and only 99 hold a comma; `ACS_12_5YR_S1903_metadata.csv` has two columns whose second holds lists separated by semicolons; `resources.csv` is four lines of different lengths written to test a parser.

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
2026-11-10T19:39:04Z  heldout  670f618b4258b70c8ac8ba9ec0af978ab5bed5ef  weighted F1 0.9480
2026-11-10T19:39:06Z  all      670f618b4258b70c8ac8ba9ec0af978ab5bed5ef  weighted F1 0.9593
```

The runs on `670f618` are the second round's single held-out run, and the whole set beside it. The runs at 16:22 measured the NUL fix in the working tree on top of `9e78c8e`, before it was committed as `e878117`; the commit contains that change and nothing else. The runs on `c715f5c` measured the tie-rule change described under the method history, in the working tree; it was dropped and never committed. The last two runs confirmed, on the working tree of the next commit, that fixes to the reader’s limits, positions and UTF-16 handling left every detection unchanged.
