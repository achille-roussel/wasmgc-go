package time

import (
	"errors"
	"runtime"
	"strconv"
)

// A Duration represents the elapsed time between two instants as an int64
// nanosecond count.
type Duration int64

const (
	Nanosecond  Duration = 1
	Microsecond          = 1000 * Nanosecond
	Millisecond          = 1000 * Microsecond
	Second               = 1000 * Millisecond
	Minute               = 60 * Second
	Hour                 = 60 * Minute
)

// Layout strings used by Time.Format / Parse (subset needed by encoding/json).
const (
	Layout      = "01/02 03:04:05PM '06 -0700"
	ANSIC       = "Mon Jan _2 15:04:05 2006"
	UnixDate    = "Mon Jan _2 15:04:05 MST 2006"
	RubyDate    = "Mon Jan 02 15:04:05 -0700 2006"
	RFC822      = "02 Jan 06 15:04 MST"
	RFC822Z     = "02 Jan 06 15:04 -0700"
	RFC850      = "Monday, 02-Jan-06 15:04:05 MST"
	RFC1123     = "Mon, 02 Jan 2006 15:04:05 MST"
	RFC1123Z    = "Mon, 02 Jan 2006 15:04:05 -0700"
	RFC3339     = "2006-01-02T15:04:05Z07:00"
	RFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"
	Kitchen     = "3:04PM"
	Stamp       = "Jan _2 15:04:05"
	StampMilli  = "Jan _2 15:04:05.000"
	StampMicro  = "Jan _2 15:04:05.000000"
	StampNano   = "Jan _2 15:04:05.000000000"
	DateTime    = "2006-01-02 15:04:05"
	DateOnly    = "2006-01-02"
	TimeOnly    = "15:04:05"
)

// Time represents an instant in time with nanosecond precision.
// wall distinguishes the zero Time from Unix(0,0); ext holds Unix nanoseconds.
type Time struct {
	wall uint64
	ext  int64
	loc  *Location
}

// ParseError describes a problem parsing a time string.
type ParseError struct {
	Layout     string
	Value      string
	LayoutElem string
	ValueElem  string
	Message    string
}

func (e *ParseError) Error() string {
	if e == nil {
		return "time: parse error"
	}
	if e.Message != "" {
		return "parsing time " + strconv.Quote(e.Value) + " as " + strconv.Quote(e.Layout) + ": " + e.Message
	}
	return "parsing time " + strconv.Quote(e.Value) + " as " + strconv.Quote(e.Layout)
}

// hasExt marks a Time that holds an absolute Unix-nano ext value.
// Distinguishes Unix(0,0) (1970) from the zero Time (year 1).
// hasCivil marks a Time whose ext is absolute seconds since year 1 UTC
// (for dates outside the Unix-nano int64 range); nsec lives in wall>>8.
const (
	hasExt   = 1
	hasCivil = 2
)

// absoluteToInternal is seconds between year 1 and Unix epoch (1970-01-01).
const absoluteToInternal int64 = 62135596800

// maxUnixNanoSec is the largest |unix seconds| that fit in int64 nanoseconds.
const maxUnixNanoSec int64 = 9223372036

// Now returns the current local time from the host wall clock.
func Now() Time {
	sec, nsec := runtime.Walltime()
	return Time{wall: hasExt, ext: sec*1e9 + int64(nsec)}
}

// Unix returns the local Time corresponding to the given Unix time.
func Unix(sec int64, nsec int64) Time {
	return Time{wall: hasExt, ext: sec*1e9 + nsec}
}

// UnixMilli returns the local Time corresponding to the given Unix time
// in milliseconds since January 1, 1970 UTC.
func UnixMilli(msec int64) Time {
	return Unix(msec/1e3, (msec%1e3)*1e6)
}

// UnixMicro returns the local Time corresponding to the given Unix time
// in microseconds since January 1, 1970 UTC.
func UnixMicro(usec int64) Time {
	return Unix(usec/1e6, (usec%1e6)*1e3)
}

// After reports whether the time instant t is after u.
func (t Time) After(u Time) bool { return t.ext > u.ext }

// Before reports whether the time instant t is before u.
func (t Time) Before(u Time) bool { return t.ext < u.ext }

// Equal reports whether t and u represent the same time instant.
func (t Time) Equal(u Time) bool { return t.ext == u.ext }

// Sub returns the duration t-u.
func (t Time) Sub(u Time) Duration { return Duration(t.ext - u.ext) }

