package task_test

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/wood-jp/task"
)

// contextTask is a Task that blocks until its context is cancelled.
type contextTask struct{ name string }

func (t *contextTask) Name() string { return t.name }
func (t *contextTask) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// ExampleManager demonstrates creating a Manager, running a task, and stopping cleanly.
func ExampleManager() {
	m := task.NewManager()

	if err := m.Run(&contextTask{name: "worker"}); err != nil {
		log.Fatal(err)
	}

	// Stop cancels all tasks and waits for them to finish.
	if err := m.Stop(); err != nil {
		log.Fatal(err)
	}
}

// ExampleManager_Cleanup demonstrates registering a cleanup function that runs
// after all tasks have stopped.
func ExampleManager_Cleanup() {
	m := task.NewManager()

	m.Cleanup(func() error {
		fmt.Println("cleanup: closing database")
		return nil
	})

	if err := m.Run(&contextTask{name: "worker"}); err != nil {
		log.Fatal(err)
	}

	_ = m.Stop()
	// Output:
	// cleanup: closing database
}

// ExampleGuard demonstrates the Guard's double-run prevention.
func ExampleGuard() {
	var g task.Guard
	fmt.Println(g.TryStart() == nil)
	fmt.Println(errors.Is(g.TryStart(), task.ErrAlreadyStarted))
	// Output:
	// true
	// true
}
