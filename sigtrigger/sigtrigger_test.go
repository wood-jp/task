package sigtrigger_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wood-jp/task"
	"github.com/wood-jp/task/sigtrigger"
)

const waitTime = 50 * time.Millisecond

func sendSignal(t *testing.T, sig syscall.Signal) {
	t.Helper()
	err := syscall.Kill(syscall.Getpid(), sig)
	require.NoError(t, err)
}

func TestName(t *testing.T) {
	t.Parallel()

	tsk := sigtrigger.NewTask(sigtrigger.WithSignals(syscall.SIGHUP))
	assert.Equal(t, "sigtrigger task (hangup)", tsk.Name())
}

func TestActionCalledOnSignal(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	called := make(chan struct{}, 1)
	tsk := sigtrigger.NewTask(
		sigtrigger.WithSignals(syscall.SIGHUP),
		sigtrigger.WithAction(func(_ context.Context) error {
			called <- struct{}{}
			return nil
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- tsk.Run(ctx) }()

	sendSignal(t, syscall.SIGHUP)

	timer := time.NewTimer(waitTime)
	t.Cleanup(func() { timer.Stop() })
	select {
	case <-called:
		// action was called
	case <-timer.C:
		t.Fatal("action was not called after signal")
	}

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(waitTime):
		t.Fatal("task did not stop after context cancel")
	}
}

func TestMultipleActionsExecuteInOrder(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	var order []int
	done := make(chan struct{}, 1)

	tsk := sigtrigger.NewTask(
		sigtrigger.WithSignals(syscall.SIGUSR1),
		sigtrigger.WithAction(func(_ context.Context) error {
			order = append(order, 1)
			return nil
		}),
		sigtrigger.WithAction(func(_ context.Context) error {
			order = append(order, 2)
			done <- struct{}{}
			return nil
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- tsk.Run(ctx) }()

	sendSignal(t, syscall.SIGUSR1)

	timer := time.NewTimer(waitTime)
	t.Cleanup(func() { timer.Stop() })
	select {
	case <-done:
		assert.Equal(t, []int{1, 2}, order)
	case <-timer.C:
		t.Fatal("actions did not complete after signal")
	}

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(waitTime):
		t.Fatal("task did not stop after context cancel")
	}
}

func TestActionErrorTerminates(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	actionErr := errors.New("action failed")
	tsk := sigtrigger.NewTask(
		sigtrigger.WithSignals(syscall.SIGUSR2),
		sigtrigger.WithAction(func(_ context.Context) error {
			return actionErr
		}),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- tsk.Run(t.Context()) }()

	sendSignal(t, syscall.SIGUSR2)

	timer := time.NewTimer(waitTime)
	t.Cleanup(func() { timer.Stop() })
	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, actionErr)
	case <-timer.C:
		t.Fatal("task did not stop after action error")
	}
}

func TestContinueOnError(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	secondCalled := make(chan struct{}, 1)
	tsk := sigtrigger.NewTask(
		sigtrigger.WithSignals(syscall.SIGCONT),
		sigtrigger.WithLogger(logger),
		sigtrigger.WithContinueOnError(),
		sigtrigger.WithAction(func(_ context.Context) error {
			return errors.New("transient error")
		}),
		sigtrigger.WithAction(func(_ context.Context) error {
			secondCalled <- struct{}{}
			return nil
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- tsk.Run(ctx) }()

	sendSignal(t, syscall.SIGCONT)

	timer := time.NewTimer(waitTime)
	t.Cleanup(func() { timer.Stop() })
	select {
	case <-secondCalled:
		// second action ran despite first erroring
	case <-timer.C:
		t.Fatal("second action was not called — error may have terminated the task")
	}

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(waitTime):
		t.Fatal("task did not stop after context cancel")
	}

	assert.Contains(t, buf.String(), "sigtrigger action failed")
}

func TestContextCancellation(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	tsk := sigtrigger.NewTask(sigtrigger.WithSignals(syscall.SIGIO))

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- tsk.Run(ctx) }()

	// Task should not exit on its own.
	timer := time.NewTimer(waitTime)
	t.Cleanup(func() { timer.Stop() })
	select {
	case err := <-errCh:
		t.Fatalf("task exited unexpectedly: %v", err)
	case <-timer.C:
	}

	cancel()

	timer.Reset(waitTime)
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-timer.C:
		t.Fatal("task did not stop after context cancellation")
	}
}

func TestErrAlreadyStarted(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	tsk := sigtrigger.NewTask(sigtrigger.WithSignals(syscall.SIGWINCH))

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- tsk.Run(ctx) }()

	// Give the goroutine a moment to start.
	time.Sleep(5 * time.Millisecond)

	err := tsk.Run(ctx)
	assert.ErrorIs(t, err, task.ErrAlreadyStarted)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(waitTime):
		t.Fatal("first Run did not stop after context cancel")
	}
}

func TestAddActionConcurrent(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	var count atomic.Int32
	fired := make(chan struct{}, 2)

	tsk := sigtrigger.NewTask(sigtrigger.WithSignals(syscall.SIGPIPE))

	// Add action before Run starts.
	tsk.AddAction(func(_ context.Context) error {
		count.Add(1)
		fired <- struct{}{}
		return nil
	})

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- tsk.Run(ctx) }()

	// Concurrently add another action after Run starts.
	go func() {
		tsk.AddAction(func(_ context.Context) error {
			count.Add(1)
			return nil
		})
	}()

	// Give goroutines a moment to settle, then send a signal.
	time.Sleep(5 * time.Millisecond)
	sendSignal(t, syscall.SIGPIPE)

	timer := time.NewTimer(waitTime)
	t.Cleanup(func() { timer.Stop() })
	select {
	case <-fired:
		// At least the first action ran.
		assert.GreaterOrEqual(t, count.Load(), int32(1))
	case <-timer.C:
		t.Fatal("action was not called after signal")
	}

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(waitTime):
		t.Fatal("task did not stop after context cancel")
	}
}

func TestSignalFiresMultipleTimes(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	fired := make(chan struct{}, 10)
	tsk := sigtrigger.NewTask(
		sigtrigger.WithSignals(syscall.SIGURG),
		sigtrigger.WithAction(func(_ context.Context) error {
			fired <- struct{}{}
			return nil
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- tsk.Run(ctx) }()

	for range 3 {
		sendSignal(t, syscall.SIGURG)
		timer := time.NewTimer(waitTime)
		select {
		case <-fired:
			timer.Stop()
		case <-timer.C:
			t.Fatal("action was not called for signal delivery")
		}
	}

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(waitTime):
		t.Fatal("task did not stop after context cancel")
	}
}

func TestEmptySignalsPanic(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() {
		sigtrigger.NewTask(sigtrigger.WithSignals())
	})
}
