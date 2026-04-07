package ossignal_test

import (
	"bytes"
	"context"
	"log/slog"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wood-jp/task/ossignal"
)

const (
	// waitTime is the maximum duration to wait when asserting that a task has stopped or not stopped.
	waitTime = time.Millisecond * 50
)

func TestWithLogger(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	// use a signal that won't cause issues with testing
	task := ossignal.NewTask(ossignal.WithLogger(logger), ossignal.WithSignals(syscall.SIGUSR1))

	// start the task (which blocks) and capture any resulting error in a channel
	errCh := make(chan error)
	go func() {
		errCh <- task.Run(t.Context())
	}()

	// send the expected signal, the task should now stop
	err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR1)
	require.NoError(t, err)

	// verify that the task stops and that the log message was written
	timer := time.NewTimer(waitTime)
	t.Cleanup(func() { timer.Stop() })
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-timer.C:
		t.Fatal("task failed to stop after signal")
	}
	assert.Contains(t, buf.String(), "os signal received")
}

func TestWithSignalLogLevel(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// use a signal that won't cause issues with testing
	task := ossignal.NewTask(ossignal.WithLogger(logger), ossignal.WithSignalLogLevel(slog.LevelDebug), ossignal.WithSignals(syscall.SIGUSR2))

	// start the task (which blocks) and capture any resulting error in a channel
	errCh := make(chan error)
	go func() {
		errCh <- task.Run(t.Context())
	}()

	// send the expected signal, the task should now stop
	err := syscall.Kill(syscall.Getpid(), syscall.SIGUSR2)
	require.NoError(t, err)

	// verify that the task stops and that the log message was written at the configured level
	timer := time.NewTimer(waitTime)
	t.Cleanup(func() { timer.Stop() })
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-timer.C:
		t.Fatal("task failed to stop after signal")
	}
	assert.Contains(t, buf.String(), "DEBUG")
	assert.Contains(t, buf.String(), "os signal received")
}

func TestSignal(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	// use a signal that won't cause issues with testing
	task := ossignal.NewTask(ossignal.WithSignals(syscall.SIGCONT))
	assert.Equal(t, "os signal task (continued)", task.Name())

	// start the task (which blocks) and capture any resulting error in a channel
	errCh := make(chan error)
	go func() {
		ctx := t.Context()
		err := task.Run(ctx)
		errCh <- err
	}()

	timer := time.NewTimer(waitTime)
	t.Cleanup(func() {
		timer.Stop()
	})

	// waiting around for a while, the task should not exit
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-timer.C:
	}

	// send the expected signal, the task should now stop
	err := syscall.Kill(syscall.Getpid(), syscall.SIGCONT)
	require.NoError(t, err)

	// verify that the task stops (wait a max amount of time for this)
	timer.Reset(waitTime)
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-timer.C:
		t.Fatal("os signal task failed to exit after being signalled")
	}
}

func TestContext(t *testing.T) {
	t.Parallel()
	// Note: Cannot use synctest.Test here because this uses OS signals

	// use a different signal from the other test
	task := ossignal.NewTask(ossignal.WithSignals(syscall.SIGIO))
	assert.Equal(t, "os signal task (I/O possible)", task.Name())

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	// start the task (which blocks) and capture any resulting error in a channel
	errCh := make(chan error)
	t.Cleanup(func() {
		close(errCh)
	})
	go func() {
		err := task.Run(ctx)
		errCh <- err
	}()

	timer := time.NewTimer(waitTime)
	t.Cleanup(func() {
		timer.Stop()
	})

	// waiting around for a while, the task should not exit
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-timer.C:
	}

	// cancel the context, the task should now stop
	cancel()

	// verify that the task stops (wait a max amount of time for this)
	timer.Reset(waitTime)
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-timer.C:
		t.Fatal("task failed to stop when context was cancelled")
	}
}