// Add returns the time t+d.
func (t Time) Add(d Duration) Time {
	wall := t.wall
	if wall == 0 {
		wall = hasExt
	}
	return Time{wall: wall, ext: t.ext + int64(d)}
}

// Since returns the time elapsed since t.
func Since(t Time) Duration { return Now().Sub(t) }

// Until returns the duration until t.
func Until(t Time) Duration { return t.Sub(Now()) }

// String returns a decimal nanosecond count (enough for logging/debug).
func (t Time) String() string {
	return strconv.FormatInt(t.ext, 10)
}

// Unix returns t as a Unix time (seconds since January 1, 1970 UTC).
func (t Time) Unix() int64 { return t.unixSec() }

// UnixMilli returns t as a Unix time in milliseconds.
func (t Time) UnixMilli() int64 {
	return t.unixSec()*1e3 + int64(t.Nanosecond())/1e6
}

// UnixMicro returns t as a Unix time in microseconds.
func (t Time) UnixMicro() int64 {
	return t.unixSec()*1e6 + int64(t.Nanosecond())/1e3
}

// UnixNano returns t as a Unix time in nanoseconds.
func (t Time) UnixNano() int64 {
	if t.wall&hasCivil != 0 {
		return t.unixSec()*1e9 + int64(t.Nanosecond())
	}
	return t.ext
}

// Nanosecond returns the nanosecond offset within the second.
func (t Time) Nanosecond() int {
	if t.wall&hasCivil != 0 {
		return int(t.wall >> 8)
	}
	return int(t.ext % 1e9)
}

// UTC returns t with location UTC (no-op location in v1 rewrite).
func (t Time) UTC() Time { return t }

// Format returns a textual representation of t.
func (t Time) Format(layout string) string {
	return string(t.AppendFormat(nil, layout))
}

// AppendFormat appends a textual representation of t to b.
// Thin subset: Kitchen, DateOnly, TimeOnly, RFC3339/RFC3339Nano (always Z).
// Other layouts fall back to UnixNano decimal (keeps joint time binary small).
func (t Time) AppendFormat(b []byte, layout string) []byte {
	switch layout {
	case Kitchen, "03:04PM": // Kitchen == "3:04PM"
		h, m, _ := t.Clock()
		ap := "AM"
		h12 := h % 12
		if h12 == 0 {
			h12 = 12
		}
		if h >= 12 {
			ap = "PM"
		}
		if layout == "03:04PM" && h12 < 10 {
			b = append(b, '0')
		}
		b = strconv.AppendInt(b, int64(h12), 10)
		b = append(b, ':')
		if m < 10 {
			b = append(b, '0')
		}
		b = strconv.AppendInt(b, int64(m), 10)
		return append(b, ap...)
	case TimeOnly:
		h, m, s := t.Clock()
		b = append2(b, h)
		b = append(b, ':')
		b = append2(b, m)
		b = append(b, ':')
		return append2(b, s)
	case DateOnly:
		y, mo, d := t.Date()
		b = append4(b, y)
		b = append(b, '-')
		b = append2(b, int(mo))
		b = append(b, '-')
		return append2(b, d)
	case RFC3339, RFC3339Nano:
		y, mo, d := t.Date()
		h, mi, s := t.Clock()
		b = append4(b, y)
		b = append(b, '-')
		b = append2(b, int(mo))
		b = append(b, '-')
		b = append2(b, d)
		b = append(b, 'T')
		b = append2(b, h)
		b = append(b, ':')
		b = append2(b, mi)
		b = append(b, ':')
		b = append2(b, s)
		if layout == RFC3339Nano {
			b = append(b, '.')
			ns := t.Nanosecond()
			for div := 100000000; div >= 1; div /= 10 {
				b = append(b, byte('0'+ns/div%10))
			}
		}
		return append(b, 'Z')
	default:
		return strconv.AppendInt(b, t.ext, 10)
	}
}

func append2(b []byte, n int) []byte {
	if n < 0 {
		n = 0
	}
	if n < 10 {
		b = append(b, '0')
	}
	return strconv.AppendInt(b, int64(n), 10)
}

func append4(b []byte, n int) []byte {
	if n < 0 {
		b = append(b, '-')
		n = -n
	}
	if n < 1000 {
		b = append(b, '0')
	}
	if n < 100 {
		b = append(b, '0')
	}
	if n < 10 {
		b = append(b, '0')
	}
	return strconv.AppendInt(b, int64(n), 10)
}

