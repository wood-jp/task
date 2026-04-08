package sigtrigger_test

import (
	"context"
	"log/slog"

	"github.com/wood-jp/task/sigtrigger"
)

// ExampleNewTask demonstrates creating a sigtrigger Task that reloads
// configuration each time SIGHUP is received.
func ExampleNewTask() {
	t := sigtrigger.NewTask(
		func(ctx context.Context) error {
			// reload configuration from disk
			return nil
		},
		sigtrigger.WithLogger(slog.Default()),
		sigtrigger.WithContinueOnError(),
	)
	_ = t
}
