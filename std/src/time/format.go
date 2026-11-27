// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

// Layout-based formatting ported from upstream time/format.go (format half only).
// Uses Zone() + location-aware Date/Clock instead of locabs/absSeconds.

const (
	_                        = iota
	stdLongMonth             = iota + stdNeedDate  // "January"
	stdMonth                                       // "Jan"
	stdNumMonth                                    // "1"
	stdZeroMonth                                   // "01"
	stdLongWeekDay                                 // "Monday"
	stdWeekDay                                     // "Mon"
	stdDay                                         // "2"
	stdUnderDay                                    // "_2"
	stdZeroDay                                     // "02"
	stdUnderYearDay          = iota + stdNeedYday  // "__2"
	stdZeroYearDay                                 // "002"
	stdHour                  = iota + stdNeedClock // "15"
	stdHour12                                      // "3"
	stdZeroHour12                                  // "03"
	stdMinute                                      // "4"
	stdZeroMinute                                  // "04"
	stdSecond                                      // "5"
	stdZeroSecond                                  // "05"
	stdLongYear              = iota + stdNeedDate  // "2006"
	stdYear                                        // "06"
	stdPM                    = iota + stdNeedClock // "PM"
	stdpm                                          // "pm"
	stdTZ                    = iota                // "MST"
	stdISO8601TZ                                   // "Z0700"  // prints Z for UTC
	stdISO8601SecondsTZ                            // "Z070000"
	stdISO8601ShortTZ                              // "Z07"
	stdISO8601ColonTZ                              // "Z07:00" // prints Z for UTC
	stdISO8601ColonSecondsTZ                       // "Z07:00:00"
	stdNumTZ                                       // "-0700"  // always numeric
	stdNumSecondsTz                                // "-070000"
	stdNumShortTZ                                  // "-07"    // always numeric
	stdNumColonTZ                                  // "-07:00" // always numeric
	stdNumColonSecondsTZ                           // "-07:00:00"
	stdFracSecond0                                 // ".0", ".00", ... , trailing zeros included
	stdFracSecond9                                 // ".9", ".99", ..., trailing zeros omitted

	stdNeedDate       = 1 << 8             // need month, day, year
	stdNeedYday       = 1 << 9             // need yday
	stdNeedClock      = 1 << 10            // need hour, minute, second
	stdArgShift       = 16                 // extra argument in high bits, above low stdArgShift
	stdSeparatorShift = 28                 // extra argument in high 4 bits for fractional second separators
	stdMask           = 1<<stdArgShift - 1 // mask out argument
)

// std0x records the std values for "01", "02", ..., "06".
var std0x = [...]int{stdZeroMonth, stdZeroDay, stdZeroHour12, stdZeroMinute, stdZeroSecond, stdYear}

// startsWithLowerCase reports whether the string has a lower-case letter at the beginning.
// Its purpose is to prevent matching strings like "Month" when looking for "Mon".
func startsWithLowerCase(str string) bool {
	if len(str) == 0 {
		return false
	}
	c := str[0]
	return 'a' <= c && c <= 'z'
}

// nextStdChunk finds the first occurrence of a std string in