// AppendText appends RFC3339 text of t to b (stub via AppendFormat).
func (t Time) AppendText(b []byte) ([]byte, error) {
	return t.AppendFormat(b, RFC3339), nil
}

// MarshalText implements encoding.TextMarshaler.
func (t Time) MarshalText() ([]byte, error) {
	return t.AppendText(nil)
}

// UnmarshalText implements encoding.TextUnmarshaler via Parse stub.
func (t *Time) UnmarshalText(data []byte) error {
	tt, err := Parse(RFC3339, string(data))
	if err != nil {
		return err
	}
	*t = tt
	return nil
}

// MarshalJSON implements json.Marshaler (quoted AppendFormat).
func (t Time) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, 24)
	b = append(b, '"')
	b = t.AppendFormat(b, RFC3339)
	b = append(b, '"')
	return b, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (t *Time) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return &ParseError{Layout: RFC3339, Value: string(data), Message: "input is not a JSON string"}
	}
	return t.UnmarshalText(data[1 : len(data)-1])
}

// AppendBinary appends the binary encoding of t to b.
func (t Time) AppendBinary(b []byte) ([]byte, error) {
	enc, err := t.marshalBinaryBuf()
	if err != nil {
		return b, err
	}
	return append(b, enc...), nil
}

func (t Time) marshalBinaryBuf() ([]byte, error) {
	ext := t.ext
	_, offset := t.Zone()
	buf := make([]byte, 14)
	buf[0] = 1 // version
	buf[1] = byte(t.wall & 0xff)
	for i := 0; i < 8; i++ {
		buf[2+i] = byte(ext >> (56 - 8*i))
	}
	uoff := uint32(offset)
	buf[10] = byte(uoff >> 24)
	buf[11] = byte(uoff >> 16)
	buf[12] = byte(uoff >> 8)
	buf[13] = byte(uoff)
	return buf, nil
}

// MarshalBinary implements encoding.BinaryMarshaler (stub: wall+ext+offset).
func (t Time) MarshalBinary() ([]byte, error) {
	return t.marshalBinaryBuf()
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (t *Time) UnmarshalBinary(data []byte) error {
	if len(data) < 9 {
		return errors.New("Time.UnmarshalBinary: too short")
	}
	var ext int64
	wall := uint64(0)
	off := 1
	if len(data) >= 10 {
		wall = uint64(data[1])
		off = 2
	}
	for i := 0; i < 8; i++ {
		if off+i >= len(data) {
			break
		}
		ext = (ext << 8) | int64(data[off+i])
	}
	t.wall = wall
	t.ext = ext
	t.loc = nil
	if len(data) >= 14 {
		uoff := uint32(data[10])<<24 | uint32(data[11])<<16 | uint32(data[12])<<8 | uint32(data[13])
		offset := int(int32(uoff))
		if offset != 0 {
			t.loc = FixedZone("", offset)
		}
	}
	return nil
}

// GobEncode implements the gob.GobEncoder interface.
func (t Time) GobEncode() ([]byte, error) { return t.MarshalBinary() }

// GobDecode implements the gob.GobDecoder interface.
func (t *Time) GobDecode(data []byte) error { return t.UnmarshalBinary(data) }

// Parse parses a formatted string and returns the time value it represents.
// Thin: Kitchen / "3:04PM" / "03:04PM" only; other layouts fall back to
// decimal Unix nanoseconds (AppendFormat default output).
func Parse(layout, value string) (Time, error) {
	switch layout {
	case Kitchen, "03:04PM":
		return parseKitchen(layout, value)
	default:
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return Time{}, &ParseError{Layout: layout, Value: value, Message: err.Error()}
		}
		return Time{wall: hasExt, ext: n}, nil
	}
}

// parseKitchen parses "3:04PM" / "03:04PM" / "12:00AM" style values.
func parseKitchen(layout, value string) (Time, error) {
	if len(value) < 6 {
		return Time{}, &ParseError{Layout: layout, Value: value, Message: "too short"}
	}
	i := 0
	for i < len(value) && value[i] != ':' {
		i++
	}
	if i == 0 || i >= len(value) {
		return Time{}, &ParseError{Layout: layout, Value: value, Message: "missing colon"}
	}
	h64, err := strconv.ParseInt(value[:i], 10, 64)
	if err != nil {
		return Time{}, &ParseError{Layout: layout, Value: value, Message: err.Error()}
	}
	rest := value[i+1:]
	if len(rest) < 4 {
		return Time{}, &ParseError{Layout: layout, Value: value, Message: "too short"}
	}
	m64, err := strconv.ParseInt(rest[:2], 10, 64)
	if err != nil {
		return Time{}, &ParseError{Layout: layout, Value: value, Message: err.Error()}
	}
	ap := rest[2:]
	h := int(h64)
	switch ap {
	case "PM", "pm":
		if h != 12 {
			h += 12
		}
	case "AM", "am":
		if h == 12 {
			h = 0
		}
	default:
		return Time{}, &ParseError{Layout: layout, Value: value, Message: "bad AM/PM"}
	}
	// Year 2000 avoids unreliable year-0 civil math in the thin stub.
	return Date(2000, January, 1, h, int(m64), 0, 0, UTC), nil
}

