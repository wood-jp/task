# task

<!-- badges -->
[![Go Version](https://img.shields.io/github/go-mod/go-version/wood-jp/task)](https://pkg.go.dev/github.com/wood-jp/task)
[![CI](https://github.com/wood-jp/task/actions/workflows/ci.yml/badge.svg)](https://github.com/wood-jp/task/actions/workflows/ci.yml)
[![Coverage Status](https://coveralls.io/repos/github/wood-jp/task/badge.svg?branch=main)](https://coveralls.io/github/wood-jp/task?branch=main)
<!-- [![Release](https://img.shields.io/github/v/release/wood-jp/task)](https://github.com/wood-jp/task/releases) -->
[![Go Report Card](https://goreportcard.com/badge/github.com/wood-jp/task)](https://goreportcard.com/report/github.com/wood-jp/task)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
<!-- [![Go Reference](https://pkg.go.dev/badge/github.com/wood-jp/task.svg)](https://pkg.go.dev/github.com/wood-jp/task) -->
<!-- /badges -->

Manage a group of long-running background tasks that all stop when any one of them stops.

- [Stability](#stability)
- [Installation](#installation)
- [Task interface](#task-interface)
- [Manager](#manager)
  - [Running tasks](#running-tasks)
  - [Ephemeral tasks](#ephemeral-tasks)
  - [Cleanup](#cleanup)
  - [Waiting and stopping](#waiting-and-stopping)
  - [Options](#options)
- [Subpackages](#subpackages)
  - [ossignal](#ossignal)
  - [loop](#loop)
- [Contributing](#contributing)
- [Security](#security)
- [Attribution](#attribution)

## Stability

v1.x releases make no breaking changes to exported APIs. New functionality may be added in minor releases; patches are bug fixes, or administrative work only.

## Installation

Go 1.26.1 or later.

```bash
go get github.com/wood-jp/task
```

## Task interface

Any type that satisfies the `Task` interface can be managed by a `Manager`:

```go
type Task interface {
    Run(context.Context) error
    Name() string
}
```

`Run` should block until the context is cancelled or the task can no longer continue. `Run` must return `nil` when the context is cancelled — a non-nil error is treated as a failure and causes the manager to cancel all other tasks. In particular, do not return `ctx.Err()`: `context.Canceled` is a non-nil error and will be treated as a failure. `Name` provides a human-friendly label used in log output.

## Manager

`Manager` runs a group of tasks concurrently. When any task stops, whether due to an error or a clean exit, the manager cancels the shared context so all other tasks know to stop. Ephemeral tasks (registered via [`RunEphemeral`](#ephemeral-tasks)) are the exception: a clean exit from an ephemeral task does not trigger shutdown.

```go
m := task.NewManager(
    task.WithLogger(logger),
)

if err := m.Run(taskA, taskB, taskC); err != nil {
    // ErrManagerStopped if the manager has already stopped
}

if err := m.Wait(); err != nil {
    // first task error, plus any cleanup errors attached as CleanupErrors
}
```

### Running tasks

`Run` starts one or more tasks immediately. The tasks share the manager's internal context. If any task returns an error, the context is cancelled and the error is propagated through `Wait`.

```go
m.Run(taskA, taskB)
```

### Ephemeral tasks

`RunEphemeral` starts tasks that are expected to finish on their own without triggering shutdown of the rest of the group. Unlike `Run`, a clean exit from an ephemeral task does not cancel the manager context.

```go
m.RunEphemeral(migrationTask)
```

### Cleanup

`Cleanup` registers a function to run after all tasks have stopped, similar to `defer`. Functions are called in reverse registration order. Errors are collected and attached to the `Wait` return value as `CleanupErrors`; retrieve them with `xerrors.Extract`:

```go
m.Cleanup(db.Close)
m.Cleanup(cache.Flush)

if err := m.Wait(); err != nil {
    if cleanupErrs, ok := xerrors.Extract[task.CleanupErrors](err); ok {
        for _, ce := range cleanupErrs {
            logger.Error("cleanup failed", slog.Any("err", ce))
        }
    }
}
```

### Waiting and stopping

`Wait` blocks until all tasks finish and all cleanup functions have run. Repeated or concurrent calls all return the same result.

```go
err := m.Wait()
```

`Stop` cancels the context immediately and then calls `Wait`:

```go
err := m.Stop()
```

**Timeout behaviour:**

| Situation | Error returned |
| --- | --- |
| Tasks do not stop within the shutdown timeout | `ErrShutdownTimeout` |
| Cleanup functions do not complete within the cleanup timeout | `ErrCleanupTimeout` |
| One or more cleanup functions returned an error | `ErrCleanupFailed` (base) with `CleanupErrors` attached |

### Options

| Option | Default | Description |
| --- | --- | --- |
| `WithLogger(logger)` | discard | Logger for task start/stop/error events |
| `WithContext(ctx)` | `context.Background()` | Parent context; cancellation triggers shutdown |
| `WithShutdownTimeout(d)` | 30s | How long to wait for tasks to stop after cancellation |
| `WithCleanupTimeout(d)` | 10s | Total time budget for all cleanup functions |

## Subpackages

### ossignal

```text
github.com/wood-jp/task/ossignal
```

A `Task` implementation that listens for OS signals and returns `nil` when one is received, triggering an orderly shutdown of the manager.

Signal capture begins at construction time, so no signals are missed between `NewTask` and `Run`. Note however that only the first caught signal will trigger the task termination, even if captured before `Run` is called. In such a case `Run` will return immediately.

```go
sig := ossignal.NewTask(
    ossignal.WithLogger(logger),
)

m := task.NewManager(task.WithLogger(logger))
m.Run(sig, taskA, taskB)
if err := m.Wait(); err != nil {
    log.Fatal(err)
}
```

By default, `ossignal.NewTask` listens for `SIGINT`, `SIGTERM`, and `SIGQUIT`. Override with `WithSignals`:

```go
sig := ossignal.NewTask(
    ossignal.WithSignals(syscall.SIGUSR1),
)
```

Other options:

| Option | Default | Description |
| --- | --- | --- |
| `WithLogger(logger)` | discard | Logger for signal receipt |
| `WithSignalLogLevel(level)` | `slog.LevelInfo` | Log level used when a signal is received |
| `WithSignals(signals...)` | SIGINT, SIGTERM, SIGQUIT | Signals to listen for |
| `WithOnSignal(fn)` | none | Callback invoked after the signal is logged |

### loop

```text
github.com/wood-jp/task/loop
```

A `Task` implementation that repeatedly creates and runs an ephemeral task via a factory function. If the inner task returns `nil`, a new one is created and run again. If the factory or inner task returns an error, it is propagated and triggers shutdown. This enables patterns like "run this worker forever, restarting it cleanly each time it finishes."

```go
worker := loop.NewTask(
    func(ctx context.Context) (task.Task, error) {
        return NewWorker(ctx, db)
    },
    "worker",
    loop.WithLogger(logger),
)

m := task.NewManager(task.WithLogger(logger))
m.Run(sig, worker)
if err := m.Wait(); err != nil {
    log.Fatal(err)
}
```

The delay between runs (default: none) is measured from the completion of one run to the start of the next, so runs never overlap:

```go
worker := loop.NewTask(factory, "worker",
    loop.WithDelay(5*time.Second),     // sleep between runs
    loop.WithInitialDelay(),           // also sleep before the first run
)
```

Options:

| Option | Default | Description |
| --- | --- | --- |
| `WithLogger(logger)` | discard | Logger for per-run start/complete events |
| `WithDelay(d)` | 0 | Sleep between runs (context-aware) |
| `WithInitialDelay()` | false | Also apply the delay before the first run |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md).

## Attribution

*This library is a simplified fork of one written by [wood-jp](https://github.com/wood-jp) at [Zircuit](https://www.zircuit.com/). The original code is available here: [zkr-go-common-public/task](https://github.com/zircuit-labs/zkr-go-common-public/tree/main/task)*
