// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

// Layout-based Parse ported from upstream time/format.go (parse half).

var longDayNames = []string{
	"Sunday",
	"Monday",
	"Tuesday",
	"Wednesday",
	"Thursday",
	"Friday",
	"Saturday",
}

var shortDayNames = []string{
	"Sun",
	"Mon",
	"Tue",
	"Wed",
	"Thu",
	"Fri",
	"Sat",
}

var shortMonthNames = []string{
	"Jan",
	"Feb",
	"Mar",
	"Apr",
	"May",
	"Jun",
	"Jul",
	"Aug",
	"Sep",
	"Oct",
	"Nov",
	"Dec",
}

var longMonthNames = []string{
	"January",
	"February",
	"March",
	"April",
	"May",
	"June",
	"July",
	"August",
	"September",
	"October",
	"November",
	"December",
}

// match reports whether s1 and s2 match ignoring case.

func match(s1, s2 string) bool {
	for i := 0; i < len(s1); i++ {
		c1 := s1[i]
		c2 := s2[i]
		if c1 != c2 {
			// Switch to lower-case; 'a'-'A' is known to be a single bit.
			c1 |= 'a' - 'A'
			c2 |= 'a' - 'A'
			if c1 != c2 || c1 < 'a' || c1 > 'z' {
				return false
			}
		}
	}
	return true
}

func lookup(tab []string, val string) (int, string, error) {
	for i, v := range tab {
		if len(val) >= len(v) && match(val[:len(v)], v) {
			return i, val[len(v):], nil
		}
	}
	return -1, val, errBad
}

// appendInt appends the decimal form of x to b and returns the result.
// If the decimal form (excluding sign) is shorter than width, the result is padded with leading 0's.

func atoi(s string) (x int, err error) {
	neg := false
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	q, rem, err := leadingInt(s)
	x = int(q)
	if err != nil || len(rem) > 0 {
		return 0, errAtoi
	}
	if neg {
		x = -x
	}
	return x, nil
}

var errAtoi = errBad

func getnum(s string, fixed bool) (int, string, error) {
	if !isDigit(s, 0) {
		return 0, s, errBad
	}
	if !isDigit(s, 1) {
		if fixed {
			return 0, s, errBad
		}
		return int(s[0] - '0'), s[1:], nil
	}
	return int(s[0]-'0')*10 + int(s[1]-'0'), s[2:], nil
}

// getnum3 parses s[0:1], s[0:2], or s[0:3] (fixed forces s[0:3])
// as a decimal integer and returns the integer and the remainder
// of the string.
func getnum3(s string, fixed bool) (int, string, error) {
	var n, i int
	for i = 0; i < 3 && isDigit(s, i); i++ {
		n = n*10 + int(s[i]-'0')
	}
	if i == 0 || fixed && i != 3 {
		return 0, s, errBad
	}
	return n, s[i:], nil
}

func cutspace(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	return s
}

// skip removes the given prefix from value,
// treating runs of space characters as equivalent.
func skip(value, prefix string) (string, error) {
	for len(prefix) > 0 {
		if prefix[0] == ' ' {
			if len(value) > 0 && value[0] != ' ' {
				return value, errBad
			}
			prefix = cutspace(prefix)
			value = cutspace(value)
			continue
		}
		if len(value) == 0 || value[0] != prefix[0] {
			return value, errBad
		}
		prefix = prefix[1:]
		value = value[1:]
	}
	return value, nil
}