const (
	minDuration Duration = -1 << 63
	maxDuration Duration = 1<<63 - 1
)

// String returns a string representing the duration in the form "72h3m0.5s".
// Leading zero units are omitted. As a special case, durations less than one
// second format with a smaller unit (ms, µs, or ns) to avoid zeros.
func (d Duration) String() string {
	var buf [32]byte
	w := len(buf)
	u := uint64(d)
	neg := d < 0
	if neg {
		u = -u
	}
	if u < uint64(Second) {
		var prec int
		w--
		buf[w] = 's'
		w--
		switch {
		case u == 0:
			buf[w] = '0'
			return string(buf[w:])
		case u < uint64(Microsecond):
			prec = 0
			buf[w] = 'n'
		case u < uint64(Millisecond):
			prec = 3
			w--
			copy(buf[w:], "µ")
		default:
			prec = 6
			buf[w] = 'm'
		}
		w, u = fmtFrac(buf[:w], u, prec)
		w = fmtInt(buf[:w], u)
	} else {
		w--
		buf[w] = 's'
		w, u = fmtFrac(buf[:w], u, 9)
		w = fmtInt(buf[:w], u%60)
		u /= 60
		if u > 0 {
			w--
			buf[w] = 'm'
			w = fmtInt(buf[:w], u%60)
			u /= 60
			if u > 0 {
				w--
				buf[w] = 'h'
				w = fmtInt(buf[:w], u)
			}
		}
	}
	if neg {
		w--
		buf[w] = '-'
	}
	return string(buf[w:])
}

func fmtFrac(buf []byte, v uint64, prec int) (nw int, nv uint64) {
	w := len(buf)
	print := false
	for i := 0; i < prec; i++ {
		digit := v % 10
		print = print || digit != 0
		if print {
			w--
			buf[w] = byte(digit) + '0'
		}
		v /= 10
	}
	if print {
		w--
		buf[w] = '.'
	}
	return w, v
}

func fmtInt(buf []byte, v uint64) int {
	w := len(buf)
	if v == 0 {
		w--
		buf[w] = '0'
	} else {
		for v > 0 {
			w--
			buf[w] = byte(v%10) + '0'
			v /= 10
		}
	}
	return w
}

func (d Duration) Nanoseconds() int64 { return int64(d) }
func (d Duration) Microseconds() int64 { return int64(d) / 1e3 }
func (d Duration) Milliseconds() int64 { return int64(d) / 1e6 }
func (d Duration) Seconds() float64    { return float64(d) / float64(Second) }
func (d Duration) Minutes() float64    { return float64(d) / float64(Minute) }
func (d Duration) Hours() float64      { return float64(d) / float64(Hour) }

// Truncate returns the result of rounding d toward zero to a multiple of m.
func (d Duration) Truncate(m Duration) Duration {
	if m <= 0 {
		return d
	}
	return d - d%m
}

func lessThanHalf(x, y Duration) bool {
	return uint64(x)+uint64(x) < uint64(y)
}

// Round returns the result of rounding d to the nearest multiple of m.
func (d Duration) Round(m Duration) Duration {
	if m <= 0 {
		return d
	}
	r := d % m
	if d < 0 {
		r = -r
		if lessThanHalf(r, m) {
			return d + r
		}
		if d1 := d - m + r; d1 < d {
			return d1
		}
		return minDuration
	}
	if lessThanHalf(r, m) {
		return d - r
	}
	if d1 := d + m - r; d1 > d {
		return d1
	}
	return maxDuration
}

// Abs returns the absolute value of d.
func (d Duration) Abs() Duration {
	switch {
	case d >= 0:
		return d
	case d == minDuration:
		return maxDuration
	default:
		return -d
	}
}

