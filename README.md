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
- [Guard](#guard)
- [Subpackages](#subpackages)
  - [ossignal](#ossignal)
  - [Action-based subpackages](#action-based-subpackages)
  - [loop](#loop)
  - [poll](#poll)
  - [sigtrigger](#sigtrigger)
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

## Guard

`Guard` prevents a task's `Run` method from being called more than once. Embed it in any `Task` struct and call `TryStart` at the top of `Run`:

```go
type MyTask struct {
    task.Guard
    // ...
}

func (t *MyTask) Run(ctx context.Context) error {
    if err := t.Guard.TryStart(); err != nil {
        return err // returns task.ErrAlreadyStarted on the second call
    }
    // ...
}
```

`TryStart` returns `nil` on the first call and a wrapped `ErrAlreadyStarted` on all subsequent calls. `ErrAlreadyStarted` is a sentinel error in the root `task` package and can be tested with `errors.Is`.

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

### Action-based subpackages

`loop`, `poll`, and `sigtrigger` are three variations of the same idea: wrap a `task.Action` in a `Task` that calls it repeatedly under different triggering conditions. The action signature is the same in all three:

```go
func(ctx context.Context) error
```

The only difference is *what causes the action to fire*:

| Package | Trigger |
| --- | --- |
| `loop` | The previous action completed (completion-based) |
| `poll` | A clock tick fired (time-based) |
| `sigtrigger` | An OS signal was received (event-based) |

All three share the same error behaviour: by default an action error propagates and triggers manager shutdown; `WithContinueOnError` logs the error and keeps running instead.

### loop

```text
github.com/wood-jp/task/loop
```

Calls the action in a tight loop: as soon as one call returns `nil`, the next begins (after an optional delay). Use this for work that should run continuously, restarting immediately after each completion.

```go
worker := loop.NewTask(
    func(ctx context.Context) error {
        return processNextBatch(ctx, db)
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

The delay between runs is measured from the completion of one call to the start of the next, so calls never overlap:

```go
worker := loop.NewTask(action, "worker",
    loop.WithDelay(5*time.Second),  // sleep between runs
    loop.WithInitialDelay(),        // also sleep before the first run
)
```

Options:

| Option | Default | Description |
| --- | --- | --- |
| `WithLogger(logger)` | discard | Logger for per-run start/complete events |
| `WithDelay(d)` | 0 | Sleep between runs (context-aware) |
| `WithInitialDelay()` | false | Also apply the delay before the first run |
| `WithContinueOnError()` | false | Log action errors and keep looping instead of propagating |

### poll

```text
github.com/wood-jp/task/poll
```

Calls the action on a fixed clock interval using a ticker. The interval is time-based: ticks fire regardless of how long the action takes. If the action takes longer than the interval, the next tick fires immediately after it completes (Go's ticker coalesces missed ticks).

```go
poller := poll.NewTask(
    func(ctx context.Context) error {
        return syncState(ctx, db)
    },
    "state-sync",
    30*time.Second,
    poll.WithLogger(logger),
)

m := task.NewManager(task.WithLogger(logger))
m.Run(sig, poller)
if err := m.Wait(); err != nil {
    log.Fatal(err)
}
```

Use `WithRunAtStart` to call the action immediately when `Run` is called, before the first tick:

```go
poller := poll.NewTask(action, "state-sync", 30*time.Second,
    poll.WithRunAtStart(),
)
```

Options:

| Option | Default | Description |
| --- | --- | --- |
| `WithLogger(logger)` | discard | Logger used when `WithContinueOnError` is active |
| `WithRunAtStart()` | false | Call the action immediately before the first tick |
| `WithContinueOnError()` | false | Log action errors and keep ticking instead of propagating |

### sigtrigger

```text
github.com/wood-jp/task/sigtrigger
```

Calls the action each time a configured OS signal is received. Unlike `ossignal`, which exits on the first signal, `sigtrigger` stays alive and re-runs the action on every delivery. Signal capture begins at construction time, so no signals are missed between `NewTask` and `Run`.

The signal channel has a buffer of one. A signal received while the action is running queues up and triggers another run immediately after the current one completes. Additional signals beyond that one are dropped (standard Go signal delivery behaviour).

```go
trig := sigtrigger.NewTask(
    func(ctx context.Context) error {
        return reloadConfig(ctx)
    },
    sigtrigger.WithLogger(logger),
)

m := task.NewManager(task.WithLogger(logger))
m.Run(sig, trig, server)
if err := m.Wait(); err != nil {
    log.Fatal(err)
}
```

By default, `sigtrigger.NewTask` listens for `SIGHUP`. Override with `WithSignals`:

```go
trig := sigtrigger.NewTask(action,
    sigtrigger.WithSignals(syscall.SIGUSR1),
)
```

Options:

| Option | Default | Description |
| --- | --- | --- |
| `WithLogger(logger)` | discard | Logger for signal receipt and (with `WithContinueOnError`) action errors |
| `WithSignals(signals...)` | SIGHUP | Signals to listen for |
| `WithContinueOnError()` | false | Log action errors and keep running instead of propagating |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md).

## Attribution

*This library is a simplified fork of one written by [wood-jp](https://github.com/wood-jp) at [Zircuit](https://www.zircuit.com/). The original code is available here: [zkr-go-common-public/task](https://github.com/zircuit-labs/zkr-go-common-public/tree/main/task)*