func parse(layout, value string, defaultLocation, local *Location) (Time, error) {
	alayout, avalue := layout, value
	rangeErrString := "" // set if a value is out of range
	amSet := false       // do we need to subtract 12 from the hour for midnight?
	pmSet := false       // do we need to add 12 to the hour?

	// Time being constructed.
	var (
		year       int
		month      int = -1
		day        int = -1
		yday       int = -1
		hour       int
		min        int
		sec        int
		nsec       int
		z          *Location
		zoneOffset int = -1
		zoneName   string
	)

	// Each iteration processes one std value.
	for {
		var err error
		prefix, std, suffix := nextStdChunk(layout)
		stdstr := layout[len(prefix) : len(layout)-len(suffix)]
		value, err = skip(value, prefix)
		if err != nil {
			return Time{}, newParseError(alayout, avalue, prefix, value, "")
		}
		if std == 0 {
			if len(value) != 0 {
				return Time{}, newParseError(alayout, avalue, "", value, ": extra text: "+quote(value))
			}
			break
		}
		layout = suffix
		var p string
		hold := value
		switch std & stdMask {
		case stdYear:
			if len(value) < 2 {
				err = errBad
				break
			}
			p, value = value[0:2], value[2:]
			year, err = atoi(p)
			if err != nil {
				break
			}
			if year >= 69 { // Unix time starts Dec 31 1969 in some time zones
				year += 1900
			} else {
				year += 2000
			}
		case stdLongYear:
			if len(value) < 4 || !isDigit(value, 0) {
				err = errBad
				break
			}
			p, value = value[0:4], value[4:]
			year, err = atoi(p)
		case stdMonth:
			month, value, err = lookup(shortMonthNames, value)
			month++
		case stdLongMonth:
			month, value, err = lookup(longMonthNames, value)
			month++
		case stdNumMonth, stdZeroMonth:
			month, value, err = getnum(value, std == stdZeroMonth)
			if err == nil && (month <= 0 || 12 < month) {
				rangeErrString = "month"
			}
		case stdWeekDay:
			// Ignore weekday except for error checking.
			_, value, err = lookup(shortDayNames, value)
		case stdLongWeekDay:
			_, value, err = lookup(longDayNames, value)
		case stdDay, stdUnderDay, stdZeroDay:
			if std == stdUnderDay && len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			day, value, err = getnum(value, std == stdZeroDay)
			// Note that we allow any one- or two-digit day here.
			// The month, day, year combination is validated after we've completed parsing.
		case stdUnderYearDay, stdZeroYearDay:
			for i := 0; i < 2; i++ {
				if std == stdUnderYearDay && len(value) > 0 && value[0] == ' ' {
					value = value[1:]
				}
			}
			yday, value, err = getnum3(value, std == stdZeroYearDay)
			// Note that we allow any one-, two-, or three-digit year-day here.
			// The year-day, year combination is validated after we've completed parsing.
		case stdHour:
			hour, value, err = getnum(value, false)
			if hour < 0 || 24 <= hour {
				rangeErrString = "hour"
			}
		case stdHour12, stdZeroHour12:
			hour, value, err = getnum(value, std == stdZeroHour12)
			if hour < 0 || 12 < hour {
				rangeErrString = "hour"
			}
		case stdMinute, stdZeroMinute:
			min, value, err = getnum(value, std == stdZeroMinute)
			if min < 0 || 60 <= min {
				rangeErrString = "minute"
			}
		case stdSecond, stdZeroSecond:
			sec, value, err = getnum(value, std == stdZeroSecond)
			if err != nil {
				break
			}
			if sec < 0 || 60 <= sec {
				rangeErrString = "second"
				break
			}
			// Special case: do we have a fractional second but no
			// fractional second in the format?
			if len(value) >= 2 && commaOrPeriod(value[0]) && isDigit(value, 1) {
				_, std, _ = nextStdChunk(layout)
				std &= stdMask
				if std == stdFracSecond0 || std == stdFracSecond9 {
					// Fractional second in the layout; proceed normally
					break
				}
				// No fractional second in the layout but we have one in the input.
				n := 2
				for ; n < len(value) && isDigit(value, n); n++ {
				}
				nsec, rangeErrString, err = parseNanoseconds(value, n)
				value = value[n:]
			}
		case stdPM:
			if len(value) < 2 {
				err = errBad
				break
			}
			p, value = value[0:2], value[2:]
			switch p {
			case "PM":
				pmSet = true
			case "AM":
				amSet = true
			default:
				err = errBad
			}
		case stdpm:
			if len(value) < 2 {
				err = errBad
				break
			}
			p, value = value[0:2], value[2:]
			switch p {
			case "pm":
				pmSet = true
			case "am":
				amSet = true
			default:
				err = errBad
			}
		case stdISO8601TZ, stdISO8601ShortTZ, stdISO8601ColonTZ, stdISO8601SecondsTZ, stdISO8601ColonSecondsTZ:
			if len(value) >= 1 && value[0] == 'Z' {
				value = value[1:]
				z = UTC
				break
			}
			fallthrough
		case stdNumTZ, stdNumShortTZ, stdNumColonTZ, stdNumSecondsTz, stdNumColonSecondsTZ:
			var sign, hour, min, seconds string
			if std == stdISO8601ColonTZ || std == stdNumColonTZ {
				if len(value) < 6 {
					err = errBad
					break
				}
				if value[3] != ':' {
					err = errBad
					break
				}
				sign, hour, min, seconds, value = value[0:1], value[1:3], value[4:6], "00", value[6:]
			} else if std == stdNumShortTZ || std == stdISO8601ShortTZ {
				if len(value) < 3 {
					err = errBad
					break
				}
				sign, hour, min, seconds, value = value[0:1], value[1:3], "00", "00", value[3:]
			} else if std == stdISO8601ColonSecondsTZ || std == stdNumColonSecondsTZ {
				if len(value) < 9 {
					err = errBad
					break
				}
				if value[3] != ':' || value[6] != ':' {
					err = errBad
					break
				}
				sign, hour, min, seconds, value = value[0:1], value[1:3], value[4:6], value[7:9], value[9:]
			} else if std == stdISO8601SecondsTZ || std == stdNumSecondsTz {
				if len(value) < 7 {
					err = errBad
					break
				}
				sign, hour, min, seconds, value = value[0:1], value[1:3], value[3:5], value[5:7], value[7:]
			} else {
				if len(value) < 5 {
					err = errBad
					break
				}
				sign, hour, min, seconds, value = value[0:1], value[1:3], value[3:5], "00", value[5:]
			}
			var hr, mm, ss int
			hr, _, err = getnum(hour, true)
			if err == nil {
				mm, _, err = getnum(min, true)
				if err == nil {
					ss, _, err = getnum(seconds, true)
				}
			}

			// The range test use > rather than >=,
			// as some people do write offsets of 24 hours
			// or 60 minutes or 60 seconds.
			if hr > 24 {
				rangeErrString = "time zone offset hour"
			}
			if mm > 60 {
				rangeErrString = "time zone offset minute"
			}
			if ss > 60 {
				rangeErrString = "time zone offset second"
			}

			zoneOffset = (hr*60+mm)*60 + ss // offset is in seconds
			switch sign[0] {
			case '+':
			case '-':
				zoneOffset = -zoneOffset
			default:
				err = errBad
			}
		case stdTZ:
			// Does it look like a time zone?
			if len(value) >= 3 && value[0:3] == "UTC" {
				z = UTC
				value = value[3:]
				break
			}
			n, ok := parseTimeZone(value)
			if !ok {
				err = errBad
				break
			}
			zoneName, value = value[:n], value[n:]

		case stdFracSecond0:
			// stdFracSecond0 requires the exact number of digits as specified in
			// the layout.
			ndigit := 1 + digitsLen(std)
			if len(value) < ndigit {
				err = errBad
				break
			}
			nsec, rangeErrString, err = parseNanoseconds(value, ndigit)
			value = value[ndigit:]

		case stdFracSecond9:
			if len(value) < 2 || !commaOrPeriod(value[0]) || value[1] < '0' || '9' < value[1] {
				// Fractional second omitted.
				break
			}
			// Take any number of digits, even more than asked for,
			// because it is what the stdSecond case would do.
			i := 0
			for i+1 < len(value) && '0' <= value[i+1] && value[i+1] <= '9' {
				i++
			}
			nsec, rangeErrString, err = parseNanoseconds(value, 1+i)
			value = value[1+i:]
		}
		if rangeErrString != "" {
			return Time{}, newParseError(alayout, avalue, stdstr, value, ": "+rangeErrString+" out of range")
		}
		if err != nil {
			return Time{}, newParseError(alayout, avalue, stdstr, hold, "")
		}
	}
	if pmSet && hour < 12 {
		hour += 12
	} else if amSet && hour == 12 {
		hour = 0
	}

	// Convert yday to day, month.
	if yday >= 0 {
		var d int
		var m int
		if isLeap(year) {
			if yday == 31+29 {
				m = int(February)
				d = 29
			} else if yday > 31+29 {
				yday--
			}
		}
		if yday < 1 || yday > 365 {
			return Time{}, newParseError(alayout, avalue, "", value, ": day-of-year out of range")
		}
		if m == 0 {
			m = (yday-1)/31 + 1
			if daysBefore(Month(m+1)) < yday {
				m++
			}
			d = yday - daysBefore(Month(m))
		}
		// If month, day already seen, yday's m, d must match.
		// Otherwise, set them from m, d.
		if month >= 0 && month != m {
			return Time{}, newParseError(alayout, avalue, "", value, ": day-of-year does not match month")
		}
		month = m
		if day >= 0 && day != d {
			return Time{}, newParseError(alayout, avalue, "", value, ": day-of-year does not match day")
		}
		day = d
	} else {
		if month < 0 {
			month = int(January)
		}
		if day < 0 {
			day = 1
		}
	}

	// Validate the day of the month.
	if day < 1 || day > daysIn(Month(month), year) {
		return Time{}, newParseError(alayout, avalue, "", value, ": day out of range")
	}

	if z != nil {
		return Date(year, Month(month), day, hour, min, sec, nsec, z), nil
	}

	if zoneOffset != -1 {
		t := Date(year, Month(month), day, hour, min, sec, nsec, UTC)
		t.addSec(-int64(zoneOffset))

		// Look for local zone with the given offset.
		// If that zone was in effect at the given time, use it.
		name, offset, _, _, _ := local.lookup(t.unixSec())
		if offset == zoneOffset && (zoneName == "" || name == zoneName) {
			t.setLoc(local)
			return t, nil
		}

		// Otherwise create fake zone to record offset.
		zoneNameCopy := zoneName
		t.setLoc(FixedZone(zoneNameCopy, zoneOffset))
		return t, nil
	}

	if zoneName != "" {
		t := Date(year, Month(month), day, hour, min, sec, nsec, UTC)
		// Look for local zone with the given offset.
		// If that zone was in effect at the given time, use it.
		offset, ok := local.lookupName(zoneName, t.unixSec())
		if ok {
			t.addSec(-int64(offset))
			t.setLoc(local)
			return t, nil
		}

		// Otherwise, create fake zone with unknown offset.
		if len(zoneName) > 3 && zoneName[:3] == "GMT" {
			offset, _ = atoi(zoneName[3:]) // Guaranteed OK by parseGMT.
			offset *= 3600
		}
		zoneNameCopy := zoneName
		t.setLoc(FixedZone(zoneNameCopy, offset))
		return t, nil
	}

	// Otherwise, fall back to default.
	return Date(year, Month(month), day, hour, min, sec, nsec, defaultLocation), nil
}