// Tick is a convenience wrapper for NewTicker(d).C.
// Like upstream, a non-positive duration returns nil (does not panic).
func Tick(d Duration) <-chan Time {
	if d <= 0 {
		return nil
	}
	return NewTicker(d).C
}

// Ticker holds a channel that delivers "ticks" of a clock at intervals.
type Ticker struct {
	C    <-chan Time
	c    chan Time
	stop chan struct{}
}

func NewTicker(d Duration) *Ticker {
	if d <= 0 {
		panic("non-positive interval for NewTicker")
	}
	c := make(chan Time, 1)
	stop := make(chan struct{})
	t := &Ticker{C: c, c: c, stop: stop}
	go tickerLoop(t, d, stop)
	return t
}

func tickerLoop(t *Ticker, d Duration, stop <-chan struct{}) {
	for {
		Sleep(d)
		select {
		case <-stop:
			return
		default:
		}
		select {
		case <-stop:
			return
		case t.c <- Now():
		default:
		}
	}
}

func (t *Ticker) Stop() {
	if t == nil || t.stop == nil {
		return
	}
	select {
	case <-t.stop:
		// already stopped
	default:
		close(t.stop)
	}
}

// Reset stops a ticker and resets its period to the specified duration.
func (t *Ticker) Reset(d Duration) {
	if t == nil || t.c == nil {
		panic("time: Reset called on uninitialized Ticker")
	}
	if d <= 0 {
		panic("non-positive interval for Ticker.Reset")
	}
	t.Stop()
	stop := make(chan struct{})
	t.stop = stop
	go tickerLoop(t, d, stop)
}

type parseDurationError struct {
	message string
	value   string
}

func (e *parseDurationError) Error() string {
	return "time: " + e.message + " " + strconv.Quote(e.value)
}

// unitLookup avoids a package-level map so programs that import time
// (via os→io/fs) without calling ParseDuration do not pull map runtime.
func unitLookup(u string) (uint64, bool) {
	switch u {
	case "ns":
		return uint64(Nanosecond), true
	case "us", "µs", "μs":
		return uint64(Microsecond), true
	case "ms":
		return uint64(Millisecond), true
	case "s":
		return uint64(Second), true
	case "m":
		return uint64(Minute), true
	case "h":
		return uint64(Hour), true
	}
	return 0, false
}

func leadingInt(s string) (x uint64, rem string, err error) {
	i := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		if x > (1<<63-1)/10 {
			return 0, s[:], errors.New("time: bad [0-9]*")
		}
		y := x*10 + uint64(c-'0')
		if y < x {
			return 0, s[:], errors.New("time: bad [0-9]*")
		}
		x = y
	}
	return x, s[i:], nil
}

func leadingFraction(s string) (x uint64, scale float64, rem string) {
	i := 0
	scale = 1
	overflow := false
	for ; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			break
		}
		if overflow {
			continue
		}
		if x > (1<<63-1)/10 {
			overflow = true
			continue
		}
		y := x*10 + uint64(c-'0')
		if y > 1<<63 {
			overflow = true
			continue
		}
		x = y
		scale *= 10
	}
	return x, scale, s[i:]
}

// ParseDuration parses a duration string such as "300ms", "-1.5h" or "2h45m".
func ParseDuration(s string) (Duration, error) {
	orig := s
	var d uint64
	neg := false
	if s != "" {
		c := s[0]
		if c == '-' || c == '+' {
			neg = c == '-'
			s = s[1:]
		}
	}
	if s == "0" {
		return 0, nil
	}
	if s == "" {
		return 0, &parseDurationError{"invalid duration", orig}
	}
	for s != "" {
		var (
			v, f  uint64
			scale float64 = 1
		)
		if !(s[0] == '.' || '0' <= s[0] && s[0] <= '9') {
			return 0, &parseDurationError{"invalid duration", orig}
		}
		pl := len(s)
		var err error
		v, s, err = leadingInt(s)
		if err != nil {
			return 0, &parseDurationError{"invalid duration", orig}
		}
		pre := pl != len(s)
		post := false
		if s != "" && s[0] == '.' {
			s = s[1:]
			pl := len(s)
			f, scale, s = leadingFraction(s)
			post = pl != len(s)
		}
		if !pre && !post {
			return 0, &parseDurationError{"invalid duration", orig}
		}
		i := 0
		for ; i < len(s); i++ {
			c := s[i]
			if c == '.' || '0' <= c && c <= '9' {
				break
			}
		}
		if i == 0 {
			return 0, &parseDurationError{"missing unit in duration", orig}
		}
		u := s[:i]
		s = s[i:]
		unit, ok := unitLookup(u)
		if !ok {
			return 0, &parseDurationError{"unknown unit " + strconv.Quote(u) + " in duration", orig}
		}
		if v > 1<<63/unit {
			return 0, &parseDurationError{"invalid duration", orig}
		}
		v *= unit
		if f > 0 {
			v += uint64(float64(f) * (float64(unit) / scale))
			if v > 1<<63 {
				return 0, &parseDurationError{"invalid duration", orig}
			}
		}
		d += v
		if d > 1<<63 {
			return 0, &parseDurationError{"invalid duration", orig}
		}
	}
	if neg {
		return -Duration(d), nil
	}
	if d > 1<<63-1 {
		return 0, &parseDurationError{"invalid duration", orig}
	}
	return Duration(d), nil
}