func nextStdChunk(layout string) (prefix string, std int, suffix string) {
	for i := 0; i < len(layout); i++ {
		switch c := int(layout[i]); c {
		case 'J': // January, Jan
			if len(layout) >= i+3 && layout[i:i+3] == "Jan" {
				if len(layout) >= i+7 && layout[i:i+7] == "January" {
					return layout[0:i], stdLongMonth, layout[i+7:]
				}
				if !startsWithLowerCase(layout[i+3:]) {
					return layout[0:i], stdMonth, layout[i+3:]
				}
			}

		case 'M': // Monday, Mon, MST
			if len(layout) >= i+3 {
				if layout[i:i+3] == "Mon" {
					if len(layout) >= i+6 && layout[i:i+6] == "Monday" {
						return layout[0:i], stdLongWeekDay, layout[i+6:]
					}
					if !startsWithLowerCase(layout[i+3:]) {
						return layout[0:i], stdWeekDay, layout[i+3:]
					}
				}
				if layout[i:i+3] == "MST" {
					return layout[0:i], stdTZ, layout[i+3:]
				}
			}

		case '0': // 01, 02, 03, 04, 05, 06, 002
			if len(layout) >= i+2 && '1' <= layout[i+1] && layout[i+1] <= '6' {
				return layout[0:i], std0x[layout[i+1]-'1'], layout[i+2:]
			}
			if len(layout) >= i+3 && layout[i+1] == '0' && layout[i+2] == '2' {
				return layout[0:i], stdZeroYearDay, layout[i+3:]
			}

		case '1': // 15, 1
			if len(layout) >= i+2 && layout[i+1] == '5' {
				return layout[0:i], stdHour, layout[i+2:]
			}
			return layout[0:i], stdNumMonth, layout[i+1:]

		case '2': // 2006, 2
			if len(layout) >= i+4 && layout[i:i+4] == "2006" {
				return layout[0:i], stdLongYear, layout[i+4:]
			}
			return layout[0:i], stdDay, layout[i+1:]

		case '_': // _2, _2006, __2
			if len(layout) >= i+2 && layout[i+1] == '2' {
				// _2006 is really a literal _, followed by stdLongYear
				if len(layout) >= i+5 && layout[i+1:i+5] == "2006" {
					return layout[0 : i+1], stdLongYear, layout[i+5:]
				}
				return layout[0:i], stdUnderDay, layout[i+2:]
			}
			if len(layout) >= i+3 && layout[i+1] == '_' && layout[i+2] == '2' {
				return layout[0:i], stdUnderYearDay, layout[i+3:]
			}

		case '3':
			return layout[0:i], stdHour12, layout[i+1:]

		case '4':
			return layout[0:i], stdMinute, layout[i+1:]

		case '5':
			return layout[0:i], stdSecond, layout[i+1:]

		case 'P': // PM
			if len(layout) >= i+2 && layout[i+1] == 'M' {
				return layout[0:i], stdPM, layout[i+2:]
			}

		case 'p': // pm
			if len(layout) >= i+2 && layout[i+1] == 'm' {
				return layout[0:i], stdpm, layout[i+2:]
			}

		case '-': // -070000, -07:00:00, -0700, -07:00, -07
			if len(layout) >= i+7 && layout[i:i+7] == "-070000" {
				return layout[0:i], stdNumSecondsTz, layout[i+7:]
			}
			if len(layout) >= i+9 && layout[i:i+9] == "-07:00:00" {
				return layout[0:i], stdNumColonSecondsTZ, layout[i+9:]
			}
			if len(layout) >= i+5 && layout[i:i+5] == "-0700" {
				return layout[0:i], stdNumTZ, layout[i+5:]
			}
			if len(layout) >= i+6 && layout[i:i+6] == "-07:00" {
				return layout[0:i], stdNumColonTZ, layout[i+6:]
			}
			if len(layout) >= i+3 && layout[i:i+3] == "-07" {
				return layout[0:i], stdNumShortTZ, layout[i+3:]
			}

		case 'Z': // Z070000, Z07:00:00, Z0700, Z07:00,
			if len(layout) >= i+7 && layout[i:i+7] == "Z070000" {
				return layout[0:i], stdISO8601SecondsTZ, layout[i+7:]
			}
			if len(layout) >= i+9 && layout[i:i+9] == "Z07:00:00" {
				return layout[0:i], stdISO8601ColonSecondsTZ, layout[i+9:]
			}
			if len(layout) >= i+5 && layout[i:i+5] == "Z0700" {
				return layout[0:i], stdISO8601TZ, layout[i+5:]
			}
			if len(layout) >= i+6 && layout[i:i+6] == "Z07:00" {
				return layout[0:i], stdISO8601ColonTZ, layout[i+6:]
			}
			if len(layout) >= i+3 && layout[i:i+3] == "Z07" {
				return layout[0:i], stdISO8601ShortTZ, layout[i+3:]
			}

		case '.', ',': // ,000, or .000, or ,999, or .999 - repeated digits for fractional seconds.
			if i+1 < len(layout) && (layout[i+1] == '0' || layout[i+1] == '9') {
				ch := layout[i+1]
				j := i + 1
				for j < len(layout) && layout[j] == ch {
					j++
				}
				// String of digits must end here - only fractional second is all digits.
				if !isDigit(layout, j) {
					code := stdFracSecond0
					if layout[i+1] == '9' {
						code = stdFracSecond9
					}
					std := stdFracSecond(code, j-(i+1), c)
					return layout[0:i], std, layout[j:]
				}
			}
		}
	}
	return layout, 0, ""
}


