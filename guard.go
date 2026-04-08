package task

import (
	"errors"
	"sync/atomic"

	"github.com/wood-jp/xerrors/stacktrace"
)

// ErrAlreadyStarted is returned by any task's Run method if called more than once.
var ErrAlreadyStarted = errors.New("task already started")

// Guard prevents a task's Run method from being called more than once.
// Embed it in a Task struct and call TryStart at the top of Run.
type Guard struct{ started atomic.Bool }

// TryStart returns nil on the first call and a wrapped [ErrAlreadyStarted] on
// all subsequent calls.
func (g *Guard) TryStart() error {
	if !g.started.CompareAndSwap(false, true) {
		return stacktrace.Wrap(ErrAlreadyStarted)
	}
	return nil
}