// Sleep pauses the current goroutine for at least duration d.
func Sleep(d Duration) {
	runtime.TimeSleep(int64(d))
}

// After waits for the duration to elapse and then sends the current time
// on the returned channel.
func After(d Duration) <-chan Time {
	return NewTimer(d).C
}

// Timer represents a single event. When the Timer expires, the current
// time will be sent on C unless the Timer was created by AfterFunc.
type Timer struct {
	C    <-chan Time
	c    chan Time
	stop chan struct{}
	f    func() // non-nil for AfterFunc timers
}

// NewTimer creates a new Timer that will send the current time on its
// channel after at least duration d.
func NewTimer(d Duration) *Timer {
	c := make(chan Time, 1)
	stop := make(chan struct{})
	t := &Timer{C: c, c: c, stop: stop}
	go timerLoop(t, d, stop)
	return t
}

func timerLoop(t *Timer, d Duration, stop <-chan struct{}) {
	Sleep(d)
	select {
	case <-stop:
		return
	default:
	}
	if t.f != nil {
		t.f()
		return
	}
	select {
	case <-stop:
	case t.c <- Now():
	default:
	}
}

// Stop prevents the Timer from firing. Reports whether the timer was active.
func (t *Timer) Stop() bool {
	if t == nil || t.c == nil {
		panic("time: Stop called on uninitialized Timer")
	}
	if t.stop == nil {
		return false
	}
	select {
	case <-t.stop:
		return false
	default:
		close(t.stop)
		return true
	}
}

// Reset changes the timer to expire after duration d.
func (t *Timer) Reset(d Duration) bool {
	if t == nil || t.c == nil {
		panic("time: Reset called on uninitialized Timer")
	}
	active := t.Stop()
	stop := make(chan struct{})
	t.stop = stop
	go timerLoop(t, d, stop)
	return active
}

// AfterFunc waits for the duration to elapse and then calls f in its own
// goroutine. It returns a Timer that can be used to cancel the call using
// its Stop method.
func AfterFunc(d Duration, f func()) *Timer {
	c := make(chan Time, 1)
	stop := make(chan struct{})
	t := &Timer{C: c, c: c, stop: stop, f: f}
	go timerLoop(t, d, stop)
	return t
}

// Month is a month of the year (January = 1, …).
type Month int

const (
	January Month = 1 + iota
	February
	March
	April
	May
	June
	July
	August
	September
	October
	November
	December
)

