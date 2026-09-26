// SPDX-FileCopyrightText: 2026 Plux contributors
// SPDX-License-Identifier: Apache-2.0

package pxl

import (
	"fmt"
	"strconv"
	"strings"
)

// Date is a proleptic Gregorian date, as days since 1970-01-01; years are
// 1 to 9999.
type Date int64

// DateTime is an instant with the UTC offset it was written in. Values are
// equal and ordered by instant.
type DateTime struct {
	// Millis are milliseconds since 1970-01-01T00:00Z.
	Millis int64
	// Offset is the UTC offset in minutes, within ±23:59.
	Offset int32
}

// Duration is a length of time in milliseconds.
type Duration int64

// Calendar bounds and units.
const (
	msPerSecond = 1000
	msPerMinute = 60 * msPerSecond
	msPerHour   = 60 * msPerMinute
	msPerDay    = 24 * msPerHour
	maxOffset   = 23*60 + 59
)

// Days of 0001-01-01 and 9999-12-31, the range of dates.
var (
	minDate = daysFromCivil(1, 1, 1)
	maxDate = daysFromCivil(9999, 12, 31)
)

// daysFromCivil returns the days since 1970-01-01 of a Gregorian date
// (H. Hinnant's algorithm).
func daysFromCivil(y, m, d int64) int64 {
	if m <= 2 {
		y--
	}
	era := floorDiv(y, 400)
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// civilFromDays is the inverse of daysFromCivil.
func civilFromDays(z int64) (y, m, d int64) {
	z += 719468
	era := floorDiv(z, 146097)
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = doy - (153*mp+2)/5 + 1
	m = mp + 3
	if m > 12 {
		m -= 12
	}
	if m <= 2 {
		y++
	}
	return y, m, d
}

// floorDiv divides rounding toward negative infinity.
func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// daysInMonth returns the length of a month.
func daysInMonth(y, m int64) int64 {
	switch m {
	case 2:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	default:
		return 31
	}
}

// makeDate validates a Gregorian date.
func makeDate(y, m, d int64) (Date, bool) {
	if y < 1 || y > 9999 || m < 1 || m > 12 || d < 1 || d > daysInMonth(y, m) {
		return 0, false
	}
	return Date(daysFromCivil(y, m, d)), true
}

// validDate reports whether days lies in years 1–9999.
func validDate(days int64) bool { return days >= minDate && days <= maxDate }

// String returns YYYY-MM-DD.
func (d Date) String() string {
	y, m, day := civilFromDays(int64(d))
	return fmt.Sprintf("%04d-%02d-%02d", y, m, day)
}

// parseDate reads YYYY-MM-DD.
func parseDate(s string) (Date, bool) {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return 0, false
	}
	y, ok1 := atoiDigits(s[0:4])
	m, ok2 := atoiDigits(s[5:7])
	d, ok3 := atoiDigits(s[8:10])
	if !ok1 || !ok2 || !ok3 {
		return 0, false
	}
	return makeDate(y, m, d)
}

// atoiDigits parses ASCII digits only.
func atoiDigits(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for i := range len(s) {
		if !isDigit(s[i]) {
			return 0, false
		}
		n = n*10 + int64(s[i]-'0')
	}
	return n, true
}

// local returns the local days and milliseconds of the day of t.
func (t DateTime) local() (days, msOfDay int64) {
	l := t.Millis + int64(t.Offset)*msPerMinute
	days = floorDiv(l, msPerDay)
	return days, l - days*msPerDay
}

// makeDateTime builds a dateTime from a local day, time and offset, and
// checks that its local date is in years 1–9999.
func makeDateTime(days, msOfDay int64, offset int32) (DateTime, bool) {
	if !validDate(days) {
		return DateTime{}, false
	}
	return DateTime{Millis: days*msPerDay + msOfDay - int64(offset)*msPerMinute, Offset: offset}, true
}

// String returns RFC 3339 with milliseconds when they are not zero and Z
// for offset zero.
func (t DateTime) String() string {
	days, ms := t.local()
	var b strings.Builder
	b.WriteString(Date(days).String())
	fmt.Fprintf(&b, "T%02d:%02d:%02d", ms/msPerHour, ms%msPerHour/msPerMinute, ms%msPerMinute/msPerSecond)
	if frac := ms % msPerSecond; frac != 0 {
		fmt.Fprintf(&b, ".%03d", frac)
	}
	switch off := t.Offset; {
	case off == 0:
		b.WriteByte('Z')
	case off < 0:
		fmt.Fprintf(&b, "-%02d:%02d", -off/60, -off%60)
	default:
		fmt.Fprintf(&b, "+%02d:%02d", off/60, off%60)
	}
	return b.String()
}

// parseDateTime reads RFC 3339 with an offset. Fraction digits beyond
// milliseconds are truncated.
func parseDateTime(s string) (DateTime, bool) {
	if len(s) < 20 || s[10] != 'T' && s[10] != 't' || s[13] != ':' || s[16] != ':' {
		return DateTime{}, false
	}
	d, ok := parseDate(s[:10])
	hh, ok1 := atoiDigits(s[11:13])
	mm, ok2 := atoiDigits(s[14:16])
	ss, ok3 := atoiDigits(s[17:19])
	if !ok || !ok1 || !ok2 || !ok3 || hh > 23 || mm > 59 || ss > 59 {
		return DateTime{}, false
	}
	ms, rest, ok := parseFraction(s[19:])
	if !ok {
		return DateTime{}, false
	}
	offset, ok := parseOffset(rest)
	if !ok {
		return DateTime{}, false
	}
	return makeDateTime(int64(d), hh*msPerHour+mm*msPerMinute+ss*msPerSecond+ms, offset)
}

