// Package loop provides a Task that repeatedly creates and runs an ephemeral Task.
package loop

import (
	"context"
	"log/slog"
	"time"

	"github.com/wood-jp/task"
	"github.com/wood-jp/xerrors/errcontext"
)

// Factory is a function that creates a new [task.Task] for each loop iteration.
type Factory func(context.Context) (task.Task, error)

// Task is a [task.Task] that repeatedly creates and runs an ephemeral task via a factory.
// If the factory or inner task returns an error, Run propagates it.
// If the inner task returns nil, a new task is created and run again.
type Task struct {
	factory      Factory
	name         string
	logger       *slog.Logger
	delay        time.Duration
	initialDelay bool
	guard        task.Guard
}

type options struct {
	logger       *slog.Logger
	delay        time.Duration
	initialDelay bool
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

// NewTask creates a new loop Task. factory is called before each run to produce the inner task.
// name is used in Name() (returned as "loop: <name>") and in log output.
func NewTask(factory Factory, name string, opts ...Option) *Task {
	o := options{
		logger: slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(&o)
	}
	return &Task{
		factory:      factory,
		name:         "loop: " + name,
		logger:       o.logger,
		delay:        o.delay,
		initialDelay: o.initialDelay,
	}
}

// Name returns the name of this task.
func (t *Task) Name() string { return t.name }

// Run loops forever: call factory, run the produced task, repeat.
// Exits without error when ctx is cancelled. Propagates any factory or task error.
// Returns [task.ErrAlreadyStarted] if called more than once.
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

		// Create inner task via factory
		inner, err := t.factory(ctx)
		if err != nil {
			return errcontext.Add(err,
				slog.Uint64("run", run),
				slog.String("phase", "factory"),
			)
		}

		t.logger.Info("run starting",
			slog.Uint64("run", run),
			slog.String("task", inner.Name()),
		)

		if err := inner.Run(ctx); err != nil {
			return errcontext.Add(err,
				slog.Uint64("run", run),
				slog.String("phase", "run"),
				slog.String("task", inner.Name()),
			)
		}

		t.logger.Info("run completed",
			slog.Uint64("run", run),
			slog.String("task", inner.Name()),
		)

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
