package poll_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wood-jp/task"
	"github.com/wood-jp/task/poll"
)

const waitTime = 50 * time.Millisecond

func TestName(t *testing.T) {
	t.Parallel()

	pt := poll.NewTask(func(_ context.Context) error { return nil }, "myname", time.Second)
	assert.Equal(t, "poll: myname", pt.Name())
}

func TestRunsOnInterval(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		interval := 10 * time.Millisecond
		var count atomic.Int64

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		pt := poll.NewTask(func(_ context.Context) error {
			count.Add(1)
			return nil
		}, "interval", interval)

		errCh := make(chan error, 1)
		go func() { errCh <- pt.Run(ctx) }()

		// Advance fake time by 3 intervals.
		for range 3 {
			synctest.Wait()
			time.Sleep(interval)
		}
		synctest.Wait()

		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(waitTime):
			t.Fatal("task did not stop after context cancellation")
		}

		assert.GreaterOrEqual(t, count.Load(), int64(3))
	})
}

func TestRunAtStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		interval := 10 * time.Millisecond
		var count atomic.Int64

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		pt := poll.NewTask(func(_ context.Context) error {
			count.Add(1)
			return nil
		}, "run-at-start", interval, poll.WithRunAtStart())

		errCh := make(chan error, 1)
		go func() { errCh <- pt.Run(ctx) }()

		// Wait for run-at-start to execute, then advance 2 intervals.
		synctest.Wait()
		assert.GreaterOrEqual(t, count.Load(), int64(1), "expected at least one call before first tick")

		for range 2 {
			synctest.Wait()
			time.Sleep(interval)
		}
		synctest.Wait()

		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(waitTime):
			t.Fatal("task did not stop after context cancellation")
		}

		// Should have the initial call plus at least 2 ticks.
		assert.GreaterOrEqual(t, count.Load(), int64(3))
	})
}

func TestContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	pt := poll.NewTask(func(_ context.Context) error {
		return nil
	}, "ctx-cancel", time.Hour)

	errCh := make(chan error, 1)
	go func() { errCh <- pt.Run(ctx) }()

	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(waitTime):
		t.Fatal("task did not stop after context cancellation")
	}
}

func TestContextCancellationDuringAction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		interval := 10 * time.Millisecond
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		pt := poll.NewTask(func(c context.Context) error {
			cancel()
			<-c.Done()
			return nil
		}, "ctx-cancel-during-action", interval)

		errCh := make(chan error, 1)
		go func() { errCh <- pt.Run(ctx) }()

		// Advance to trigger the first tick.
		synctest.Wait()
		time.Sleep(interval)

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(waitTime):
			t.Fatal("task did not stop after context cancellation during action")
		}
	})
}

func TestActionError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		interval := 10 * time.Millisecond
		actionErr := errors.New("action failed")

		pt := poll.NewTask(func(_ context.Context) error {
			return actionErr
		}, "action-err", interval)

		errCh := make(chan error, 1)
		go func() { errCh <- pt.Run(t.Context()) }()

		// Advance to trigger the first tick.
		synctest.Wait()
		time.Sleep(interval)

		select {
		case err := <-errCh:
			require.Error(t, err)
			assert.ErrorIs(t, err, actionErr)
		case <-time.After(waitTime):
			t.Fatal("task did not stop after action error")
		}
	})
}

func TestContinueOnError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		interval := 10 * time.Millisecond
		var count atomic.Int64

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		pt := poll.NewTask(func(_ context.Context) error {
			count.Add(1)
			return errors.New("transient error")
		}, "continue-on-error", interval, poll.WithContinueOnError())

		errCh := make(chan error, 1)
		go func() { errCh <- pt.Run(ctx) }()

		// Advance 3 intervals; errors should not terminate the task.
		for range 3 {
			synctest.Wait()
			time.Sleep(interval)
		}
		synctest.Wait()

		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(waitTime):
			t.Fatal("task did not stop after context cancellation")
		}

		assert.GreaterOrEqual(t, count.Load(), int64(3))
	})
}

func TestContinueOnErrorWithLogger(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		interval := 10 * time.Millisecond
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		pt := poll.NewTask(func(_ context.Context) error {
			return errors.New("logged error")
		}, "continue-with-logger", interval,
			poll.WithContinueOnError(),
			poll.WithLogger(logger),
		)

		errCh := make(chan error, 1)
		go func() { errCh <- pt.Run(ctx) }()

		// Trigger one tick.
		synctest.Wait()
		time.Sleep(interval)
		synctest.Wait()

		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(waitTime):
			t.Fatal("task did not stop after context cancellation")
		}

		assert.Contains(t, buf.String(), "poll action failed")
		assert.Contains(t, buf.String(), "logged error")
	})
}

func TestAlreadyStarted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		pt := poll.NewTask(func(c context.Context) error {
			<-c.Done()
			return nil
		}, "already-started", time.Hour)

		errCh := make(chan error, 1)
		go func() { errCh <- pt.Run(ctx) }()

		// Wait until the goroutine is blocked inside the action.
		synctest.Wait()

		err := pt.Run(ctx)
		assert.ErrorIs(t, err, task.ErrAlreadyStarted)

		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(waitTime):
			t.Fatal("first Run did not stop after context cancel")
		}
	})
}

func TestRunAtStartError(t *testing.T) {
	t.Parallel()

	actionErr := errors.New("start action failed")

	pt := poll.NewTask(func(_ context.Context) error {
		return actionErr
	}, "run-at-start-err", time.Hour, poll.WithRunAtStart())

	err := pt.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, actionErr)
}

func TestZeroIntervalPanic(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		poll.NewTask(func(_ context.Context) error { return nil }, "zero", 0)
	})

	assert.Panics(t, func() {
		poll.NewTask(func(_ context.Context) error { return nil }, "negative", -time.Second)
	})
}