func appendInt(b []byte, x int, width int) []byte {
	u := uint(x)
	if x < 0 {
		b = append(b, '-')
		u = uint(-x)
	}

	// 2-digit and 4-digit fields are the most common in time formats.
	utod := func(u uint) byte { return '0' + byte(u) }
	switch {
	case width == 2 && u < 1e2:
		return append(b, utod(u/1e1), utod(u%1e1))
	case width == 4 && u < 1e4:
		return append(b, utod(u/1e3), utod(u/1e2%1e1), utod(u/1e1%1e1), utod(u%1e1))
	}

	// Compute the number of decimal digits.
	var n int
	if u == 0 {
		n = 1
	}
	for u2 := u; u2 > 0; u2 /= 10 {
		n++
	}

	// Add 0-padding.
	for pad := width - n; pad > 0; pad-- {
		b = append(b, '0')
	}

	// Ensure capacity.
	if len(b)+n <= cap(b) {
		b = b[:len(b)+n]
	} else {
		b = append(b, make([]byte, n)...)
	}

	// Assemble decimal in reverse order.
	i := len(b) - 1
	for u >= 10 && i > 0 {
		q := u / 10
		b[i] = utod(u - q*10)
		u = q
		i--
	}
	b[i] = utod(u)
	return b
}


func stdFracSecond(code, n, c int) int {
	// Use 0xfff to make the failure case even more absurd.
	if c == '.' {
		return code | ((n & 0xfff) << stdArgShift)
	}
	return code | ((n & 0xfff) << stdArgShift) | 1<<stdSeparatorShift
}

func digitsLen(std int) int {
	return (std >> stdArgShift) & 0xfff
}

func separator(std int) byte {
	if (std >> stdSeparatorShift) == 0 {
		return '.'
	}
	return ','
}

// appendNano appends a fractional second, as nanoseconds, to b
// and returns the result. The nanosec must be within [0, 999999999].
func appendNano(b []byte, nanosec int, std int) []byte {
	trim := std&stdMask == stdFracSecond9
	n := digitsLen(std)
	if trim && (n == 0 || nanosec == 0) {
		return b
	}
	dot := separator(std)
	b = append(b, dot)
	b = appendInt(b, nanosec, 9)
	if n < 9 {
		b = b[:len(b)-9+n]
	}
	if trim {
		for len(b) > 0 && b[len(b)-1] == '0' {
			b = b[:len(b)-1]
		}
		if len(b) > 0 && b[len(b)-1] == dot {
			b = b[:len(b)-1]
		}
	}
	return b
}

// String returns the time formatted using the format string
//
//	"2006-01-02 15:04:05.999999999 -0700 MST"
//
// If the time has a monotonic clock reading, the returned string
// includes a final field "m=±<value>", where value is the monotonic
// clock reading formatted as a decimal number of seconds.
//
// The returned string is meant for debugging; for a stable serialized
// representation, use t.MarshalText, t.MarshalBinary, or t.Format

func isDigit(s string, i int) bool {
	if len(s) <= i {
		return false
	}
	c := s[i]
	return '0' <= c && c <= '9'
}

// AppendFormat appends a textual representation of t to b according to layout.
func (t Time) AppendFormat(b []byte, layout string) []byte {
	switch layout {
	case RFC3339:
		return t.appendFormatRFC3339(b, false)
	case RFC3339Nano:
		return t.appendFormatRFC3339(b, true)
	}
	return t.appendFormat(b, layout)
}