var monthNames = [...]string{
	"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}

func (m Month) String() string {
	if January <= m && m <= December {
		return monthNames[m-1]
	}
	return "%!Month(" + strconv.Itoa(int(m)) + ")"
}

// Month returns the month of the year specified by t (UTC stub).
// Zero Time is January 1, year 1 UTC — matches host Go.
func (t Time) Month() Month {
	_, m, _ := t.date()
	return m
}

// Weekday is a day of the week (Sunday = 0, …).
type Weekday int

const (
	Sunday Weekday = iota
	Monday
	Tuesday
	Wednesday
	Thursday
	Friday
	Saturday
)

var weekdayNames = [...]string{
	"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday",
}

func (d Weekday) String() string {
	if Sunday <= d && d <= Saturday {
		return weekdayNames[d]
	}
	return "%!Weekday(" + strconv.Itoa(int(d)) + ")"
}

// IsZero reports whether t represents the zero time instant.
func (t Time) IsZero() bool { return t.ext == 0 && t.wall == 0 }

// Year / Day / Weekday / Hour / Minute / Second / Date / Clock are UTC stubs
// derived from Unix nanoseconds (ext). Zero Time → year 1, Jan 1.
func (t Time) Year() int {
	y, _, _ := t.date()
	return y
}
func (t Time) Day() int {
	_, _, d := t.date()
	return d
}
func (t Time) Weekday() Weekday {
	// Special-case zero Time: Go's zero is Monday 0001-01-01.
	if t.IsZero() {
		return Monday
	}
	sec := t.unixSec()
	// 1970-01-01 was Thursday.
	const epochWeekday = Thursday
	days := sec / 86400
	if sec%86400 < 0 {
		days--
	}
	w := (int64(epochWeekday) + days) % 7
	if w < 0 {
		w += 7
	}
	return Weekday(w)
}
// unixSec returns seconds since Unix epoch (handles hasCivil and hasExt).
func (t Time) unixSec() int64 {
	if t.wall&hasCivil != 0 {
		return t.ext - absoluteToInternal
	}
	return t.ext / 1e9
}

func (t Time) Hour() int {
	if t.IsZero() {
		return 0
	}
	s := t.unixSec() % 86400
	if s < 0 {
		s += 86400
	}
	return int(s / 3600)
}
func (t Time) Minute() int {
	if t.IsZero() {
		return 0
	}
	s := t.unixSec() % 86400
	if s < 0 {
		s += 86400
	}
	return int((s % 3600) / 60)
}
func (t Time) Second() int {
	if t.IsZero() {
		return 0
	}
	s := t.unixSec() % 86400
	if s < 0 {
		s += 86400
	}
	return int(s % 60)
}
func (t Time) Clock() (hour, min, sec int) {
	return t.Hour(), t.Minute(), t.Second()
}
func (t Time) Date() (year int, month Month, day int) {
	return t.date()
}

// Location is a stub timezone.
type Location struct {
	name   string
	offset int // seconds east of UTC
}

var (
	utcLoc   = Location{name: "UTC"}
	localLoc = Location{name: "Local"}
	UTC      = &utcLoc
	Local    = &localLoc
)

func (t Time) Location() *Location {
	if t.loc == nil {
		return UTC
	}
	return t.loc
}
func (t Time) Local() Time { return t }
func (t Time) In(loc *Location) Time {
	t.loc = loc
	return t
}
func (l *Location) String() string {
	if l == nil {
		return "UTC"
	}
	return l.name
}
func (t Time) Zone() (name string, offset int) {
	if t.loc != nil {
		return t.loc.name, t.loc.offset
	}
	return "UTC", 0
}

// FixedZone returns a Location that always uses the given zone name and
// offset (seconds east of UTC).
func FixedZone(name string, offset int) *Location {
	return &Location{name: name, offset: offset}
}

// Compare compares t with u. Returns -1, 0, or +1.
func (t Time) Compare(u Time) int {
	if t.ext < u.ext {
		return -1
	}
	if t.ext > u.ext {
		return 1
	}
	return 0
}

// AddDate returns the time corresponding to adding the given number of years,
// months, and days to t. Months are normalized; days are applied as 24h steps
// (UTC-only rewrite, no DST).
func (t Time) AddDate(years int, months int, days int) Time {
	y, m, d := t.Date()
	h, mi, s := t.Clock()
	y += years
	miMonth := int(m) + months
	for miMonth > 12 {
		y++
		miMonth -= 12
	}
	for miMonth <= 0 {
		y--
		miMonth += 12
	}
	base := Date(y, Month(miMonth), d, h, mi, s, t.Nanosecond(), t.Location())
	return base.Add(Duration(days) * 24 * Hour)
}

// Truncate returns t rounded down to a multiple of d (toward zero wall time).
func (t Time) Truncate(d Duration) Time {
	if d <= 0 {
		return t
	}
	r := t.ext % int64(d)
	if r < 0 {
		r += int64(d)
	}
	return Time{wall: hasExt, ext: t.ext - r}
}

// Round returns t rounded to the nearest multiple of d.
func (t Time) Round(d Duration) Time {
	if d <= 0 {
		return t
	}
	r := t.ext % int64(d)
	if r < 0 {
		r += int64(d)
	}
	if r+r < int64(d) {
		return Time{wall: hasExt, ext: t.ext - r}
	}
	return Time{wall: hasExt, ext: t.ext + (int64(d) - r)}
}

// GoString returns a Go-syntax representation of t.
func (t Time) GoString() string {
	monthNames := []string{
		"", "time.January", "time.February", "time.March", "time.April", "time.May", "time.June",
		"time.July", "time.August", "time.September", "time.October", "time.November", "time.December",
	}
	m := t.Month()
	ms := strconv.Itoa(int(m))
	if int(m) >= 1 && int(m) <= 12 {
		ms = monthNames[int(m)]
	}
	loc := "time.UTC"
	if l := t.Location(); l == Local {
		loc = "time.Local"
	} else if l != nil && l != UTC && l.name != "" && l.name != "UTC" {
		loc = "time.Location(" + quoteGoString(l.name) + ")"
	}
	return "time.Date(" + strconv.Itoa(t.Year()) + ", " + ms +
		", " + strconv.Itoa(t.Day()) + ", " + strconv.Itoa(t.Hour()) + ", " +
		strconv.Itoa(t.Minute()) + ", " + strconv.Itoa(t.Second()) + ", " +
		strconv.Itoa(t.Nanosecond()) + ", " + loc + ")"
}

// quoteGoString quotes s like strconv.Quote but escapes every non-ASCII byte
// as \xHH (matches upstream time.GoString expectations for Location names).
func quoteGoString(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' || c == '"':
			b = append(b, '\\', c)
		case c == '\n':
			b = append(b, '\\', 'n')
		case c == '\t':
			b = append(b, '\\', 't')
		case c >= 0x20 && c < 0x7f:
			b = append(b, c)
		default:
			const hex = "0123456789abcdef"
			b = append(b, '\\', 'x', hex[c>>4], hex[c&0xf])
		}
	}
	b = append(b, '"')
	return string(b)
}

