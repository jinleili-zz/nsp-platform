## Why

`Engine.Submit` currently persists a transaction and returns immediately, which leaves callers to build their own polling loop around `Engine.Query` when they need a final result. That makes synchronous integration points harder to implement and obscures the distinction between saga transaction timeout, caller wait timeout, transient read-path failures while waiting, and the edge case where a persisted transaction stays pending because no executor picks it up.

## What Changes

- Add a blocking `Engine.SubmitAndWait(ctx, def)` API that submits a saga transaction and waits until it reaches a terminal state or the caller context is canceled.
- Define clear return semantics for success, terminal failure, caller-side cancellation/timeout, transient `Query` failures during waiting, and unexpected disappearance of a submitted transaction.
- Export and document sentinel errors for terminal saga failure and for a transaction that disappears after successful submit so callers can distinguish them from generic infrastructure errors with `errors.Is`.
- Keep saga transaction timeout behavior driven by `SagaBuilder.WithTimeout` / `SagaDefinition.TimeoutSec`, while using `ctx` only to control the current submit-and-wait call lifecycle.
- Cover synchronous steps, asynchronous polling steps, failure/compensation, caller cancellation, query-path failures, disappeared transactions, no-active-executor scenarios, and queue-dispatch edge cases with tests.
- Update saga-facing documentation and examples to describe when to use `Submit` vs `SubmitAndWait`, clarify that waiting requires at least one running engine instance to advance the transaction, and warn that the current coordinator queue overflow/recovery behavior can leave transactions pending until a future recovery opportunity.

## Capabilities

### New Capabilities
- `saga-submit-and-wait`: Blocking saga submission that waits for a terminal transaction result while preserving existing asynchronous execution semantics and defining wait-loop error handling.

### Modified Capabilities
- N/A

## Impact

- Affected code: `saga/engine.go`, possibly coordinator-adjacent comments/helpers, saga tests, and any shared helpers needed for wait polling, query retry behavior, or terminal status handling.
- Affected API: new exported saga engine method and exported error/type needed to report terminal transaction failure and disappeared transactions cleanly.
- Affected docs: `AGENTS.md`, `docs/saga.md`, `docs/modules/saga.md`, and `saga/README.md`.
- Runtime/dependency impact: no new external service dependency; implementation should continue to work across multi-instance execution by relying on persisted transaction state rather than in-memory notifications.
