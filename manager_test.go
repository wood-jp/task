package task_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wood-jp/task"
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
	tm.Cleanup(func() { cleanupCheck = append(cleanupCheck, 1) })
	tm.Cleanup(func() { cleanupCheck = append(cleanupCheck, 2) })

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
	tm.Cleanup(func() { cleanupCheck = append(cleanupCheck, 1) })
	tm.Cleanup(func() { cleanupCheck = append(cleanupCheck, 2) })

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
		tm.Cleanup(func() { cleanupCheck = append(cleanupCheck, 1) })
		tm.Cleanup(func() { cleanupCheck = append(cleanupCheck, 2) })

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
		tm.Cleanup(func() { cleanupCheck = append(cleanupCheck, 1) })
		tm.Cleanup(func() { cleanupCheck = append(cleanupCheck, 2) })

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
		tm.Cleanup(func() { cleanupCheck = append(cleanupCheck, 1) })
		tm.Cleanup(func() { cleanupCheck = append(cleanupCheck, 2) })

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

	// stubTask ignores context cancellation and never stops on its own.
	stubTask := &neverStopTask{}

	tm := task.NewManager(task.WithShutdownTimeout(10 * time.Millisecond))
	require.NoError(t, tm.Run(stubTask))

	go func() {
		tm.Stop() //nolint:errcheck
	}()

	err := tm.Wait()
	assert.ErrorIs(t, err, task.ErrShutdownTimeout)
}

// neverStopTask is a task that blocks forever regardless of context cancellation.
type neverStopTask struct{}

func (t *neverStopTask) Name() string { return "never-stop" }
func (t *neverStopTask) Run(_ context.Context) error {
	select {} // block forever
}
