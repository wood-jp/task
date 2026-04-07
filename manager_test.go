package task_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wood-jp/task"
	"github.com/wood-jp/xerrors"
)

func NewTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewJSONHandler(t.Output(), nil))
}

var errTest = errors.New("test error")

type TestTask struct {
	errChan chan error
	name    string
	err     error
}

func (t TestTask) Run(ctx context.Context) error {
	defer close(t.errChan)

	select {
	case err := <-t.errChan:
		return err
	case <-ctx.Done():
		return t.err
	}
}

func (t TestTask) Error(err error) {
	t.errChan <- err
}

func NewTestTask(name string, err error) *TestTask {
	return &TestTask{
		errChan: make(chan error),
		name:    name,
		err:     err,
	}
}

func (t TestTask) Name() string {
	return t.name
}

func TestTaskManagerStop(t *testing.T) {
	t.Parallel()

	logger := NewTestLogger(t)
	tm := task.NewManager(task.WithLogger(logger))

	ctx := tm.Context()
	assert.NotNil(t, ctx)
	assert.NoError(t, ctx.Err())

	task1 := NewTestTask("task1", nil)
	task2 := NewTestTask("task2", nil)

	cleanupCheck := make([]int, 0, 2)
	tm.Cleanup(func() error { cleanupCheck = append(cleanupCheck, 1); return nil })
	tm.Cleanup(func() error { cleanupCheck = append(cleanupCheck, 2); return nil })

	require.NoError(t, tm.Run(task1, task2))

	err := tm.Stop()
	assert.NoError(t, err)
	assert.Equal(t, []int{2, 1}, cleanupCheck)

	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestTaskManagerStopError(t *testing.T) {
	t.Parallel()

	logger := NewTestLogger(t)
	tm := task.NewManager(task.WithLogger(logger))

	task1 := NewTestTask("task1", errTest)
	task2 := NewTestTask("task2", nil)

	cleanupCheck := make([]int, 0, 2)
	tm.Cleanup(func() error { cleanupCheck = append(cleanupCheck, 1); return nil })
	tm.Cleanup(func() error { cleanupCheck = append(cleanupCheck, 2); return nil })

	require.NoError(t, tm.Run(task1, task2))

	err := tm.Stop()
	assert.Error(t, err)
	assert.ErrorIs(t, err, errTest)
	assert.Equal(t, []int{2, 1}, cleanupCheck)
}

func TestTaskManagerRunError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		logger := NewTestLogger(t)
		tm := task.NewManager(task.WithLogger(logger))

		task1 := NewTestTask("task1", nil)
		task2 := NewTestTask("task2", nil)

		cleanupCheck := make([]int, 0, 2)
		tm.Cleanup(func() error { cleanupCheck = append(cleanupCheck, 1); return nil })
		tm.Cleanup(func() error { cleanupCheck = append(cleanupCheck, 2); return nil })

		require.NoError(t, tm.Run(task1, task2))

		// task 2 encounters an error after it has started running
		go func() {
			time.Sleep(time.Millisecond * 100)
			task2.Error(errTest)
		}()

		err := tm.Wait()
		assert.Error(t, err)
		assert.ErrorIs(t, err, errTest)
		assert.Equal(t, []int{2, 1}, cleanupCheck)
	})
}

func TestTaskManagerRun(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		logger := NewTestLogger(t)
		tm := task.NewManager(task.WithLogger(logger))

		task1 := NewTestTask("task1", nil)
		task2 := NewTestTask("task2", nil)

		cleanupCheck := make([]int, 0, 2)
		tm.Cleanup(func() error { cleanupCheck = append(cleanupCheck, 1); return nil })
		tm.Cleanup(func() error { cleanupCheck = append(cleanupCheck, 2); return nil })

		require.NoError(t, tm.Run(task1, task2))

		// task 2 stops without error after it has started running
		go func() {
			time.Sleep(time.Millisecond * 100)
			task2.Error(nil)
		}()

		err := tm.Wait()
		assert.NoError(t, err)
		assert.Equal(t, []int{2, 1}, cleanupCheck)
	})
}

func TestTaskManagerRunEphemeral(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		logger := NewTestLogger(t)
		tm := task.NewManager(task.WithLogger(logger))

		task1 := NewTestTask("task1", nil)
		task2 := NewTestTask("task2", nil)

		cleanupCheck := make([]int, 0, 2)
		tm.Cleanup(func() error { cleanupCheck = append(cleanupCheck, 1); return nil })
		tm.Cleanup(func() error { cleanupCheck = append(cleanupCheck, 2); return nil })

		require.NoError(t, tm.Run(task1))
		require.NoError(t, tm.RunEphemeral(task2))

		// task 2 stops without error after it has started running
		go func() {
			time.Sleep(time.Millisecond * 100)
			task2.Error(nil)
		}()

		// task 1 stops with error even later
		go func() {
			time.Sleep(time.Millisecond * 200)
			task1.Error(errTest)
		}()

		// We should expect the error from task1 to stop the manager and
		// then propagate up to the caller.
		// Task2 should stop itself, but not the manager - allowing task1
		// to continue running until it finishes or errors out.

		err := tm.Wait()
		assert.Error(t, err)
		assert.ErrorIs(t, err, errTest)
		assert.Equal(t, []int{2, 1}, cleanupCheck)
	})
}

func TestManagerRunAfterStop(t *testing.T) {
	t.Parallel()

	tm := task.NewManager()
	err := tm.Stop()
	require.NoError(t, err)

	err = tm.Run(NewTestTask("task1", nil))
	assert.ErrorIs(t, err, task.ErrManagerStopped)
}

