package task

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/wood-jp/xerrors"
	"github.com/wood-jp/xerrors/errcontext"
	"github.com/wood-jp/xerrors/errgroup"
	"github.com/wood-jp/xerrors/stacktrace"
)

// ErrManagerStopped is returned when Run or RunEphemeral is called after the manager has stopped.
var ErrManagerStopped = errors.New("manager already stopped")

// ErrShutdownTimeout is returned by Wait when tasks do not stop within the shutdown timeout.
var ErrShutdownTimeout = errors.New("shutdown timed out waiting for tasks to stop")

// ErrCleanupTimeout is returned by Wait when cleanup functions do not complete within
// the cleanup timeout. If tasks also failed, the task error is provided as the base
// and cleanup errors are attached; see [CleanupErrors].
var ErrCleanupTimeout = errors.New("cleanup timed out")

// ErrCleanupFailed is used as the base error when cleanup functions return errors
// but no task error occurred.
var ErrCleanupFailed = errors.New("one or more cleanup functions failed")

// CleanupErrors holds errors returned by registered cleanup functions.
// Retrieve it from a Wait error using [xerrors.Extract][CleanupErrors].
type CleanupErrors []error

// LogValue implements [slog.LogValuer].
func (e CleanupErrors) LogValue() slog.Value {
	attrs := make([]slog.Attr, len(e))
	for i, err := range e {
		// slog.Any preserves rich error detail: if err implements slog.LogValuer
		// (e.g. xerrors.ExtendedError carrying stacktrace or context), the slog
		// handler resolves it recursively. Plain errors fall back to their Error() string.
		attrs[i] = slog.Any(strconv.Itoa(i), err)
	}
	return slog.GroupValue(slog.Attr{Key: "cleanup_errors", Value: slog.GroupValue(attrs...)})
}

// Manager manages a group of tasks that
// should all stop when any one of them stops.
type Manager struct {
	ctx             context.Context
	cancel          context.CancelFunc
	group           *errgroup.Group
	logger          *slog.Logger
	cleanup         []func() error
	waitOnce        sync.Once
	waitResult      error
	shutdownTimeout time.Duration
	cleanupTimeout  time.Duration
}

type options struct {
	logger          *slog.Logger
	ctx             context.Context
	shutdownTimeout time.Duration
	cleanupTimeout  time.Duration
}

// Option is an option func for NewManager.
type Option func(options *options)

// WithLogger sets the logger to be used.
func WithLogger(logger *slog.Logger) Option {
	return func(options *options) {
		options.logger = logger
	}
}

// WithContext sets a parent context for the manager. When the parent context is
// cancelled, the manager will begin shutting down.
func WithContext(ctx context.Context) Option {
	return func(options *options) {
		options.ctx = ctx
	}
}

// WithShutdownTimeout sets the maximum duration to wait for tasks to stop after
// the manager context is cancelled. Defaults to 30 seconds.
func WithShutdownTimeout(d time.Duration) Option {
	return func(options *options) {
		options.shutdownTimeout = d
	}
}

// WithCleanupTimeout sets the maximum total duration for all cleanup functions to
// complete. Defaults to 10 seconds.
func WithCleanupTimeout(d time.Duration) Option {
	return func(options *options) {
		options.cleanupTimeout = d
	}
}

// NewManager creates a Manager.
func NewManager(opts ...Option) *Manager {
	options := options{
		logger:          slog.New(slog.DiscardHandler),
		ctx:             context.Background(),
		shutdownTimeout: 30 * time.Second,
		cleanupTimeout:  10 * time.Second,
	}

	for _, opt := range opts {
		opt(&options)
	}

	ctx, cancel := context.WithCancel(options.ctx)
	return &Manager{
		ctx:             ctx,
		cancel:          cancel,
		group:           errgroup.New(),
		logger:          options.logger,
		shutdownTimeout: options.shutdownTimeout,
		cleanupTimeout:  options.cleanupTimeout,
	}
}

// Run immediately starts all of the given tasks.
// Returns ErrManagerStopped if the manager has already stopped.
func (tm *Manager) Run(tasks ...Task) error {
	if tm.ctx.Err() != nil {
		return stacktrace.Wrap(ErrManagerStopped)
	}
	for _, task := range tasks {
		// errgroup recovers panics as errors.
		tm.group.Go(tm.runTask(task, true))
	}
	return nil
}

