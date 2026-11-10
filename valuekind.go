package wildcsv

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValueKind is the kind of value a cell or a column holds.
type ValueKind string

// Value kinds, in the order the classifier tests them (design §7.3).
const (
	ValueEmpty    ValueKind = "empty"
	ValueNull     ValueKind = "null"
	ValueBoolean  ValueKind = "boolean"
	ValueInteger  ValueKind = "integer"
	ValueNumber   ValueKind = "number"
	ValuePercent  ValueKind = "percent"
	ValueCurrency ValueKind = "currency"
	ValueDate     ValueKind = "date"
	ValueTime     ValueKind = "time"
	ValueDateTime ValueKind = "datetime"
	ValueURL      ValueKind = "url"
	ValueEmail    ValueKind = "email"
	ValueIPv4     ValueKind = "ipv4"
	ValueUUID     ValueKind = "uuid"
	ValuePath     ValueKind = "path"
	ValueText     ValueKind = "text"
	ValueOther    ValueKind = "other"
)

// Decimal separator evidence of a numeric cell.
const (
	decUnknown = iota
	decPoint
	decComma
)

const maxClassifiedBytes = 256

// classify returns the kind of a cell and, for numeric kinds, which decimal
// separator it shows.
func classify(cell string, quoted bool) (ValueKind, int) {
	s := strings.TrimSpace(cell)
	if s == "" {
		return ValueEmpty, decUnknown
	}
	if len(s) > maxClassifiedBytes {
		return ValueOther, decUnknown
	}
	if isNull(s) {
		return ValueNull, decUnknown
	}
	if isBoolean(s) {
		return ValueBoolean, decUnknown
	}
	if isInteger(s) {
		return ValueInteger, decUnknown
	}
	if ok, dec := isNumber(s); ok {
		return ValueNumber, dec
	}
	if ok, dec := isPercent(s); ok {
		return ValuePercent, dec
	}
	if ok, dec := isCurrency(s); ok {
		return ValueCurrency, dec
	}
	if isDate(s) {
		return ValueDate, decUnknown
	}
	if isTime(s) {
		return ValueTime, decUnknown
	}
	if isDateTime(s) {
		return ValueDateTime, decUnknown
	}
	if isURL(s) {
		return ValueURL, decUnknown
	}
	if isEmail(s) {
		return ValueEmail, decUnknown
	}
	if isIPv4(s) {
		return ValueIPv4, decUnknown
	}
	if isUUID(s) {
		return ValueUUID, decUnknown
	}
	if isPath(s) {
		return ValuePath, decUnknown
	}
	if isText(s, quoted) {
		return ValueText, decUnknown
	}
	return ValueOther, decUnknown
}

// lowerASCII returns s in lower case without allocating when s is ASCII and
// at most 8 bytes long, the only strings the word lists below can match; ok is
// false otherwise, and the caller falls back to strings.ToLower.
func lowerASCII(s string, buf *[8]byte) (string, bool) {
	if len(s) > len(buf) {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= utf8.RuneSelf {
			return "", false
		}
		if c >= 65 && c <= 90 {
			c += 32
		}
		buf[i] = c
	}
	return string(buf[:len(s)]), true
}

// lower is strings.ToLower, cheaper for short ASCII words.
func lower(s string, buf *[8]byte) string {
	if l, ok := lowerASCII(s, buf); ok {
		return l
	}
	if isASCIIString(s) {
		return "" // longer than any word looked up: matches none
	}
	return strings.ToLower(s)
}

func isASCIIString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func isNull(s string) bool {
	var buf [8]byte
	switch lower(s, &buf) {
	case "na", "n/a", "nan", "null", "nil", "none", "-":
		return true
	}
	return false
}

func isBoolean(s string) bool {
	var buf [8]byte
	switch lower(s, &buf) {
	case "true", "false", "yes", "no":
		return true
	}
	return false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func trimSign(s string) string {
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		return s[1:]
	}
	return s
}

func isInteger(s string) bool { return allDigits(trimSign(s)) }

// isNumber recognises a decimal with a point or a comma and an optional
// exponent, or digits in groups of three with an optional decimal part.
func isNumber(s string) (bool, int) {
	u := trimSign(s)
	if ok, dec := isDecimal(u); ok {
		return true, dec
	}
	return isGrouped(u)
}

