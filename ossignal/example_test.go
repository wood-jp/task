package ossignal_test

import (
	"log/slog"

	"github.com/wood-jp/task/ossignal"
)

// ExampleNewTask demonstrates creating an ossignal Task that listens for the
// default termination signals (SIGINT, SIGTERM, SIGQUIT).
func ExampleNewTask() {
	t := ossignal.NewTask(
		ossignal.WithLogger(slog.Default()),
	)
	_ = t
}
