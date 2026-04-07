// Package ossignal provides a Task that listens for signals from the operating system.
package ossignal

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// DefaultSignals are the os signals that will cause this task to exit.
var DefaultSignals = []os.Signal{
	syscall.SIGINT,
	syscall.SIGTERM,
	syscall.SIGQUIT,
}

// Task is a [task.Task] that waits for a termination signal from the OS.
type Task struct {
	sigCh    chan os.Signal
	name     string
	logger   *slog.Logger
	logLevel slog.Level
}

// options holds the configuration for a Task.
type options struct {
	signals  []os.Signal
	logger   *slog.Logger
	logLevel slog.Level
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

// NewTask creates a new [Task].
func NewTask(opts ...Option) *Task {
	// Set up default options
	options := options{
		signals:  DefaultSignals,
		logger:   slog.New(slog.DiscardHandler),
		logLevel: slog.LevelInfo,
	}

	// Apply provided options
	for _, opt := range opts {
		opt(&options)
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
	}
	signal.Notify(task.sigCh, options.signals...)
	return task
}

// Name returns the name of this task, including the signals being listened for.
func (t *Task) Name() string {
	return t.name
}

// Run blocks until an OS signal is received or the context is cancelled, then returns nil.
func (t *Task) Run(ctx context.Context) error {
	select {
	case sig := <-t.sigCh:
		t.logger.Log(context.Background(), t.logLevel, "os signal received", slog.String("signal", sig.String()))
	case <-ctx.Done():
	}

	signal.Stop(t.sigCh)
	close(t.sigCh)
	return nil
}
