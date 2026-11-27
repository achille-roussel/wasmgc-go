// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package time

import "errors"

func (t Time) appendFormatRFC3339(b []byte, nanos bool) []byte {
	year, month, day := t.Date()
	b = appendInt(b, year, 4)
	b = append(b, '-')
	b = appendInt(b, int(month), 2)
	b = append(b, '-')
	b = appendInt(b, day, 2)
	b = append(b, 'T')
	hour, min, sec := t.Clock()
	b = appendInt(b, hour, 2)
	b = append(b, ':')
	b = appendInt(b, min, 2)
	b = append(b, ':')
	b = appendInt(b, sec, 2)
	if nanos {
		std := stdFracSecond(stdFracSecond9, 9, '.')
		b = appendNano(b, t.Nanosecond(), std)
	}
	_, offset := t.Zone()
	if offset == 0 {
		return append(b, 'Z')
	}
	zone := offset / 60
	if zone < 0 {
		b = append(b, '-')
		zone = -zone
	} else {
		b = append(b, '+')
	}
	b = appendInt(b, zone/60, 2)
	b = append(b, ':')
	b = appendInt(b, zone%60, 2)
	return b
}

func (t Time) appendStrictRFC3339(b []byte) ([]byte, error) {
	n0 := len(b)
	b = t.appendFormatRFC3339(b, true)
	num2 := func(b []byte) byte { return 10*(b[0]-'0') + (b[1] - '0') }
	switch {
	case b[n0+len("9999")] != '-':
		return b, errors.New("year outside of range [0,9999]")
	case b[len(b)-1] != 'Z':
		c := b[len(b)-len("Z07:00")]
		if ('0' <= c && c <= '9') || num2(b[len(b)-len("07:00"):]) >= 24 {
			return b, errors.New("timezone hour outside of range [0,23]")
		}
	}
	return b, nil
}

func daysIn(m Month, year int) int {
	if m == February {
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	}
	if m == April || m == June || m == September || m == November {
		return 30
	}
	return 31
}


var errBad = errors.New("bad value for field")

func parseRFC3339String(s string) (Time, bool) {
	ok := true
	parseUint := func(ss string, min, max int) (x int) {
		for i := 0; i < len(ss); i++ {
			c := ss[i]
			if c < '0' || '9' < c {
				ok = false
				return min
			}
			x = x*10 + int(c) - '0'
		}
		if x < min || max < x {
			ok = false
			return min
		}
		return x
	}
	if len(s) < len("2006-01-02T15:04:05") {
		return Time{}, false
	}
	year := parseUint(s[0:4], 0, 9999)
	month := parseUint(s[5:7], 1, 12)
	day := parseUint(s[8:10], 1, daysIn(Month(month), year))
	hour := parseUint(s[11:13], 0, 23)
	min := parseUint(s[14:16], 0, 59)
	sec := parseUint(s[17:19], 0, 59)
	if !ok || !(s[4] == '-' && s[7] == '-' && s[10] == 'T' && s[13] == ':' && s[16] == ':') {
		return Time{}, false
	}
	s = s[19:]
	var nsec int
	if len(s) >= 2 && s[0] == '.' && isDigit(s, 1) {
		n := 2
		for ; n < len(s) && isDigit(s, n); n++ {
		}
		nsec, _, _ = parseNanoseconds(s, n)
		s = s[n:]
	}
	t := Date(year, Month(month), day, hour, min, sec, nsec, UTC)
	if len(s) == 1 && s[0] == 'Z' {
		return t, true
	}
	if len(s) != len("-07:00") {
		return Time{}, false
	}
	hr := parseUint(s[1:3], 0, 23)
	mm := parseUint(s[4:6], 0, 59)
	if !ok || !((s[0] == '-' || s[0] == '+') && s[3] == ':') {
		return Time{}, false
	}
	zoneOffset := (hr*60 + mm) * 60
	if s[0] == '-' {
		zoneOffset *= -1
	}
	// Instant is wall-clock in the offset zone; Date(..., UTC) treated wall as UTC.
	// Adjust absolute time so wall in zoneOffset matches parsed fields.
	t = Date(year, Month(month), day, hour, min, sec, nsec, FixedZone("", zoneOffset))
	return t, true
}

func parseStrictRFC3339(b []byte) (Time, error) {
	t, ok := parseRFC3339String(string(b))
	if !ok {
		return Time{}, &ParseError{Layout: RFC3339, Value: string(b), Message: "invalid RFC3339"}
	}
	return t, nil
}
