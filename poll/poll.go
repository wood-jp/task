// Package poll provides a Task that periodically executes an action function.
package poll

import (
	"context"
	"log/slog"
	"time"

	"github.com/wood-jp/task"
)

// Action is a function executed on each poll interval.
// It must return nil when the context is cancelled; a non-nil error
// either terminates the task (default) or is logged and discarded (WithContinueOnError).
type Action func(context.Context) error

// Task is a [task.Task] that executes an Action on a fixed interval using a ticker.
// Unlike loop.Task, the interval is clock-based: ticks fire regardless of how long
// the action takes. If the action takes longer than the interval, the next tick
// fires immediately after it completes (Go's ticker coalesces missed ticks).
type Task struct {
	action          Action
	name            string
	interval        time.Duration
	logger          *slog.Logger
	runAtStart      bool
	continueOnError bool
	guard           task.Guard
}

type options struct {
	logger          *slog.Logger
	runAtStart      bool
	continueOnError bool
}

// Option is an option func for NewTask.
type Option func(*options)

// WithLogger sets the logger used when WithContinueOnError is active and an action
// returns an error.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.logger = logger }
}

// WithRunAtStart causes the action to execute immediately when Run is called,
// before the first interval tick.
func WithRunAtStart() Option {
	return func(o *options) { o.runAtStart = true }
}

// WithContinueOnError causes the task to log action errors and continue polling
// rather than propagating the error and stopping. A logger should be set via
// WithLogger so errors are visible; without one they are silently discarded.
func WithContinueOnError() Option {
	return func(o *options) { o.continueOnError = true }
}

// NewTask creates a new poll Task. action is called on every interval tick.
// name is used in Name() (returned as "poll: <name>").
// Panics if interval is less than or equal to zero.
func NewTask(action Action, name string, interval time.Duration, opts ...Option) *Task {
	if interval <= 0 {
		panic("poll: interval must be greater than zero")
	}
	o := options{
		logger: slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &Task{
		action:          action,
		name:            "poll: " + name,
		interval:        interval,
		logger:          o.logger,
		runAtStart:      o.runAtStart,
		continueOnError: o.continueOnError,
	}
}

// Name returns the name of this task.
func (t *Task) Name() string { return t.name }

// Run starts the polling loop. It blocks until ctx is cancelled, returning nil,
// or until the action returns an error (when WithContinueOnError is not set).
// Returns ErrAlreadyStarted if called more than once.
func (t *Task) Run(ctx context.Context) error {
	if err := t.guard.TryStart(); err != nil {
		return err
	}

	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	if t.runAtStart {
		if err := t.execute(ctx); err != nil {
			return err
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := t.execute(ctx); err != nil {
				return err
			}
		}
	}
}

func (t *Task) execute(ctx context.Context) error {
	if err := t.action(ctx); err != nil {
		if t.continueOnError {
			t.logger.Error("poll action failed", slog.Any("error", err))
			return nil
		}
		return err
	}
	return nil
}