func (t Time) appendFormat(b []byte, layout string) []byte {
	name, offset := t.Zone()

	var (
		year  int = -1
		month Month
		day   int
		yday  int = -1
		hour  int = -1
		min   int
		sec   int
	)

	for layout != "" {
		prefix, std, suffix := nextStdChunk(layout)
		if prefix != "" {
			b = append(b, prefix...)
		}
		if std == 0 {
			break
		}
		layout = suffix

		if year < 0 && std&stdNeedDate != 0 {
			year, month, day = t.Date()
		}
		if yday < 0 && std&stdNeedYday != 0 {
			yday = t.YearDay()
		}
		if hour < 0 && std&stdNeedClock != 0 {
			hour, min, sec = t.Clock()
		}

		switch std & stdMask {
		case stdYear:
			y := year
			if y < 0 {
				y = -y
			}
			b = appendInt(b, y%100, 2)
		case stdLongYear:
			b = appendInt(b, year, 4)
		case stdMonth:
			b = append(b, month.String()[:3]...)
		case stdLongMonth:
			b = append(b, month.String()...)
		case stdNumMonth:
			b = appendInt(b, int(month), 0)
		case stdZeroMonth:
			b = appendInt(b, int(month), 2)
		case stdWeekDay:
			b = append(b, t.Weekday().String()[:3]...)
		case stdLongWeekDay:
			b = append(b, t.Weekday().String()...)
		case stdDay:
			b = appendInt(b, day, 0)
		case stdUnderDay:
			if day < 10 {
				b = append(b, ' ')
			}
			b = appendInt(b, day, 0)
		case stdZeroDay:
			b = appendInt(b, day, 2)
		case stdUnderYearDay:
			if yday < 100 {
				b = append(b, ' ')
				if yday < 10 {
					b = append(b, ' ')
				}
			}
			b = appendInt(b, yday, 0)
		case stdZeroYearDay:
			b = appendInt(b, yday, 3)
		case stdHour:
			b = appendInt(b, hour, 2)
		case stdHour12:
			hr := hour % 12
			if hr == 0 {
				hr = 12
			}
			b = appendInt(b, hr, 0)
		case stdZeroHour12:
			hr := hour % 12
			if hr == 0 {
				hr = 12
			}
			b = appendInt(b, hr, 2)
		case stdMinute:
			b = appendInt(b, min, 0)
		case stdZeroMinute:
			b = appendInt(b, min, 2)
		case stdSecond:
			b = appendInt(b, sec, 0)
		case stdZeroSecond:
			b = appendInt(b, sec, 2)
		case stdPM:
			if hour >= 12 {
				b = append(b, "PM"...)
			} else {
				b = append(b, "AM"...)
			}
		case stdpm:
			if hour >= 12 {
				b = append(b, "pm"...)
			} else {
				b = append(b, "am"...)
			}
		case stdISO8601TZ, stdISO8601ColonTZ, stdISO8601SecondsTZ, stdISO8601ShortTZ, stdISO8601ColonSecondsTZ, stdNumTZ, stdNumColonTZ, stdNumSecondsTz, stdNumShortTZ, stdNumColonSecondsTZ:
			if offset == 0 && (std == stdISO8601TZ || std == stdISO8601ColonTZ || std == stdISO8601SecondsTZ || std == stdISO8601ShortTZ || std == stdISO8601ColonSecondsTZ) {
				b = append(b, 'Z')
				break
			}
			zone := offset / 60
			absoffset := offset
			if zone < 0 {
				b = append(b, '-')
				zone = -zone
				absoffset = -absoffset
			} else {
				b = append(b, '+')
			}
			b = appendInt(b, zone/60, 2)
			if std == stdISO8601ColonTZ || std == stdNumColonTZ || std == stdISO8601ColonSecondsTZ || std == stdNumColonSecondsTZ {
				b = append(b, ':')
			}
			if std != stdNumShortTZ && std != stdISO8601ShortTZ {
				b = appendInt(b, zone%60, 2)
			}
			if std == stdISO8601SecondsTZ || std == stdNumSecondsTz || std == stdNumColonSecondsTZ || std == stdISO8601ColonSecondsTZ {
				if std == stdNumColonSecondsTZ || std == stdISO8601ColonSecondsTZ {
					b = append(b, ':')
				}
				b = appendInt(b, absoffset%60, 2)
			}
		case stdTZ:
			if name != "" {
				b = append(b, name...)
				break
			}
			zone := offset / 60
			if zone < 0 {
				b = append(b, '-')
				zone = -zone
			} else {
				b = append(b, '+')
			}
			b = appendInt(b, zone/60, 2)
			b = appendInt(b, zone%60, 2)
		case stdFracSecond0, stdFracSecond9:
			b = appendNano(b, t.Nanosecond(), std)
		}
	}
	return b
}

// quote matches upstream time.quote (export_test exposes Quote).
func quote(s string) string { return quoteGoString(s) }

// Export-test aliases (NextStdChunk, StdChunkNames, DaysIn, …) live in
// std/overlay/time/export_test.go so fmt hello does not keep the chunk-name
// map. LoadTest injects that overlay after dropping upstream export_test.
