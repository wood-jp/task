// Package sigtrigger provides a Task that executes an action each time a
// configured OS signal is received. Unlike ossignal, which exits on the first
// signal, sigtrigger stays alive and re-runs the action on every signal delivery.
// Signal capture begins at [NewTask] construction time.
package sigtrigger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/wood-jp/task"
)

// Action is a function executed each time a configured signal fires.
type Action func(context.Context) error

// DefaultSignals returns the default signal list: [SIGHUP].
func DefaultSignals() []os.Signal {
	return []os.Signal{syscall.SIGHUP}
}

// Task is a [task.Task] that runs an action each time a configured OS signal is
// received. It stays alive until the context is cancelled.
type Task struct {
	sigCh           chan os.Signal
	action          Action
	continueOnError bool
	logger          *slog.Logger
	name            string
	guard           task.Guard
}

// options holds the configuration for a Task.
type options struct {
	signals         []os.Signal
	logger          *slog.Logger
	continueOnError bool
}

// Option is an option func for NewTask.
type Option func(*options)

// WithLogger sets the logger used for signal receipts and (when WithContinueOnError
// is active) action errors.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) { o.logger = logger }
}

// WithSignals overrides the default signals being listened for.
func WithSignals(signals ...os.Signal) Option {
	return func(o *options) { o.signals = signals }
}

// WithContinueOnError causes action errors to be logged and discarded rather
// than terminating the task. A logger should be set via WithLogger so errors
// are visible; without one they are silently discarded.
func WithContinueOnError() Option {
	return func(o *options) { o.continueOnError = true }
}

// NewTask creates a new [Task] that calls action on each signal delivery.
// Signal capture begins immediately upon construction.
// Panics if the resolved signals list is empty.
func NewTask(action Action, opts ...Option) *Task {
	o := options{
		signals: DefaultSignals(),
		logger:  slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(&o)
	}

	if len(o.signals) == 0 {
		panic("sigtrigger: NewTask requires at least one signal")
	}

	sigNames := make([]string, len(o.signals))
	for i, s := range o.signals {
		sigNames[i] = s.String()
	}
	t := &Task{
		sigCh:           make(chan os.Signal, 1),
		action:          action,
		continueOnError: o.continueOnError,
		logger:          o.logger,
		name:            fmt.Sprintf("sigtrigger task (%s)", strings.Join(sigNames, ", ")),
	}
	signal.Notify(t.sigCh, o.signals...)
	return t
}

// Name returns the name of this task, including the signals being listened for.
func (t *Task) Name() string { return t.name }

// Run blocks until ctx is cancelled, returning nil. Each time a configured
// signal is received, the action is executed. If the action returns an error
// and WithContinueOnError is not set, Run returns that error immediately.
// Returns [task.ErrAlreadyStarted] if called more than once.
func (t *Task) Run(ctx context.Context) error {
	if err := t.guard.TryStart(); err != nil {
		return err
	}
	defer func() {
		signal.Stop(t.sigCh)
		close(t.sigCh)
	}()
	for {
		select {
		case sig := <-t.sigCh:
			t.logger.Debug("signal received", slog.String("signal", sig.String()))
			if err := t.action(ctx); err != nil {
				if t.continueOnError {
					t.logger.Error("sigtrigger action failed", slog.Any("error", err))
					continue
				}
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}
