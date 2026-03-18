# Code Review: Taskqueue SDK

**Commits:** `ee295d948` → `c4becd85` → `a10b5196` → `5aecb12a` → `754daeedc` → `d9396b21`
**Path:** `nsp-common/pkg/taskqueue/`, `nsp-common/pkg/taskqueue/asynqbroker/`, `nsp-common/pkg/taskqueue/rocketmqbroker/`
**Date:** 2026-03-03
**Reviewer:** Claude Code
**Status:** 11 issues open, 2 resolved

---

## Overview

This series introduces a complete distributed async task queue framework:

- `broker.go` / `consumer.go` — `Broker` / `Consumer` interface abstractions
- `task.go` — domain types (`Task`, `Workflow`, `StepTask`, `CallbackPayload`, priorities, statuses)
- `engine.go` — orchestration engine (submit workflow, handle callbacks, drive state machine)
- `store.go` / `pg_store.go` — `Store` interface + PostgreSQL persistence
- `handler.go` — `HandlerFunc` type
- `asynqbroker/` — asynq (Redis-backed) implementation of Broker and Consumer
- `rocketmqbroker/` — Apache RocketMQ implementation of Broker and Consumer
- `migrations/001_init.sql` — schema for `tq_workflows` and `tq_steps`
- Demo programs, guides, and compatibility documentation

The architecture (interface-first, pluggable broker, clean state machine) is well-designed.
The critical issues below must be resolved before production use.

---

## Issues

### High

**1. Wrong `resource_id` sent to workers in step payload** [OPEN]

`engine.go:enqueueStep` sends `step.WorkflowID` as `resource_id`, but workers expect the
actual resource being operated on (e.g., `"vpc-demo-001"`). The `WorkflowDefinition` has a
distinct `ResourceID` field that is never propagated to the per-step payload.

```go
// engine.go — bug
payload := map[string]interface{}{
    "task_id":     step.ID,
    "resource_id": step.WorkflowID,   // wrong: sends workflow UUID, not resource ID
    "task_params": step.Params,
}
```

Fix: store `ResourceID` on `StepTask` (add a field or inherit from the workflow) and use it here:

```go
"resource_id": step.ResourceID,   // or e.g. workflow.ResourceID passed through
```

---

**2. `MaxRetries` is stored but never enforced** [OPEN]

`handleStepFailure` immediately marks the workflow as `failed` on any single step failure,
ignoring `retry_count` and `max_retries`. The `RetryStep` API requires an external manual
call, doesn't increment `retry_count`, and doesn't check the `max_retries` ceiling.

```go
// engine.go:handleStepFailure — no retry logic
func (e *Engine) handleStepFailure(ctx context.Context, step *StepTask, errorMsg string) error {
    e.store.IncrementFailedSteps(ctx, step.WorkflowID)
    e.store.UpdateWorkflowStatus(ctx, step.WorkflowID, WorkflowStatusFailed, errorMsg)
    // max_retries is completely ignored
}
```

Fix: check `step.RetryCount < step.MaxRetries`; if so, increment `retry_count`, reset the
step to `pending`, and re-enqueue it instead of failing the workflow immediately.

---

**3. `Consumer.Start(ctx)` ignores the context parameter (asynqbroker)** [OPEN]

`asynqbroker/consumer.go:Start` accepts a `context.Context` (matching the `Consumer`
interface) but passes nothing to `server.Run`. Cancelling the caller's context has no effect;
the consumer runs until `Stop()` is explicitly called.

```go
// asynqbroker/consumer.go:102-103 — ctx is silently ignored
func (c *Consumer) Start(ctx context.Context) error {
    return c.server.Run(c.mux)
}
```

Fix: run `server.Run` in a goroutine and call `server.Shutdown()` when `ctx.Done()` fires:

```go
func (c *Consumer) Start(ctx context.Context) error {
    errCh := make(chan error, 1)
    go func() { errCh <- c.server.Run(c.mux) }()
    select {
    case <-ctx.Done():
        c.server.Shutdown()
        return ctx.Err()
    case err := <-errCh:
        return err
    }
}
```

---

**4. TOCTOU race in `checkAndCompleteWorkflow`** [OPEN]

