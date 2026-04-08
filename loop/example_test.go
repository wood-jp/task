package loop_test

import (
	"context"
	"log/slog"
	"time"

	"github.com/wood-jp/task/loop"
)

// ExampleNewTask demonstrates creating a loop Task that retries an action
// continuously with a delay between runs.
func ExampleNewTask() {
	t := loop.NewTask(
		func(ctx context.Context) error {
			// perform a unit of work
			return nil
		},
		"my-worker",
		loop.WithDelay(5*time.Second),
		loop.WithLogger(slog.Default()),
		loop.WithContinueOnError(),
	)
	_ = t
}
