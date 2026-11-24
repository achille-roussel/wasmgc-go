// Package context is a wasmgc rewrite with Background/TODO/WithCancel/WithValue.
// Upstream needs time.Timer/AfterFunc which the thin time rewrite lacks.
package context

import (
	"errors"
	"sync"
	"time"
)

// Context carries deadlines, cancellation signals, and request-scoped values.
type Context interface {
	Deadline() (deadline time.Time, ok bool)
	Done() <-chan struct{}
	Err() error
	Value(key any) any
}

// Canceled is the error returned by Context.Err when the context is canceled.
var Canceled = errors.New("context canceled")

// DeadlineExceeded is the error returned by Context.Err when the deadline passes.
var DeadlineExceeded error = deadlineExceededError{}

type deadlineExceededError struct{}

func (deadlineExceededError) Error() string   { return "context deadline exceeded" }
func (deadlineExceededError) Timeout() bool   { return true }
func (deadlineExceededError) Temporary() bool { return true }

type emptyCtx struct{}

func (emptyCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (emptyCtx) Done() <-chan struct{}       { return nil }
func (emptyCtx) Err() error                  { return nil }
func (emptyCtx) Value(key any) any           { return nil }

var (
	background = emptyCtx{}
	todo       = emptyCtx{}
)

// Background returns a non-nil, empty Context. Never canceled, no values.
func Background() Context { return background }

// TODO returns a non-nil, empty Context. Use when it is unclear which Context to use.
func TODO() Context { return todo }

// CancelFunc tells an operation to abandon its work.
type CancelFunc func()

// CancelCauseFunc is like CancelFunc but also sets a cancellation cause.
type CancelCauseFunc func(cause error)

type cancelCtx struct {
	Context
	mu   sync.Mutex
	done chan struct{}
	err  error
}

func (c *cancelCtx) Done() <-chan struct{} { return c.done }

func (c *cancelCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *cancelCtx) cancel(err error) {
	if err == nil {
		err = Canceled
	}
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	close(c.done)
	c.mu.Unlock()
}

// WithCancel returns a copy of parent with a new Done channel.
func WithCancel(parent Context) (ctx Context, cancel CancelFunc) {
	if parent == nil {
		panic("cannot create context from nil parent")
	}
	c := &cancelCtx{Context: parent, done: make(chan struct{})}
	return c, func() { c.cancel(Canceled) }
}

// WithCancelCause is like WithCancel but sets a cause on cancel.
func WithCancelCause(parent Context) (ctx Context, cancel CancelCauseFunc) {
	if parent == nil {
		panic("cannot create context from nil parent")
	}
	c := &cancelCtx{Context: parent, done: make(chan struct{})}
	return c, func(cause error) {
		if cause == nil {
			cause = Canceled
		}
		c.cancel(cause)
	}
}

type valueCtx struct {
	Context
	key, val any
}

func (c *valueCtx) Value(key any) any {
	if c.key == key {
		return c.val
	}
	return c.Context.Value(key)
}

// WithValue returns a copy of parent with key mapped to val.
func WithValue(parent Context, key, val any) Context {
	if parent == nil {
		panic("cannot create context from nil parent")
	}
	if key == nil {
		panic("nil key")
	}
	return &valueCtx{Context: parent, key: key, val: val}
}

type deadlineCtx struct {
	*cancelCtx
	deadline time.Time
}

func (c *deadlineCtx) Deadline() (time.Time, bool) { return c.deadline, true }

// WithDeadline returns a context that cancels when the deadline is reached
// or cancel is called. Under wasmgc the timer path uses time.Sleep in a goroutine.
func WithDeadline(parent Context, d time.Time) (Context, CancelFunc) {
	if parent == nil {
		panic("cannot create context from nil parent")
	}
	c := &cancelCtx{Context: parent, done: make(chan struct{})}
	dc := &deadlineCtx{cancelCtx: c, deadline: d}
	cancel := func() { c.cancel(Canceled) }
	dur := time.Until(d)
	if dur <= 0 {
		c.cancel(DeadlineExceeded)
		return dc, cancel
	}
	go func() {
		time.Sleep(dur)
		c.cancel(DeadlineExceeded)
	}()
	return dc, cancel
}

// WithTimeout is like WithDeadline with Now()+timeout.
func WithTimeout(parent Context, timeout time.Duration) (Context, CancelFunc) {
	return WithDeadline(parent, time.Now().Add(timeout))
}

// WithTimeoutCause is like WithTimeout but records a cause (ignored under stub;
// cancel still uses DeadlineExceeded / Canceled).
func WithTimeoutCause(parent Context, timeout time.Duration, cause error) (Context, CancelFunc) {
	_ = cause
	return WithTimeout(parent, timeout)
}

// WithDeadlineCause is like WithDeadline but records a cause (ignored under stub).
func WithDeadlineCause(parent Context, d time.Time, cause error) (Context, CancelFunc) {
	_ = cause
	return WithDeadline(parent, d)
}

// Cause returns the underlying cause of ctx's cancellation, if known.
func Cause(ctx Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// AfterFunc runs f in a new goroutine when ctx is done. If Done is nil
// (never canceled), f never runs. Returns a stop function.
func AfterFunc(ctx Context, f func()) (stop func() bool) {
	done := ctx.Done()
	if done == nil {
		return func() bool { return false }
	}
	stopped := false
	var mu sync.Mutex
	go func() {
		<-done
		mu.Lock()
		defer mu.Unlock()
		if !stopped {
			f()
		}
	}()
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			return false
		}
		stopped = true
		return true
	}
}

// WithoutCancel returns a context that is never canceled, keeping parent's values.
func WithoutCancel(parent Context) Context {
	if parent == nil {
		panic("cannot create context from nil parent")
	}
	return &withoutCancelCtx{parent}
}

type withoutCancelCtx struct{ Context }

func (withoutCancelCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (withoutCancelCtx) Done() <-chan struct{}       { return nil }
func (withoutCancelCtx) Err() error                  { return nil }