The read-then-write sequence in `engine.go:checkAndCompleteWorkflow` is non-atomic. If two
callbacks for the final two steps arrive concurrently, both goroutines may read totals that
show the workflow is not yet complete and neither triggers completion — or both do.

```go
// engine.go:371-380 — non-atomic read-then-update
stats, _ := e.store.GetStepStats(ctx, workflowID)
if stats.Completed == stats.Total && stats.Failed == 0 {
    e.store.UpdateWorkflowStatus(ctx, workflowID, WorkflowStatusSucceeded, "")
}
```

Fix: use a single conditional `UPDATE` that both checks and transitions in one statement:

```sql
UPDATE tq_workflows
SET status = 'succeeded', updated_at = NOW()
WHERE id = $1
  AND status = 'running'
  AND completed_steps = total_steps
  AND failed_steps = 0
```

Check `RowsAffected() == 1` to determine whether this node "won" the transition.

---

**5. No tests** [OPEN]

~1800 lines of production code with zero test files. The engine state machine, callback
routing, step enqueue logic, and PostgreSQL scanning are entirely untested.

`NewEngineWithStore` was added explicitly to facilitate testability with a mock store — use
it. At minimum:

- Unit tests for `handleStepSuccess` → next-step enqueue
- Unit tests for `handleStepSuccess` → workflow completion (last step)
- Unit tests for `handleStepFailure` → retry vs. fail
- Unit tests for `checkAndCompleteWorkflow` concurrency scenario
- Table-driven tests for `scanStep` / `scanStepRow` SQL scanning

---

### Medium

**6. `Consumer.handlers` and `mu` are dead code (asynqbroker)** [OPEN]

`Handle()` stores the handler in a `handlers` map under a mutex, then immediately registers
the same logic as a closure in the `mux`. The map is never read again. This misleads readers
into thinking the map is authoritative.

```go
// asynqbroker/consumer.go — dead code
handlers map[string]taskqueue.HandlerFunc   // never read
mu       sync.RWMutex                       // guards a map nobody reads

func (c *Consumer) Handle(taskType string, handler taskqueue.HandlerFunc) {
    c.mu.Lock()
    c.handlers[taskType] = handler   // stored but never used
    c.mu.Unlock()
    c.mux.HandleFunc(taskType, func(...) { /* uses handler directly */ })
}
```

Fix: remove `handlers`, `mu`, and the map write from `Handle()`.

---

**7. `scanStep` and `scanStepRow` are nearly identical** [OPEN]

`pg_store.go` has two 45-line scan methods that differ only in whether they accept `*sql.Row`
or `*sql.Rows`. Any schema change must be applied twice.

```go
func (s *PostgresStore) scanStep(row *sql.Row) (*StepTask, error) { ... }    // line 275
func (s *PostgresStore) scanStepRow(rows *sql.Rows) (*StepTask, error) { ... } // line 320
```

Fix: extract a `Scanner` interface or accept `func(...interface{}) error` to eliminate
duplication:

```go
type rowScanner interface {
    Scan(dest ...interface{}) error
}
func (s *PostgresStore) scanStepFrom(row rowScanner) (*StepTask, error) { ... }
```

---

**8. Handler result is silently discarded in both implementations** [OPEN]

Both `asynqbroker` and `rocketmqbroker` consumers call the handler and discard its return:

```go
// asynqbroker/consumer.go:89
_ = result // result is used by the caller via CallbackSender
```

The comment is misleading: callers must manually call `callbackSender.Success/Fail` — nothing
in the framework enforces this. If a handler returns successfully without a callback, the
workflow stalls indefinitely with no error.

Fix: either invoke `callbackSender` inside the framework wrapper using the returned result, or
change the interface contract to require handlers to accept a `CallbackSender` parameter and
document the obligation explicitly.

---

**9. `log.Printf` used throughout instead of `pkg/logger`** [OPEN]

`engine.go`, `asynqbroker/consumer.go`, and `rocketmqbroker/consumer.go` all use the standard
library `log.Printf`. The project has a structured, zap-based logger in `pkg/logger`. The
`c4becd85` commit added a `Logger` field to `ConsumerConfig` only for asynq's internal
messages, not for the application-level log calls.

Adopt `pkg/logger` for all framework log output to get structured fields, log levels, and
consistent formatting.