// YearDay returns the day of the year in [1,365] (or 366 for leap years).
func (t Time) YearDay() int {
	y, m, d := t.Date()
	return int(daysFromCivil(y, int(m), d)-daysFromCivil(y, 1, 1)) + 1
}

// ISOWeek returns the ISO 8601 year and week number.
// Uses the Thursday of the current week to determine year and week.
func (t Time) ISOWeek() (year, week int) {
	wd := int(t.Weekday()) // Sunday=0 … Saturday=6
	isoWD := (wd + 6) % 7  // Monday=0 … Sunday=6
	thu := t.Add(Duration(3-isoWD) * 24 * Hour)
	year = thu.Year()
	week = (thu.YearDay()-1)/7 + 1
	return year, week
}

// Date returns the Time corresponding to the given calendar arguments in loc.
// Stub: ignores loc timezone rules; uses Unix nanos when they fit in int64,
// otherwise hasCivil absolute seconds (year 0 / far dates for Format tests).
func Date(year int, month Month, day, hour, min, sec, nsec int, loc *Location) Time {
	days := daysFromCivil(year, int(month), day) - daysFromCivil(1970, 1, 1)
	unixSec := days*86400 + int64(hour)*3600 + int64(min)*60 + int64(sec)
	if unixSec >= -maxUnixNanoSec && unixSec <= maxUnixNanoSec {
		return Time{wall: hasExt, ext: unixSec*1e9 + int64(nsec), loc: loc}
	}
	absSec := unixSec + absoluteToInternal
	return Time{wall: hasCivil | (uint64(uint32(nsec)) << 8), ext: absSec, loc: loc}
}

func (t Time) date() (year int, month Month, day int) {
	if t.IsZero() {
		return 1, January, 1
	}
	sec := t.unixSec()
	days := sec / 86400
	if sec%86400 < 0 {
		days--
	}
	abs := days + daysFromCivil(1970, 1, 1)
	y, m, d := civilFromDays(abs)
	return y, Month(m), d
}

// --- civil date helpers (proleptic Gregorian, UTC) ---

// daysFromCivil returns days since 0000-03-01 (Howard Hinnant algorithm).
func daysFromCivil(y, m, d int) int64 {
	y0 := int64(y)
	if m <= 2 {
		y0--
	}
	era := y0 / 400
	if y0 < 0 && y0%400 != 0 {
		era--
	}
	yoe := y0 - era*400
	var m0 int64
	if m > 2 {
		m0 = int64(m - 3)
	} else {
		m0 = int64(m + 9)
	}
	doy := (153*m0+2)/5 + int64(d-1)
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468 // shift so 1970-01-01 aligns via civilFromDays
}

func civilFromDays(z int64) (y, m, d int) {
	z += 719468
	era := z / 146097
	if z < 0 && z%146097 != 0 {
		era--
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y0 := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = int(doy - (153*mp+2)/5 + 1)
	if mp < 10 {
		m = int(mp + 3)
	} else {
		m = int(mp - 9)
	}
	y = int(y0)
	if m <= 2 {
		y++
	}
	return
}
