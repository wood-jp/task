package poll_test

import (
	"context"
	"log/slog"
	"time"

	"github.com/wood-jp/task/poll"
)

// ExampleNewTask demonstrates creating a poll Task that runs an action on a
// fixed interval, starting immediately.
func ExampleNewTask() {
	t := poll.NewTask(
		func(ctx context.Context) error {
			// sync state with an external system
			return nil
		},
		"state-syncer",
		30*time.Second,
		poll.WithRunAtStart(),
		poll.WithLogger(slog.Default()),
		poll.WithContinueOnError(),
	)
	_ = t
}
