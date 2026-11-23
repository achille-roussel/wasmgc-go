package time

import "runtime"

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

// Time represents an instant in time with nanosecond precision.
// wall is reserved for a later monotonic encoding; ext holds Unix nanoseconds.
type Time struct {
	wall uint64
	ext  int64
}

// Now returns the current local time from the host wall clock.
func Now() Time {
	sec, nsec := runtime.Walltime()
	return Time{ext: sec*1e9 + int64(nsec)}
}

// After reports whether the time instant t is after u.
func (t Time) After(u Time) bool { return t.ext > u.ext }

// Before reports whether the time instant t is before u.
func (t Time) Before(u Time) bool { return t.ext < u.ext }

// Equal reports whether t and u represent the same time instant.
func (t Time) Equal(u Time) bool { return t.ext == u.ext }

// Sub returns the duration t-u.
func (t Time) Sub(u Time) Duration { return Duration(t.ext - u.ext) }

// Since returns the time elapsed since t.
func Since(t Time) Duration { return Now().Sub(t) }

// Unix returns t as a Unix time (seconds since January 1, 1970 UTC).
func (t Time) Unix() int64 { return t.ext / 1e9 }

// UnixNano returns t as a Unix time in nanoseconds.
func (t Time) UnixNano() int64 { return t.ext }

// Sleep pauses the current goroutine for at least duration d.
func Sleep(d Duration) {
	runtime.TimeSleep(int64(d))
}

// After waits for the duration to elapse and then sends the current time
// on the returned channel.
func After(d Duration) <-chan Time {
	c := make(chan Time, 1)
	go func() {
		Sleep(d)
		c <- Now()
	}()
	return c
}
