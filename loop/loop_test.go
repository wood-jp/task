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

// stubTask is a minimal task.Task for testing.
type stubTask struct {
	name string
	run  func(context.Context) error
}

func (s *stubTask) Name() string { return s.name }
func (s *stubTask) Run(ctx context.Context) error {
	if s.run != nil {
		return s.run(ctx)
	}
	return nil
}

// Ensure stubTask implements task.Task.
var _ task.Task = (*stubTask)(nil)

func TestName(t *testing.T) {
	t.Parallel()

	lt := loop.NewTask(func(_ context.Context) (task.Task, error) {
		return &stubTask{name: "inner"}, nil
	}, "myname")
	assert.Equal(t, "loop: myname", lt.Name())
}

func TestRunsRepeatedly(t *testing.T) {
	t.Parallel()

	var count int
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	lt := loop.NewTask(func(_ context.Context) (task.Task, error) {
		count++
		if count >= 3 {
			cancel()
		}
		return &stubTask{name: "inner"}, nil
	}, "repeated")

	err := lt.Run(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 3)
}

func TestFactoryError(t *testing.T) {
	t.Parallel()

	factoryErr := errors.New("factory failed")
	callCount := 0

	lt := loop.NewTask(func(_ context.Context) (task.Task, error) {
		callCount++
		if callCount == 2 {
			return nil, factoryErr
		}
		return &stubTask{name: "inner"}, nil
	}, "factory-err")

	err := lt.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, factoryErr)

	ec := errcontext.Get(err)
	require.NotNil(t, ec)
	assert.Equal(t, slog.Uint64Value(2), ec["run"])
	assert.Equal(t, slog.StringValue("factory"), ec["phase"])
}

func TestRunError(t *testing.T) {
	t.Parallel()

	runErr := errors.New("run failed")

	lt := loop.NewTask(func(_ context.Context) (task.Task, error) {
		return &stubTask{
			name: "failing-inner",
			run:  func(_ context.Context) error { return runErr },
		}, nil
	}, "run-err")

	err := lt.Run(t.Context())
	require.Error(t, err)
	assert.ErrorIs(t, err, runErr)

	ec := errcontext.Get(err)
	require.NotNil(t, ec)
	assert.Equal(t, slog.Uint64Value(1), ec["run"])
	assert.Equal(t, slog.StringValue("run"), ec["phase"])
	assert.Equal(t, slog.StringValue("failing-inner"), ec["task"])
}

func TestContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	lt := loop.NewTask(func(_ context.Context) (task.Task, error) {
		return &stubTask{
			name: "blocking",
			run: func(c context.Context) error {
				cancel()
				<-c.Done()
				return nil
			},
		}, nil
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

		lt := loop.NewTask(func(_ context.Context) (task.Task, error) {
			return &stubTask{name: "inner"}, nil
		}, "delay-cancel", loop.WithDelay(100*time.Millisecond))

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
		lt := loop.NewTask(func(_ context.Context) (task.Task, error) {
			callCount++
			runTimes = append(runTimes, time.Now())
			if callCount >= 3 {
				cancel()
			}
			return &stubTask{name: "inner"}, nil
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

		lt := loop.NewTask(func(_ context.Context) (task.Task, error) {
			if firstRunTime.IsZero() {
				firstRunTime = time.Now()
				cancel()
			}
			return &stubTask{name: "inner"}, nil
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
	lt := loop.NewTask(func(_ context.Context) (task.Task, error) {
		callCount++
		if callCount >= 2 {
			cancel()
		}
		return &stubTask{name: "logged-inner"}, nil
	}, "logger-test", loop.WithLogger(logger))

	err := lt.Run(ctx)
	require.NoError(t, err)

	log := buf.String()
	assert.Contains(t, log, "run starting")
	assert.Contains(t, log, "run completed")
	assert.Contains(t, log, "logged-inner")
}

func TestAlreadyStarted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		lt := loop.NewTask(func(_ context.Context) (task.Task, error) {
			return &stubTask{
				name: "inner",
				run: func(c context.Context) error {
					<-c.Done()
					return nil
				},
			}, nil
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
