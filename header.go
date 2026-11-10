package wildcsv

import "strings"

// HeaderGuess says whether the first record looks like a header.
type HeaderGuess string

// A table made only of text gives no evidence either way, so the guess has
// three values. The reader never drops the first record on its own.
const (
	HeaderUnknown HeaderGuess = "unknown"
	HeaderPresent HeaderGuess = "present"
	HeaderAbsent  HeaderGuess = "absent"
)

// Column describes one column of the sample.
type Column struct {
	Name         string    // the first record's cell, when the header is present
	Kind         ValueKind // the kind of most of its values
	DecimalComma bool      // numbers in it use a comma as decimal separator
}

// headerKinds are the kinds whose presence under a text cell suggests a
// header; integer counts as number.
var headerKinds = map[ValueKind]bool{
	ValueNumber: true, ValuePercent: true, ValueCurrency: true, ValueDate: true,
	ValueTime: true, ValueDateTime: true, ValueBoolean: true,
}

func headerKind(k ValueKind) ValueKind {
	if k == ValueInteger {
		return ValueNumber
	}
	return k
}

// kindOrder lists kinds in classifier order, so ties resolve the same way
// every time.
var kindOrder = []ValueKind{
	ValueBoolean, ValueInteger, ValueNumber, ValuePercent, ValueCurrency, ValueDate, ValueTime,
	ValueDateTime, ValueURL, ValueEmail, ValueIPv4, ValueUUID, ValuePath, ValueText, ValueOther,
}

// columnCount is the most common field count; ties go to the count seen first.
func columnCount(t *sampleTable) int {
	counts := map[int]int{}
	best, bestN := 0, 0
	for _, rec := range t.records {
		k := len(rec)
		counts[k]++
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	return best
}

func guessHeader(t *sampleTable, ncols int) HeaderGuess {
	n := len(t.records)
	if n < 2 {
		return HeaderUnknown
	}
	firstHasEmpty := false
	for _, k := range t.kinds[0] {
		if k == ValueEmpty {
			firstHasEmpty = true
		}
	}
	present, absent := false, false
	for j := 0; j < ncols && j < len(t.kinds[0]); j++ {
		counts := map[ValueKind]int{}
		others := 0
		for _, rec := range t.kinds[1:] {
			if j >= len(rec) || rec[j] == ValueEmpty || rec[j] == ValueNull {
				continue
			}
			counts[headerKind(rec[j])]++
			others++
		}
		if others == 0 {
			continue
		}
		var dominant ValueKind
		for _, k := range kindOrder {
			if headerKinds[k] && counts[k]*100 >= others*80 {
				dominant = k
				break
			}
		}
		if dominant == "" {
			continue
		}
		switch headerKind(t.kinds[0][j]) {
		case ValueText:
			present = true
		case dominant:
			absent = true
		}
	}
	switch {
	case present && !firstHasEmpty:
		return HeaderPresent
	case absent && !present:
		return HeaderAbsent
	}
	return HeaderUnknown
}

func describeColumns(t *sampleTable, ncols int, header HeaderGuess) []Column {
	cols := make([]Column, ncols)
	first := 0
	if header == HeaderPresent {
		first = 1
	}
	for j := range cols {
		if header == HeaderPresent && j < len(t.records[0]) {
			cols[j].Name = strings.TrimSpace(t.records[0][j])
		}
		counts := map[ValueKind]int{}
		comma, point, total := 0, 0, 0
		for i := first; i < len(t.kinds); i++ {
			if j >= len(t.kinds[i]) {
				continue
			}
			k := t.kinds[i][j]
			if k == ValueEmpty || k == ValueNull {
				continue
			}
			counts[k]++
			total++
			switch t.decs[i][j] {
			case decComma:
				comma++
			case decPoint:
				point++
			}
		}
		cols[j].Kind = columnKind(counts, total)
		switch cols[j].Kind {
		case ValueNumber, ValuePercent, ValueCurrency:
			cols[j].DecimalComma = comma > point
		}
	}
	return cols
}

func columnKind(counts map[ValueKind]int, total int) ValueKind {
	if total == 0 {
		return ValueEmpty
	}
	enough := func(c int) bool { return c*100 >= total*90 }
	if enough(counts[ValueInteger]) {
		return ValueInteger
	}
	if enough(counts[ValueInteger] + counts[ValueNumber]) {
		return ValueNumber
	}
	for _, k := range kindOrder {
		if enough(counts[k]) {
			return k
		}
	}
	return ValueText
}
