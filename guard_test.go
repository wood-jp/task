package task_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wood-jp/task"
)

func TestGuardTryStartFirstCall(t *testing.T) {
	t.Parallel()

	var g task.Guard
	err := g.TryStart()
	require.NoError(t, err)
}

func TestGuardTryStartSecondCall(t *testing.T) {
	t.Parallel()

	var g task.Guard
	_ = g.TryStart()
	err := g.TryStart()
	require.Error(t, err)
}

func TestGuardTryStartErrorIs(t *testing.T) {
	t.Parallel()

	var g task.Guard
	_ = g.TryStart()
	err := g.TryStart()
	assert.ErrorIs(t, err, task.ErrAlreadyStarted)
}
