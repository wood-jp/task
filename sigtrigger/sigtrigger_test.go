package sigtrigger_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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

	tsk := sigtrigger.NewTask(func(_ context.Context) error { return nil }, sigtrigger.WithSignals(syscall.SIGHUP))
	assert.Equal(t, "sigtrigger task (hangup)", tsk.Name())
}

func TestActionCalledOnSignal(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	called := make(chan struct{}, 1)
	tsk := sigtrigger.NewTask(
		func(_ context.Context) error {
			called <- struct{}{}
			return nil
		},
		sigtrigger.WithSignals(syscall.SIGHUP),
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

func TestActionErrorTerminates(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	actionErr := errors.New("action failed")
	tsk := sigtrigger.NewTask(
		func(_ context.Context) error { return actionErr },
		sigtrigger.WithSignals(syscall.SIGUSR2),
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

	count := make(chan struct{}, 5)
	tsk := sigtrigger.NewTask(
		func(_ context.Context) error {
			count <- struct{}{}
			return errors.New("transient error")
		},
		sigtrigger.WithSignals(syscall.SIGCONT),
		sigtrigger.WithLogger(logger),
		sigtrigger.WithContinueOnError(),
	)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() { errCh <- tsk.Run(ctx) }()

	// Send two signals; the task should keep running despite errors.
	for range 2 {
		sendSignal(t, syscall.SIGCONT)
		timer := time.NewTimer(waitTime)
		select {
		case <-count:
			timer.Stop()
		case <-timer.C:
			t.Fatal("action was not called after signal")
		}
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

	tsk := sigtrigger.NewTask(
		func(_ context.Context) error { return nil },
		sigtrigger.WithSignals(syscall.SIGIO),
	)

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

	tsk := sigtrigger.NewTask(
		func(_ context.Context) error { return nil },
		sigtrigger.WithSignals(syscall.SIGWINCH),
	)

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

func TestSignalFiresMultipleTimes(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	fired := make(chan struct{}, 10)
	tsk := sigtrigger.NewTask(
		func(_ context.Context) error {
			fired <- struct{}{}
			return nil
		},
		sigtrigger.WithSignals(syscall.SIGURG),
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
		sigtrigger.NewTask(func(_ context.Context) error { return nil }, sigtrigger.WithSignals())
	})
}