func TestManagerRunEphemeralAfterStop(t *testing.T) {
	t.Parallel()

	tm := task.NewManager()
	err := tm.Stop()
	require.NoError(t, err)

	err = tm.RunEphemeral(NewTestTask("task1", nil))
	assert.ErrorIs(t, err, task.ErrManagerStopped)
}

// TestWaitTasksCompleteNaturally covers the wait() branch where all tasks finish
// before the manager context is ever cancelled (i.e. the "done" case in the first
// select fires rather than "ctx.Done").
func TestWaitTasksCompleteNaturally(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		tm := task.NewManager()

		task1 := NewTestTask("task1", nil)
		// RunEphemeral: task completion does not cancel the manager context.
		require.NoError(t, tm.RunEphemeral(task1))

		go func() {
			time.Sleep(time.Millisecond * 100)
			task1.Error(nil)
		}()

		err := tm.Wait()
		assert.NoError(t, err)
	})
}

func TestManagerWithContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	tm := task.NewManager(task.WithContext(ctx))
	task1 := NewTestTask("task1", nil)

	require.NoError(t, tm.Run(task1))

	// Cancel the parent context; the manager should stop.
	cancel()

	err := tm.Wait()
	assert.NoError(t, err)
}

func TestManagerShutdownTimeout(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here — the neverStopTask goroutine never exits,
	// so goroutines in the bubble would never complete.

	stubTask := &neverStopTask{}

	tm := task.NewManager(task.WithShutdownTimeout(10 * time.Millisecond))
	require.NoError(t, tm.Run(stubTask))

	go func() {
		tm.Stop() //nolint:errcheck
	}()

	err := tm.Wait()
	assert.ErrorIs(t, err, task.ErrShutdownTimeout)
}

func TestCleanupErrors(t *testing.T) {
	t.Parallel()

	var errCleanup1 = errors.New("cleanup 1 failed")
	var errCleanup2 = errors.New("cleanup 2 failed")

	tm := task.NewManager()
	tm.Cleanup(func() error { return errCleanup1 })
	tm.Cleanup(func() error { return errCleanup2 })

	err := tm.Stop()
	require.Error(t, err)

	// No task error — base should be ErrCleanupFailed.
	assert.ErrorIs(t, err, task.ErrCleanupFailed)

	// Cleanup errors are attached and extractable.
	cleanupErrs, ok := xerrors.Extract[task.CleanupErrors](err)
	require.True(t, ok)
	assert.Len(t, cleanupErrs, 2)
	// Cleanups run in reverse registration order; cleanup 2 runs first.
	assert.ErrorIs(t, cleanupErrs[0], errCleanup2)
	assert.ErrorIs(t, cleanupErrs[1], errCleanup1)
}

func TestCleanupErrorsWithTaskError(t *testing.T) {
	t.Parallel()

	var errCleanup = errors.New("cleanup failed")

	tm := task.NewManager()
	tm.Cleanup(func() error { return errCleanup })

	task1 := NewTestTask("task1", errTest)
	require.NoError(t, tm.Run(task1))

	err := tm.Stop()
	require.Error(t, err)

	// Task error is preserved as the base.
	assert.ErrorIs(t, err, errTest)

	// Cleanup error is also attached.
	cleanupErrs, ok := xerrors.Extract[task.CleanupErrors](err)
	require.True(t, ok)
	assert.Len(t, cleanupErrs, 1)
	assert.ErrorIs(t, cleanupErrs[0], errCleanup)
}

func TestCleanupTimeout(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here for the same reason as TestManagerShutdownTimeout.

	tm := task.NewManager(task.WithCleanupTimeout(10 * time.Millisecond))
	tm.Cleanup(func() error {
		time.Sleep(time.Hour) // never returns in time
		return nil
	})

	err := tm.Stop()
	assert.ErrorIs(t, err, task.ErrCleanupTimeout)
}

func TestCleanupTimeoutWithTaskError(t *testing.T) {
	t.Parallel()

	tm := task.NewManager(task.WithCleanupTimeout(10 * time.Millisecond))
	tm.Cleanup(func() error {
		time.Sleep(time.Hour) // never returns in time
		return nil
	})

	task1 := NewTestTask("task1", errTest)
	require.NoError(t, tm.Run(task1))

	err := tm.Stop()
	require.Error(t, err)

	// Task error is preserved as the base when cleanup times out.
	assert.ErrorIs(t, err, errTest)
}

func TestCleanupErrorsLogValue(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	var errCleanup1 = errors.New("first cleanup failed")
	var errCleanup2 = errors.New("second cleanup failed")

	tm := task.NewManager(task.WithLogger(logger))
	tm.Cleanup(func() error { return errCleanup1 })
	tm.Cleanup(func() error { return errCleanup2 })

	err := tm.Stop()
	require.Error(t, err)

	// Log the error — this exercises CleanupErrors.LogValue via xerrors.Log.
	logger.Error("shutdown failed", xerrors.Log(err))
	assert.Contains(t, buf.String(), "cleanup_errors")
	assert.Contains(t, buf.String(), errCleanup1.Error())
	assert.Contains(t, buf.String(), errCleanup2.Error())
}

// neverStopTask is a task that blocks forever regardless of context cancellation.
type neverStopTask struct{}

func (t *neverStopTask) Name() string { return "never-stop" }
func (t *neverStopTask) Run(_ context.Context) error {
	select {} // block forever
}
