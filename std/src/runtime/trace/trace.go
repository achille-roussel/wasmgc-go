// Package trace is a wasmgc no-op rewrite of runtime/trace.
// Upstream needs runtime.StartTrace/StopTrace/ReadTrace which are absent;
// language tests only call Start/Stop/Log around panics.
package trace

import (
	"context"
	"io"
)

// Start begins a no-op execution trace writing to w (ignored).
func Start(w io.Writer) error {
	_ = w
	return nil
}

// Stop stops a no-op execution trace.
func Stop() {}

// Log writes a no-op log record associated with ctx.
func Log(ctx context.Context, category, message string) {
	_, _, _ = ctx, category, message
}

// Logf writes a no-op formatted log record associated with ctx.
func Logf(ctx context.Context, category, format string, args ...any) {
	_, _, _, _ = ctx, category, format, args
}

// Task is a no-op user task.
type Task struct{}

// End marks the end of a no-op task.
func (t *Task) End() {}

// NewTask returns ctx unchanged with a no-op Task.
func NewTask(pctx context.Context, taskType string) (ctx context.Context, task *Task) {
	_ = taskType
	return pctx, &Task{}
}

// Region is a no-op region marker.
type Region struct{}

// End marks the end of a no-op region.
func (r *Region) End() {}

// StartRegion returns a no-op Region.
func StartRegion(ctx context.Context, regionType string) *Region {
	_, _ = ctx, regionType
	return &Region{}
}

// WithRegion runs fn inside a no-op region.
func WithRegion(ctx context.Context, regionType string, fn func()) {
	_, _ = ctx, regionType
	fn()
}

// IsEnabled reports whether tracing is enabled (always false under wasmgc).
func IsEnabled() bool { return false }
