// Package main demonstrates graceful shutdown using the task package.
// It runs a poll task that logs every 2 seconds alongside an ossignal task
// that triggers shutdown on SIGINT or SIGTERM. Press Ctrl+C to stop.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/wood-jp/task"
	"github.com/wood-jp/task/ossignal"
	"github.com/wood-jp/task/poll"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	m := task.NewManager(
		task.WithLogger(logger),
	)

	m.Cleanup(func() error {
		logger.Info("cleanup: releasing resources")
		return nil
	})

	worker := poll.NewTask(
		func(ctx context.Context) error {
			logger.Info("worker: tick")
			return nil
		},
		"heartbeat",
		2*time.Second,
		poll.WithRunAtStart(),
		poll.WithLogger(logger),
		poll.WithContinueOnError(),
	)

	shutdown := ossignal.NewTask(
		ossignal.WithLogger(logger),
	)

	if err := m.Run(worker, shutdown); err != nil {
		logger.Error("failed to start tasks", slog.Any("error", err))
		os.Exit(1)
	}

	if err := m.Wait(); err != nil {
		logger.Error("manager stopped with error", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("shutdown complete")
}