func isDecimal(u string) (bool, int) {
	mant, exp := u, ""
	if i := strings.IndexAny(u, "eE"); i >= 0 {
		mant, exp = u[:i], u[i+1:]
		if !allDigits(trimSign(exp)) {
			return false, decUnknown
		}
	}
	sep := strings.IndexAny(mant, ".,")
	if sep < 0 {
		if exp != "" && allDigits(mant) {
			return true, decUnknown
		}
		return false, decUnknown
	}
	intPart, frac := mant[:sep], mant[sep+1:]
	if (intPart != "" && !allDigits(intPart)) || (frac != "" && !allDigits(frac)) || intPart+frac == "" {
		return false, decUnknown
	}
	dec := decPoint
	if mant[sep] == ',' {
		dec = decComma
	}
	// "1,234" and "1.234" may be a thousands separator.
	if len(frac) == 3 && intPart != "" && exp == "" {
		dec = decUnknown
	}
	return true, dec
}

func isGrouped(u string) (bool, int) {
	first := 0
	for first < len(u) && isDigit(u[first]) {
		first++
	}
	if first == 0 || first > 3 || first == len(u) {
		return false, decUnknown
	}
	g := u[first]
	if g != ',' && g != '.' && g != ' ' && g != '\'' {
		return false, decUnknown
	}
	i := first
	for i < len(u) && u[i] == g {
		if i+4 > len(u) || !allDigits(u[i+1:i+4]) {
			return false, decUnknown
		}
		i += 4
	}
	if i == len(u) {
		switch g {
		case ',':
			return true, decPoint
		case '.':
			return true, decComma
		}
		return true, decUnknown
	}
	d := u[i]
	if d == g || (d != '.' && d != ',') || !allDigits(u[i+1:]) {
		return false, decUnknown
	}
	if d == ',' {
		return true, decComma
	}
	return true, decPoint
}

func isNumeric(s string) (bool, int) {
	if isInteger(s) {
		return true, decUnknown
	}
	return isNumber(s)
}

func isPercent(s string) (bool, int) {
	if !strings.HasSuffix(s, "%") {
		return false, decUnknown
	}
	return isNumeric(strings.TrimSuffix(s[:len(s)-1], " "))
}

func isCurrency(s string) (bool, int) {
	sign := ""
	if s[0] == '+' || s[0] == '-' {
		sign, s = s[:1], s[1:]
	}
	if s == "" {
		return false, decUnknown
	}
	if r, size := utf8.DecodeRuneInString(s); unicode.Is(unicode.Sc, r) {
		return isNumeric(sign + strings.TrimPrefix(s[size:], " "))
	}
	if r, size := utf8.DecodeLastRuneInString(s); unicode.Is(unicode.Sc, r) {
		return isNumeric(sign + strings.TrimSuffix(s[:len(s)-size], " "))
	}
	return false, decUnknown
}

func atoiSmall(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
}

func validMonthDay(m, d string) bool {
	if len(m) == 0 || len(m) > 2 || len(d) == 0 || len(d) > 2 || !allDigits(m) || !allDigits(d) {
		return false
	}
	mm, dd := atoiSmall(m), atoiSmall(d)
	return mm >= 1 && mm <= 12 && dd >= 1 && dd <= 31
}

// isDate recognises year, month and day in the orders Y-M-D, D-M-Y and M-D-Y
// with the same separator twice.
func isDate(s string) bool {
	if len(s) < 5 || len(s) > 10 {
		return false
	}
	sep := byte(0)
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '-' || c == '/' || c == '.' {
			sep = c
			break
		}
	}
	if sep == 0 {
		return false
	}
	parts := strings.Split(s, string(sep))
	if len(parts) != 3 {
		return false
	}
	a, b, c := parts[0], parts[1], parts[2]
	if !allDigits(a) || !allDigits(b) || !allDigits(c) {
		return false
	}
	if len(a) == 4 {
		return validMonthDay(b, c)
	}
	if len(c) != 4 && len(c) != 2 {
		return false
	}
	return validMonthDay(b, a) || validMonthDay(a, b)
}

func twoDigitsIn(s string, max int) bool {
	return len(s) == 2 && allDigits(s) && atoiSmall(s) <= max
}