// RunEphemeral immediately starts all of the given tasks. These tasks are
// expected to terminate without error while others continue running.
// Returns ErrManagerStopped if the manager has already stopped.
func (tm *Manager) RunEphemeral(tasks ...Task) error {
	if tm.ctx.Err() != nil {
		return stacktrace.Wrap(ErrManagerStopped)
	}
	for _, task := range tasks {
		// errgroup recovers panics as errors.
		tm.group.Go(tm.runTask(task, false))
	}
	return nil
}

// Cleanup registers a function that runs after all tasks are stopped.
// Similar to defer, cleanup functions are executed in the reverse order
// in which they were registered. Any errors returned are collected and
// attached to the Wait return value as [CleanupErrors].
func (tm *Manager) Cleanup(f func() error) {
	tm.cleanup = append(tm.cleanup, f)
}

// Wait blocks until all tasks are complete, then executes all registered
// cleanup functions. It returns the first encountered task error, with any
// cleanup errors attached as [CleanupErrors] (retrieve via [xerrors.Extract]).
// If tasks do not stop within the shutdown timeout, Wait returns ErrShutdownTimeout.
// If cleanup functions do not complete within the cleanup timeout, Wait returns
// ErrCleanupTimeout. Concurrent or repeated calls all return the same result.
func (tm *Manager) Wait() error {
	tm.waitOnce.Do(func() {
		tm.waitResult = tm.wait()
	})
	return tm.waitResult
}

func (tm *Manager) wait() error {
	done := make(chan error, 1)
	go func() {
		done <- tm.group.Wait()
	}()

	select {
	case err := <-done:
		return tm.runCleanup(err)
	case <-tm.ctx.Done():
	}

	// Context was cancelled; wait for tasks to finish within the shutdown timeout.
	timer := time.NewTimer(tm.shutdownTimeout)
	defer timer.Stop()

	select {
	case err := <-done:
		return tm.runCleanup(err)
	case <-timer.C:
		// Tasks did not stop in time; still attempt cleanup best-effort.
		return tm.runCleanup(stacktrace.Wrap(ErrShutdownTimeout))
	}
}

// runCleanup executes all registered cleanup functions sequentially in reverse
// registration order, subject to cleanupTimeout. Any errors are collected and
// attached to taskErr as [CleanupErrors]. If the timeout fires, ErrCleanupTimeout
// is returned instead (with taskErr as the base when non-nil).
func (tm *Manager) runCleanup(taskErr error) error {
	if len(tm.cleanup) == 0 {
		return taskErr
	}

	var errs CleanupErrors
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, f := range slices.Backward(tm.cleanup) {
			if err := f(); err != nil {
				errs = append(errs, err)
			}
		}
	}()

	timer := time.NewTimer(tm.cleanupTimeout)
	defer timer.Stop()

	select {
	case <-done:
		if len(errs) == 0 {
			return taskErr
		}
		base := taskErr
		if base == nil {
			base = ErrCleanupFailed
		}
		return xerrors.Extend(errs, base)
	case <-timer.C:
		base := error(ErrCleanupTimeout)
		if taskErr != nil {
			base = taskErr
		}
		return stacktrace.Wrap(base)
	}
}

// Stop cancels the context immediately and waits for all running tasks to complete.
func (tm *Manager) Stop() error {
	tm.cancel()
	return tm.Wait()
}

func (tm *Manager) runTask(t Task, terminateAll bool) func() error {
	return func() error {
		tm.logger.Info("task starting", slog.String("task", t.Name()))
		if err := t.Run(tm.ctx); err != nil {
			err = errcontext.Add(err, slog.String("task", t.Name()))
			tm.logger.Error("task failed", xerrors.Log(err))
			tm.cancel()
			return err
		}

		if terminateAll {
			// When the task completes, regardless of why, cancel the context
			// so that other tasks know they should also stop.
			defer tm.cancel()
		}

		tm.logger.Info("task stopped", slog.String("task", t.Name()))
		return nil
	}
}

// Context returns the context used for managing all tasks.
func (tm *Manager) Context() context.Context {
	return tm.ctx
}