func parseTimeZone(value string) (length int, ok bool) {
	if len(value) < 3 {
		return 0, false
	}
	// Special case 1: ChST and MeST are the only zones with a lower-case letter.
	if len(value) >= 4 && (value[:4] == "ChST" || value[:4] == "MeST") {
		return 4, true
	}
	// Special case 2: GMT may have an hour offset; treat it specially.
	if value[:3] == "GMT" {
		length = parseGMT(value)
		return length, true
	}
	// Special Case 3: Some time zones are not named, but have +/-00 format
	if value[0] == '+' || value[0] == '-' {
		length = parseSignedOffset(value)
		ok := length > 0 // parseSignedOffset returns 0 in case of bad input
		return length, ok
	}
	// How many upper-case letters are there? Need at least three, at most five.
	var nUpper int
	for nUpper = 0; nUpper < 6; nUpper++ {
		if nUpper >= len(value) {
			break
		}
		if c := value[nUpper]; c < 'A' || 'Z' < c {
			break
		}
	}
	switch nUpper {
	case 0, 1, 2, 6:
		return 0, false
	case 5: // Must end in T to match.
		if value[4] == 'T' {
			return 5, true
		}
	case 4:
		// Must end in T, except one special case.
		if value[3] == 'T' || value[:4] == "WITA" {
			return 4, true
		}
	case 3:
		return 3, true
	}
	return 0, false
}

