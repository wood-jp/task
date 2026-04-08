// Package task provides wrappers for simplified management of async functions.
package task

import "context"

// Action is a function that performs a unit of work. It must return nil when
// the context is cancelled.
type Action func(context.Context) error

// Task represents a background service.
type Task interface {
	// Run must execute the work of this service and block until the context
	// is cancelled, or until the service is unable to continue due to an error.
	// Run must return nil when the context is cancelled; a non-nil error signals
	// a failure and will cause the manager to cancel all other tasks. In
	// particular, do not return ctx.Err(): context.Canceled is a non-nil error
	// and will be treated as a failure.
	Run(context.Context) error

	// Name provides a human-friendly name for use in logging.
	Name() string
}
