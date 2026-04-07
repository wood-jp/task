// Package ossignal provides a Task that listens for signals from the operating system.
// Signal capture begins at [NewTask] construction time.
package ossignal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/wood-jp/xerrors/stacktrace"
)

// ErrAlreadyStarted is returned when Run is called on a Task that is already running.
var ErrAlreadyStarted = errors.New("ossignal task already started")

// DefaultSignals returns the os signals that will cause this task to exit.
func DefaultSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}
}

// Task is a [task.Task] that waits for a termination signal from the OS.
type Task struct {
	sigCh    chan os.Signal
	name     string
	logger   *slog.Logger
	logLevel slog.Level
	started  atomic.Bool
	onSignal func(os.Signal)
}

// options holds the configuration for a Task.
type options struct {
	signals  []os.Signal
	logger   *slog.Logger
	logLevel slog.Level
	onSignal func(os.Signal)
}

// Option is an option func for NewTask.
type Option func(options *options)

// WithLogger sets the logger to be used.
func WithLogger(logger *slog.Logger) Option {
	return func(options *options) {
		options.logger = logger
	}
}

// WithSignalLogLevel sets the log level used when an OS signal is received.
func WithSignalLogLevel(level slog.Level) Option {
	return func(options *options) {
		options.logLevel = level
	}
}

// WithSignals overrides the default signals being listened for.
func WithSignals(signals ...os.Signal) Option {
	return func(options *options) {
		options.signals = signals
	}
}

// WithOnSignal sets a callback that is invoked after the signal is logged.
func WithOnSignal(fn func(os.Signal)) Option {
	return func(options *options) {
		options.onSignal = fn
	}
}

// NewTask creates a new [Task]. Signal capture begins immediately upon construction.
// Panics if the resolved signals list is empty.
func NewTask(opts ...Option) *Task {
	// Set up default options
	options := options{
		signals:  DefaultSignals(),
		logger:   slog.New(slog.DiscardHandler),
		logLevel: slog.LevelInfo,
	}

	// Apply provided options
	for _, opt := range opts {
		opt(&options)
	}

	if len(options.signals) == 0 {
		panic("ossignal: NewTask requires at least one signal")
	}

	sigNames := make([]string, len(options.signals))
	for i, s := range options.signals {
		sigNames[i] = s.String()
	}
	task := &Task{
		sigCh:    make(chan os.Signal, 1),
		name:     fmt.Sprintf("os signal task (%s)", strings.Join(sigNames, ", ")),
		logger:   options.logger,
		logLevel: options.logLevel,
		onSignal: options.onSignal,
	}
	signal.Notify(task.sigCh, options.signals...)
	return task
}

// Name returns the name of this task, including the signals being listened for.
func (t *Task) Name() string {
	return t.name
}

// Run blocks until an OS signal is received or the context is cancelled, then returns nil.
// Returns ErrAlreadyStarted if called more than once.
func (t *Task) Run(ctx context.Context) error {
	if !t.started.CompareAndSwap(false, true) {
		return stacktrace.Wrap(ErrAlreadyStarted)
	}

	select {
	case sig := <-t.sigCh:
		t.logger.Log(context.Background(), t.logLevel, "os signal received", slog.String("signal", sig.String()))
		if t.onSignal != nil {
			t.onSignal(sig)
		}
	case <-ctx.Done():
	}

	signal.Stop(t.sigCh)
	close(t.sigCh)
	return nil
}
