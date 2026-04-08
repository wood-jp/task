// Package loop provides a Task that repeatedly executes an action function.
package loop

import (
	"context"
	"log/slog"
	"time"

	"github.com/wood-jp/task"
	"github.com/wood-jp/xerrors/errcontext"
)

// Task is a [task.Task] that calls a [task.Action] in a loop until the context
// is cancelled or the action returns an error.
// If the action returns nil, it is called again (after an optional delay).
type Task struct {
	action          task.Action
	name            string
	logger          *slog.Logger
	delay           time.Duration
	initialDelay    bool
	continueOnError bool
	guard           task.Guard
}

type options struct {
	logger          *slog.Logger
	delay           time.Duration
	initialDelay    bool
	continueOnError bool
}

// Option is an option func for NewTask.
type Option func(*options)

// WithLogger sets the logger to be used for per-run log lines.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.logger = logger }
}

// WithDelay sets a context-aware sleep between runs (default: 0, no sleep).
// The delay occurs after each run completes before the next begins.
// Use WithInitialDelay to also sleep before the first run.
func WithDelay(d time.Duration) Option {
	return func(o *options) { o.delay = d }
}

// WithInitialDelay causes the delay (set via WithDelay) to also apply before the first run.
func WithInitialDelay() Option {
	return func(o *options) { o.initialDelay = true }
}

// WithContinueOnError causes the task to log action errors and continue looping
// rather than propagating the error and stopping. A logger should be set via
// WithLogger so errors are visible; without one they are silently discarded.
func WithContinueOnError() Option {
	return func(o *options) { o.continueOnError = true }
}

// NewTask creates a new loop Task. action is called on each iteration.
// name is used in Name() (returned as "loop: <name>") and in log output.
func NewTask(action task.Action, name string, opts ...Option) *Task {
	o := options{
		logger: slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &Task{
		action:          action,
		name:            "loop: " + name,
		logger:          o.logger,
		delay:           o.delay,
		initialDelay:    o.initialDelay,
		continueOnError: o.continueOnError,
	}
}

// Name returns the name of this task.
func (t *Task) Name() string { return t.name }

// Run loops forever: call action, repeat.
// Exits without error when ctx is cancelled. Propagates any action error (unless
// WithContinueOnError is set). Returns [task.ErrAlreadyStarted] if called more than once.
func (t *Task) Run(ctx context.Context) error {
	if err := t.guard.TryStart(); err != nil {
		return err
	}

	for run := uint64(1); ; run++ {
		// Sleep if configured (before first run only if initialDelay; otherwise only between runs)
		if t.delay > 0 && (run > 1 || t.initialDelay) {
			if !sleepWithContext(ctx, t.delay) {
				return nil
			}
		}

		t.logger.Info("run starting", slog.Uint64("run", run))

		if err := t.action(ctx); err != nil {
			if t.continueOnError {
				t.logger.Error("loop action failed", slog.Uint64("run", run), slog.Any("error", err))
				if ctx.Err() != nil {
					return nil
				}
				continue
			}
			return errcontext.Add(err, slog.Uint64("run", run))
		}

		t.logger.Info("run completed", slog.Uint64("run", run))

		// Exit cleanly if context was cancelled during the run
		if ctx.Err() != nil {
			return nil
		}
	}
}

// sleepWithContext sleeps for d, returning true if the sleep completed or false if ctx was cancelled.
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
