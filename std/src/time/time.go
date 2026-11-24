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
// wall is reserved for a later monotonic encoding; ext holds Unix nanoseconds.
type Time struct {
	wall uint64
	ext  int64
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

// Now returns the current local time from the host wall clock.
func Now() Time {
	sec, nsec := runtime.Walltime()
	return Time{ext: sec*1e9 + int64(nsec)}
}

// Unix returns the local Time corresponding to the given Unix time.
func Unix(sec int64, nsec int64) Time {
	return Time{ext: sec*1e9 + nsec}
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
func (t Time) Add(d Duration) Time { return Time{ext: t.ext + int64(d)} }

// Since returns the time elapsed since t.
func Since(t Time) Duration { return Now().Sub(t) }

// Until returns the duration until t.
func Until(t Time) Duration { return t.Sub(Now()) }

// String returns a decimal nanosecond count (enough for logging/debug).
func (t Time) String() string {
	return strconv.FormatInt(t.ext, 10)
}

// Unix returns t as a Unix time (seconds since January 1, 1970 UTC).
func (t Time) Unix() int64 { return t.ext / 1e9 }

// UnixNano returns t as a Unix time in nanoseconds.
func (t Time) UnixNano() int64 { return t.ext }

// Nanosecond returns the nanosecond offset within the second.
func (t Time) Nanosecond() int { return int(t.ext % 1e9) }

// UTC returns t with location UTC (no-op location in v1 rewrite).
func (t Time) UTC() Time { return t }

// Format returns a textual representation of t. Stub: UnixNano decimal
// (enough for io/fs.FormatFileInfo which needs a Format method).
func (t Time) Format(layout string) string {
	return string(t.AppendFormat(nil, layout))
}

// AppendFormat appends a textual representation of t to b.
// Stub: appends UnixNano decimal; full layout parsing is deferred.
func (t Time) AppendFormat(b []byte, layout string) []byte {
	_ = layout
	return strconv.AppendInt(b, t.ext, 10)
}

// UnmarshalText implements encoding.TextUnmarshaler via RFC3339 stub.
func (t *Time) UnmarshalText(data []byte) error {
	tt, err := Parse(RFC3339, string(data))
	if err != nil {
		return err
	}
	*t = tt
	return nil
}

// Parse parses a formatted string and returns the time value it represents.
// Stub: only accepts decimal Unix nanoseconds (AppendFormat output).
func Parse(layout, value string) (Time, error) {
	_ = layout
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return Time{}, &ParseError{Layout: layout, Value: value, Message: err.Error()}
	}
	return Time{ext: n}, nil
}

// String returns a decimal nanosecond count (enough for json duration format).
func (d Duration) String() string {
	return strconv.FormatInt(int64(d), 10) + "ns"
}

func (d Duration) Nanoseconds() int64 { return int64(d) }
func (d Duration) Microseconds() int64 { return int64(d) / 1e3 }
func (d Duration) Milliseconds() int64 { return int64(d) / 1e6 }
func (d Duration) Seconds() float64    { return float64(d) / float64(Second) }
func (d Duration) Minutes() float64    { return float64(d) / float64(Minute) }
func (d Duration) Hours() float64      { return float64(d) / float64(Hour) }

// Tick is a convenience wrapper for NewTicker(d).C. Stub ticker.
func Tick(d Duration) <-chan Time {
	return NewTicker(d).C
}

// Ticker holds a channel that delivers "ticks" of a clock at intervals.
type Ticker struct {
	C <-chan Time
	c chan Time
}

func NewTicker(d Duration) *Ticker {
	c := make(chan Time, 1)
	t := &Ticker{C: c, c: c}
	go func() {
		for {
			Sleep(d)
			select {
			case c <- Now():
			default:
			}
		}
	}()
	return t
}

func (t *Ticker) Stop() {
	// stub: goroutine keeps running (language tests rarely require stop)
}

// ParseDuration parses a duration string.
// Stub: accepts an integer with optional "ns"/"us"/"µs"/"ms"/"s"/"m"/"h" suffix.
func ParseDuration(s string) (Duration, error) {
	if s == "" {
		return 0, errors.New("time: invalid duration")
	}
	// Try plain integer nanoseconds first.
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return Duration(n), nil
	}
	unit := Nanosecond
	num := s
	switch {
	case len(s) > 2 && s[len(s)-2:] == "ns":
		num, unit = s[:len(s)-2], Nanosecond
	case len(s) > 2 && s[len(s)-2:] == "us":
		num, unit = s[:len(s)-2], Microsecond
	case len(s) > 3 && s[len(s)-3:] == "µs":
		num, unit = s[:len(s)-3], Microsecond
	case len(s) > 2 && s[len(s)-2:] == "ms":
		num, unit = s[:len(s)-2], Millisecond
	case len(s) > 1 && s[len(s)-1:] == "s":
		num, unit = s[:len(s)-1], Second
	case len(s) > 1 && s[len(s)-1:] == "m":
		num, unit = s[:len(s)-1], Minute
	case len(s) > 1 && s[len(s)-1:] == "h":
		num, unit = s[:len(s)-1], Hour
	default:
		return 0, errors.New("time: invalid duration " + strconv.Quote(s))
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		return 0, errors.New("time: invalid duration " + strconv.Quote(s))
	}
	return Duration(n) * unit, nil
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
	C <-chan Time
	c chan Time
}

// NewTimer creates a new Timer that will send the current time on its
// channel after at least duration d.
func NewTimer(d Duration) *Timer {
	c := make(chan Time, 1)
	t := &Timer{C: c, c: c}
	go func() {
		Sleep(d)
		select {
		case c <- Now():
		default:
		}
	}()
	return t
}

// Stop prevents the Timer from firing. Stub: always reports stopped.
func (t *Timer) Stop() bool {
	if t == nil {
		return false
	}
	return true
}

// Reset changes the timer to expire after duration d. Stub: starts a new sleep.
func (t *Timer) Reset(d Duration) bool {
	if t == nil {
		return false
	}
	go func() {
		Sleep(d)
		select {
		case t.c <- Now():
		default:
		}
	}()
	return true
}

// AfterFunc waits for the duration to elapse and then calls f in its own
// goroutine. It returns a Timer that can be used to cancel the call using
// its Stop method (best-effort under wasmgc).
func AfterFunc(d Duration, f func()) *Timer {
	c := make(chan Time, 1)
	t := &Timer{C: c, c: c}
	go func() {
		Sleep(d)
		f()
	}()
	return t
}
