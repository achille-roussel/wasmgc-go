// Package log is a wasmgc rewrite of the standard logger.
// Upstream needs time.Time.Date/Clock which the thin time rewrite lacks;
// language tests only need Print*/Fatal* writing to stderr.
package log

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// Flags that may be or'ed together to control prefix formatting.
const (
	Ldate         = 1 << iota
	Ltime
	Lmicroseconds
	Llongfile
	Lshortfile
	LUTC
	Lmsgprefix
	LstdFlags     = Ldate | Ltime
)

// A Logger generates lines of output to an io.Writer.
type Logger struct {
	mu     sync.Mutex
	out    io.Writer
	prefix string
	flag   int
}

var std = New(os.Stderr, "", LstdFlags)

// New creates a Logger that writes to out with the given prefix and flag.
func New(out io.Writer, prefix string, flag int) *Logger {
	return &Logger{out: out, prefix: prefix, flag: flag}
}

// SetOutput sets the output destination for the standard logger.
func SetOutput(w io.Writer) { std.SetOutput(w) }

// SetFlags sets the output flags for the standard logger.
func SetFlags(flag int) { std.SetFlags(flag) }

// SetPrefix sets the output prefix for the standard logger.
func SetPrefix(prefix string) { std.SetPrefix(prefix) }

// Flags returns the output flags for the standard logger.
func Flags() int { return std.Flags() }

// Prefix returns the output prefix for the standard logger.
func Prefix() string { return std.Prefix() }

// Writer returns the output destination for the standard logger.
func Writer() io.Writer { return std.Writer() }

// SetOutput sets the output destination for this logger.
func (l *Logger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.out = w
}

// SetFlags sets the output flags for this logger.
func (l *Logger) SetFlags(flag int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flag = flag
}

// SetPrefix sets the output prefix for this logger.
func (l *Logger) SetPrefix(prefix string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prefix = prefix
}

// Flags returns the output flags for this logger.
func (l *Logger) Flags() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.flag
}

// Prefix returns the output prefix for this logger.
func (l *Logger) Prefix() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.prefix
}

// Writer returns the output destination for this logger.
func (l *Logger) Writer() io.Writer {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.out
}

// Output writes the string s with a simple prefix to the logger.
func (l *Logger) Output(calldepth int, s string) error {
	_ = calldepth
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.out
	if out == nil {
		out = os.Stderr
	}
	prefix := l.prefix
	if prefix != "" {
		_, err := fmt.Fprint(out, prefix, s)
		if len(s) == 0 || s[len(s)-1] != '\n' {
			fmt.Fprint(out, "\n")
		}
		return err
	}
	_, err := fmt.Fprint(out, s)
	if len(s) == 0 || s[len(s)-1] != '\n' {
		fmt.Fprint(out, "\n")
	}
	return err
}

func (l *Logger) Print(v ...any)                 { l.Output(2, fmt.Sprint(v...)) }
func (l *Logger) Printf(format string, v ...any) { l.Output(2, fmt.Sprintf(format, v...)) }
func (l *Logger) Println(v ...any)               { l.Output(2, fmt.Sprintln(v...)) }

func (l *Logger) Fatal(v ...any) {
	l.Output(2, fmt.Sprint(v...))
	os.Exit(1)
}
func (l *Logger) Fatalf(format string, v ...any) {
	l.Output(2, fmt.Sprintf(format, v...))
	os.Exit(1)
}
func (l *Logger) Fatalln(v ...any) {
	l.Output(2, fmt.Sprintln(v...))
	os.Exit(1)
}

func (l *Logger) Panic(v ...any) {
	s := fmt.Sprint(v...)
	l.Output(2, s)
	panic(s)
}
func (l *Logger) Panicf(format string, v ...any) {
	s := fmt.Sprintf(format, v...)
	l.Output(2, s)
	panic(s)
}
func (l *Logger) Panicln(v ...any) {
	s := fmt.Sprintln(v...)
	l.Output(2, s)
	panic(s)
}

// Package-level helpers on the standard logger.
func Print(v ...any)                 { std.Print(v...) }
func Printf(format string, v ...any) { std.Printf(format, v...) }
func Println(v ...any)               { std.Println(v...) }
func Fatal(v ...any)                 { std.Fatal(v...) }
func Fatalf(format string, v ...any) { std.Fatalf(format, v...) }
func Fatalln(v ...any)               { std.Fatalln(v...) }
func Panic(v ...any)                 { std.Panic(v...) }
func Panicf(format string, v ...any) { std.Panicf(format, v...) }
func Panicln(v ...any)               { std.Panicln(v...) }

// Default returns the standard logger used by the package-level functions.
func Default() *Logger { return std }