// isTime recognises H:MM or HH:MM with optional seconds, fraction, AM or PM,
// and zone.
func isTime(s string) bool {
	ampm := false
	if hasSuffixAMPM(s) {
		ampm = true
		s = strings.TrimSuffix(s[:len(s)-2], " ")
	}
	// zone
	if strings.HasSuffix(s, "Z") || strings.HasSuffix(s, "z") {
		s = s[:len(s)-1]
	} else if i := strings.LastIndexAny(s, "+-"); i > 0 {
		zone := s[i+1:]
		if !(len(zone) == 5 && zone[2] == ':' && twoDigitsIn(zone[:2], 23) && twoDigitsIn(zone[3:], 59)) &&
			!(len(zone) == 4 && twoDigitsIn(zone[:2], 23) && twoDigitsIn(zone[2:], 59)) {
			return false
		}
		s = s[:i]
	}
	c := strings.IndexByte(s, ':')
	if c < 1 || c > 2 {
		return false
	}
	h := s[:c]
	if !allDigits(h) {
		return false
	}
	hour := atoiSmall(h)
	if hour > 23 || (ampm && (hour < 1 || hour > 12)) {
		return false
	}
	rest := s[c+1:]
	if len(rest) < 2 || !twoDigitsIn(rest[:2], 59) {
		return false
	}
	rest = rest[2:]
	if rest == "" {
		return true
	}
	if rest[0] != ':' || len(rest) < 3 || !twoDigitsIn(rest[1:3], 59) {
		return false
	}
	rest = rest[3:]
	if rest == "" {
		return true
	}
	return rest[0] == '.' && allDigits(rest[1:])
}

func isDateTime(s string) bool {
	i := strings.IndexAny(s, " T")
	if i < 0 {
		return false
	}
	return isDate(s[:i]) && isTime(strings.TrimLeft(s[i+1:], " "))
}

func hasSpace(s string) bool { return strings.ContainsAny(s, " \t") }

func isURL(s string) bool {
	if hasSpace(s) {
		return false
	}
	low := s
	if !isASCIIString(s) {
		low = strings.ToLower(s)
	}
	for _, p := range []string{"http://", "https://", "ftp://", "www."} {
		if len(s) > len(p) && hasPrefixLowerASCII(low, p) {
			return true
		}
	}
	return false
}

// hasPrefixLowerASCII reports whether s starts with p, which is lower-case
// ASCII, ignoring the case of ASCII letters in s.
func hasPrefixLowerASCII(s, p string) bool {
	if len(s) < len(p) {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := s[i]
		if c >= 65 && c <= 90 {
			c += 32
		}
		if c != p[i] {
			return false
		}
	}
	return true
}

// hasSuffixAMPM reports whether strings.ToUpper(s) ends in AM or PM.
func hasSuffixAMPM(s string) bool {
	if !isASCIIString(s) {
		u := strings.ToUpper(s)
		return strings.HasSuffix(u, "AM") || strings.HasSuffix(u, "PM")
	}
	if len(s) < 2 {
		return false
	}
	a, m := s[len(s)-2]&^32, s[len(s)-1]&^32
	return (a == 65 || a == 80) && m == 77
}

func isEmail(s string) bool {
	if hasSpace(s) || strings.Count(s, "@") != 1 {
		return false
	}
	at := strings.IndexByte(s, '@')
	domain := s[at+1:]
	dot := strings.LastIndexByte(domain, '.')
	return at > 0 && dot > 0 && dot < len(domain)-1
}

func isIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 3 || !allDigits(p) || atoiSmall(p) > 255 {
			return false
		}
	}
	return true
}

func isHex(c byte) bool { return isDigit(c) || c|0x20 >= 'a' && c|0x20 <= 'f' }

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if s[i] != '-' {
				return false
			}
		} else if !isHex(s[i]) {
			return false
		}
	}
	return true
}

func isPath(s string) bool {
	if s[0] == '~' || s[0] == '.' {
		s = s[1:]
	}
	s = strings.TrimSuffix(s, "/")
	if s == "" || s[0] != '/' {
		return false
	}
	for _, part := range strings.Split(s[1:], "/") {
		if part == "" {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if !isASCIILetter(c) && !isDigit(c) && c != '.' && c != '-' && c != '_' {
				return false
			}
		}
	}
	return true
}

func isText(s string, quoted bool) bool {
	letterOrDigit := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			letterOrDigit = true
		case unicode.IsMark(r) || r == ' ':
		case r == '_' || r == '-' || r == '.' || r == '(' || r == ')' || r == '?' || r == '!':
		case r == ',' && quoted:
		default:
			return false
		}
	}
	return letterOrDigit
}
