package loop_test

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
	"github.com/wood-jp/task/loop"
	"github.com/wood-jp/xerrors/errcontext"
)

const waitTime = 50 * time.Millisecond

func TestName(t *testing.T) {
	t.Parallel()

	lt := loop.NewTask(func(_ context.Context) error { return nil }, "myname")
	assert.Equal(t, "loop: myname", lt.Name())
}

func TestRunsRepeatedly(t *testing.T) {
	t.Parallel()

	var count int
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	lt := loop.NewTask(func(_ context.Context) error {
		count++
		if count >= 3 {
			cancel()
		}
		return nil
	}, "repeated")

	err := lt.Run(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 3)
}

func TestActionError(t *testing.T) {
	t.Parallel()

	actionErr := errors.New("action failed")
	callCount := 0

	lt := loop.NewTask(func(_ context.Context) error {
		callCount++
		if callCount == 2 {
			return actionErr
		}
		return nil
	}, "action-err")

	err := lt.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, actionErr)

	ec := errcontext.Get(err)
	require.NotNil(t, ec)
	assert.Equal(t, slog.Uint64Value(2), ec["run"])
}

func TestContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	lt := loop.NewTask(func(c context.Context) error {
		cancel()
		<-c.Done()
		return nil
	}, "ctx-cancel")

	errCh := make(chan error, 1)
	go func() { errCh <- lt.Run(ctx) }()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(waitTime):
		t.Fatal("task did not stop after context cancellation")
	}
}

func TestDelayCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		lt := loop.NewTask(func(_ context.Context) error { return nil }, "delay-cancel", loop.WithDelay(100*time.Millisecond))

		errCh := make(chan error, 1)
		go func() { errCh <- lt.Run(ctx) }()

		// Wait until the loop goroutine is blocked on the delay timer, then cancel.
		synctest.Wait()
		cancel()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(waitTime):
			t.Fatal("task did not stop after context cancellation during delay")
		}
	})
}

func TestDelay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		delay := 20 * time.Millisecond
		runTimes := make([]time.Time, 0, 3)

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		callCount := 0
		lt := loop.NewTask(func(_ context.Context) error {
			callCount++
			runTimes = append(runTimes, time.Now())
			if callCount >= 3 {
				cancel()
			}
			return nil
		}, "delay", loop.WithDelay(delay))

		err := lt.Run(ctx)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(runTimes), 3)

		// Fake time advances exactly one delay period between each run.
		gap1 := runTimes[1].Sub(runTimes[0])
		gap2 := runTimes[2].Sub(runTimes[1])
		assert.GreaterOrEqual(t, gap1, delay, "expected delay between run 1 and 2")
		assert.GreaterOrEqual(t, gap2, delay, "expected delay between run 2 and 3")
	})
}

func TestInitialDelay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		delay := 20 * time.Millisecond
		start := time.Now()
		firstRunTime := time.Time{}

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		lt := loop.NewTask(func(_ context.Context) error {
			if firstRunTime.IsZero() {
				firstRunTime = time.Now()
				cancel()
			}
			return nil
		}, "initial-delay", loop.WithDelay(delay), loop.WithInitialDelay())

		err := lt.Run(ctx)
		require.NoError(t, err)

		elapsed := firstRunTime.Sub(start)
		assert.GreaterOrEqual(t, elapsed, delay, "expected initial delay before first run")
	})
}

func TestWithLogger(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	callCount := 0
	lt := loop.NewTask(func(_ context.Context) error {
		callCount++
		if callCount >= 2 {
			cancel()
		}
		return nil
	}, "logger-test", loop.WithLogger(logger))

	err := lt.Run(ctx)
	require.NoError(t, err)

	log := buf.String()
	assert.Contains(t, log, "run starting")
	assert.Contains(t, log, "run completed")
}

func TestAlreadyStarted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		lt := loop.NewTask(func(c context.Context) error {
			<-c.Done()
			return nil
		}, "already-started")

		errCh := make(chan error, 1)
		go func() { errCh <- lt.Run(ctx) }()

		// Wait until the goroutine is blocked on <-c.Done() before calling Run again.
		synctest.Wait()

		err := lt.Run(ctx)
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

func TestContinueOnError(t *testing.T) {
	t.Parallel()

	actionErr := errors.New("transient error")
	callCount := 0

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	lt := loop.NewTask(func(_ context.Context) error {
		callCount++
		if callCount <= 2 {
			return actionErr
		}
		cancel()
		return nil
	}, "continue-on-err", loop.WithContinueOnError())

	err := lt.Run(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, callCount, 3)
}

func TestContinueOnErrorCancelledContext(t *testing.T) {
	t.Parallel()

	actionErr := errors.New("error on cancel")

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	lt := loop.NewTask(func(_ context.Context) error {
		cancel()
		return actionErr
	}, "continue-on-err-cancel", loop.WithContinueOnError())

	err := lt.Run(ctx)
	require.NoError(t, err)
}
