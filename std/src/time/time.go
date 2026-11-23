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

// Time is a placeholder instant. Full wall-clock time is out of M7 scope.
type Time struct {
	wall uint64
	ext  int64
}

// Now returns a zero Time (no wall clock yet).
func Now() Time { return Time{} }

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
		c <- Time{}
	}()
	return c
}