// parseGMT parses a GMT time zone. The input string is known to start "GMT".
// The function checks whether that is followed by a sign and a number in the
// range -23 through +23 excluding zero.
func parseGMT(value string) int {
	value = value[3:]
	if len(value) == 0 {
		return 3
	}

	return 3 + parseSignedOffset(value)
}

// parseSignedOffset parses a signed timezone offset (e.g. "+03" or "-04").
// The function checks for a signed number in the range -23 through +23 excluding zero.
// Returns length of the found offset string or 0 otherwise.
func parseSignedOffset(value string) int {
	sign := value[0]
	if sign != '-' && sign != '+' {
		return 0
	}
	x, rem, err := leadingInt(value[1:])

	// fail if nothing consumed by leadingInt
	if err != nil || value[1:] == rem {
		return 0
	}
	if x > 23 {
		return 0
	}
	return len(value) - len(rem)
}

func commaOrPeriod(b byte) bool {
	return b == '.' || b == ','
}

func parseNanoseconds(value string, nbytes int) (ns int, rangeErrString string, err error) {
	if nbytes == 0 || !commaOrPeriod(value[0]) {
		err = errBad
		return
	}
	if nbytes > 10 {
		value = value[:10]
		nbytes = 10
	}
	if ns, err = atoi(value[1:nbytes]); err != nil {
		return
	}
	if ns < 0 {
		rangeErrString = "fractional second"
		return
	}
	scaleDigits := 10 - nbytes
	for i := 0; i < scaleDigits; i++ {
		ns *= 10
	}
	return
}

func newParseError(layout, value, layoutElem, valueElem, message string) *ParseError {
	return &ParseError{Layout: layout, Value: value, LayoutElem: layoutElem, ValueElem: valueElem, Message: message}
}


func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// daysBefore returns the number of days in a non-leap year before month m.
// daysBefore(December+1) returns 365.
func daysBefore(m Month) int {
	adj := 0
	if m >= March {
		adj = -2
	}
	return (214*int(m)-211)/7 + adj
}