---

### Low

**10. `StepTask.Params` / `TaskPayload.Params` type mismatch** [OPEN]

`StepTask.Params` is `string` (raw JSON), but `TaskPayload.Params` (what a handler receives)
is `[]byte`. The conversion is done implicitly in the consumer:

```go
Params: []byte(raw.TaskParams),
```

Consider using `json.RawMessage` consistently across both types to make the JSON contract
explicit.

---

**11. Schema migration approach is unversioned** [OPEN]

`pg_store.go:Migrate` re-executes `migrations/001_init.sql` on every startup using
`IF NOT EXISTS`. This risks running stale DDL as the schema evolves and provides no rollback
path.

Adopt a proper migration tool (e.g., `goose`, `golang-migrate`) before the schema changes
significantly. The current file organisation (`migrations/001_init.sql`) is already compatible
with both tools.

---

## Resolved Issues

**R1. `github.com/lib/pq` missing from `nsp-common/go.mod`** [RESOLVED]

Originally, demo programs imported `_ "github.com/lib/pq"` without the package appearing as
a direct dependency. Now present as `github.com/lib/pq v1.10.9`.

---

**R2. Direct dependencies marked `// indirect`** [RESOLVED]

`github.com/hibiken/asynq` and `github.com/apache/rocketmq-client-go/v2` were initially
marked `// indirect`; both are now correctly listed as direct dependencies.

---

## Additional Issues (Second Review Session — commits `a10b5196` through `d9396b21`)

**A1. `AGENTS.md` committed to repo root with hardcoded credentials** [OPEN]

`AGENTS.md` is an AI agent prompt file added in `754daeedc`. It contains:

```
postgres://saga:saga123@127.0.0.1:5432/taskqueue_rmq_test
```

This file should never be committed. Add `AGENTS.md` to `.gitignore` and rotate the
credential immediately if it is used in any shared environment.

**A2. `integration_main.go` is `package main` inside the `rocketmqbroker` library package** [OPEN]

`nsp-common/pkg/taskqueue/rocketmqbroker/integration_main.go` declares `package main`
alongside the library files (`broker.go`, `consumer.go`) in the same directory. Go only
permits one package per directory. This prevents `rocketmqbroker` from being imported.

Move to `nsp-demo/cmd/taskqueue-rocketmq-integration/main.go`.

**A3. Fictitious Go version `1.25.6` in `go.mod`** [OPEN]

`nsp-demo/go.mod` was bumped to `go 1.25.6` which does not exist. Use the latest real
stable release (`go 1.24.x`).

**A4. RocketMQ consumer `Stop()` path may leak push consumer** [OPEN]

If the external context passed to `Start()` is cancelled (not via `Stop()`), the function
returns without calling `pushConsumer.Shutdown()`, leaving the underlying RocketMQ connection
alive. Add `defer pushConsumer.Shutdown()` at the start of `Start()`.

---

## Summary

| # | Issue | Severity | Status |
|---|---|---|---|
| 1 | Wrong `resource_id` sent to workers | High | Open |
| 2 | `MaxRetries` not enforced | High | Open |
| 3 | `Consumer.Start` ignores context (asynq) | High | Open |
| 4 | TOCTOU race in `checkAndCompleteWorkflow` | High | Open |
| 5 | Zero tests | High | Open |
| 6 | Dead code: `handlers` map in asynq consumer | Medium | Open |
| 7 | `scanStep`/`scanStepRow` duplication | Medium | Open |
| 8 | Handler result silently discarded | Medium | Open |
| 9 | `log.Printf` instead of `pkg/logger` | Medium | Open |
| 10 | `Params` type mismatch (`string` vs `[]byte`) | Low | Open |
| 11 | Unversioned schema migration | Low | Open |
| R1 | `lib/pq` missing from `nsp-common/go.mod` | High | Resolved |
| R2 | Direct deps marked `// indirect` | Medium | Resolved |
| A1 | `AGENTS.md` with credentials in repo root | High | Open |
| A2 | `integration_main.go` in library package dir | High | Open |
| A3 | Fictitious Go version `1.25.6` | Medium | Open |
| A4 | RocketMQ consumer leaks on context cancel | Medium | Open |