// parseFraction reads an optional fraction of 1–9 digits as milliseconds.
func parseFraction(s string) (ms int64, rest string, ok bool) {
	if !strings.HasPrefix(s, ".") {
		return 0, s, true
	}
	n := skipDigits(s, 1)
	frac := s[1:n]
	if frac == "" || len(frac) > 9 {
		return 0, "", false
	}
	ms, _ = atoiDigits((frac + "00")[:3])
	return ms, s[n:], true
}

// parseOffset reads Z or ±HH:MM as minutes.
func parseOffset(s string) (int32, bool) {
	if s == "Z" || s == "z" {
		return 0, true
	}
	if len(s) != 6 || s[0] != '+' && s[0] != '-' || s[3] != ':' {
		return 0, false
	}
	oh, ok1 := atoiDigits(s[1:3])
	om, ok2 := atoiDigits(s[4:6])
	if !ok1 || !ok2 || oh > 23 || om > 59 {
		return 0, false
	}
	offset := int32(oh*60 + om) //nolint:gosec // G115: at most 1439.
	if s[0] == '-' {
		offset = -offset
	}
	return offset, true
}

// String returns ISO 8601 [-]P[nD][T[nH][nM][n[.fff]S]], or PT0S.
func (d Duration) String() string {
	if d == 0 {
		return "PT0S"
	}
	var b strings.Builder
	ms := uint64(d) //nolint:gosec // G115: two's complement magnitude, handled below.
	if d < 0 {
		b.WriteByte('-')
		ms = uint64(-(d + 1)) + 1 //nolint:gosec // G115: −d without overflow for the smallest value.
	}
	b.WriteByte('P')
	if days := ms / msPerDay; days > 0 {
		b.WriteString(strconv.FormatUint(days, 10) + "D")
	}
	ms %= msPerDay
	if ms == 0 {
		return b.String()
	}
	b.WriteByte('T')
	if h := ms / msPerHour; h > 0 {
		b.WriteString(strconv.FormatUint(h, 10) + "H")
	}
	if m := ms % msPerHour / msPerMinute; m > 0 {
		b.WriteString(strconv.FormatUint(m, 10) + "M")
	}
	if s := ms % msPerMinute; s > 0 {
		b.WriteString(strconv.FormatUint(s/msPerSecond, 10))
		if frac := s % msPerSecond; frac > 0 {
			b.WriteString(strings.TrimRight(fmt.Sprintf(".%03d", frac), "0"))
		}
		b.WriteByte('S')
	}
	return b.String()
}

// parseDuration reads [-]P[nD][T[nH][nM][n[.f{1,3}]S]] with at least one
// component; days are 24 hours.
func parseDuration(s string) (Duration, bool) {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	if !strings.HasPrefix(s, "P") || len(s) < 3 {
		return 0, false
	}
	datePart, timePart, hasT := strings.Cut(s[1:], "T")
	if hasT && timePart == "" {
		return 0, false
	}
	acc := durationAcc{}
	if datePart != "" {
		n, ok := atoiDigits(strings.TrimSuffix(datePart, "D"))
		if !strings.HasSuffix(datePart, "D") || !ok || !acc.add(n, msPerDay) {
			return 0, false
		}
	}
	if !acc.time(timePart) || !acc.seen {
		return 0, false
	}
	if neg {
		return Duration(-acc.total), true
	}
	return Duration(acc.total), true
}

// durationAcc sums duration components without overflow.
type durationAcc struct {
	total int64
	seen  bool
}

// add adds n units, reporting overflow as false.
func (a *durationAcc) add(n, unit int64) bool {
	if n > (1<<63-1-a.total)/unit {
		return false
	}
	a.total += n * unit
	a.seen = true
	return true
}

// time reads [nH][nM][n[.fff]S] in order.
func (a *durationAcc) time(s string) bool {
	for _, u := range []struct {
		suffix byte
		ms     int64
	}{{'H', msPerHour}, {'M', msPerMinute}, {'S', msPerSecond}} {
		i := strings.IndexByte(s, u.suffix)
		if i < 0 {
			continue
		}
		num, frac, point := s[:i], "", false
		s = s[i+1:]
		if u.suffix == 'S' {
			num, frac, point = strings.Cut(num, ".")
		}
		n, ok := atoiDigits(num)
		if !ok || !a.add(n, u.ms) || point && !a.fraction(frac) {
			return false
		}
	}
	return s == ""
}

// fraction adds a millisecond fraction of one to three digits.
func (a *durationAcc) fraction(f string) bool {
	if _, ok := atoiDigits(f); !ok || len(f) > 3 {
		return false
	}
	ms, _ := atoiDigits((f + "00")[:3])
	return a.add(ms, 1)
}
