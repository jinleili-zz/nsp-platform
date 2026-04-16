## 1. API and Engine Behavior

- [x] 1.1 Add `Engine.SubmitAndWait(ctx, def)` in `saga/engine.go` and reuse existing `Submit` + `Query` flow to wait for terminal status.
- [x] 1.2 Export and document `ErrTransactionFailed`, `ErrTransactionNotFound`, and `ErrTransactionDisappeared`, and define return semantics for transaction failure, explicit not-found queries, disappeared transactions, caller context cancellation, and infrastructure errors.
- [x] 1.3 Keep `ctx` scoped to submit-and-wait lifecycle only, and preserve `SagaDefinition.TimeoutSec` / `WithTimeout` as saga transaction timeout behavior.
- [x] 1.4 Define wait-loop handling for transient `Query` errors, persistent `Query` errors, explicit `ErrTransactionNotFound`, and disappeared transactions after successful submit, using package-private retry thresholds and backoff constants.
- [x] 1.5 Keep the normal polling interval internal to the package with a 500ms default and make it controllable for tests without adding new public config.
- [x] 1.6 Document the current queue-dispatch / recovery limitation that can leave submitted transactions pending, and keep this change from silently reclassifying that state as success.

## 2. Tests

- [x] 2.1 Add saga tests for a successful synchronous transaction through `SubmitAndWait`.
- [x] 2.2 Add saga tests for a successful asynchronous transaction through `SubmitAndWait`, including polling completion.
- [x] 2.3 Add saga tests for terminal failure and compensation through `SubmitAndWait`, asserting the method waits through `compensating` until final `failed`.
- [x] 2.4 Add saga tests for caller `ctx` cancellation or timeout before terminal state, asserting the method returns early without changing background transaction execution semantics.
- [x] 2.5 Add saga tests covering `WithTimeout` plus `SubmitAndWait`, asserting the method waits through `compensating` until final `failed` when caller `ctx` remains valid.
- [x] 2.6 Add saga tests covering temporary `Query` failures during waiting and eventual successful completion.
- [x] 2.7 Add saga tests covering persistent `Query` failures and asserting the method returns infrastructure errors with the latest known status when available.
- [x] 2.7a Add saga tests covering direct `Query` calls on missing transactions and asserting the method returns `ErrTransactionNotFound`.
- [x] 2.8 Add saga tests covering a transaction that disappears after successful submit and asserting the method returns `ErrTransactionDisappeared`.
- [x] 2.9 Add saga tests covering no active executor instances, asserting the method waits until caller `ctx` ends instead of fabricating completion.
- [x] 2.10 Review existing saga tests that hand-roll `Submit + Query` wait loops, and only migrate tests whose sole purpose is final-result waiting without intermediate assertions; keep more tightly coupled wait-loop tests in place or move any broader cleanup to a separate follow-up PR.

## 3. Documentation

- [x] 3.1 Update `AGENTS.md` to document the new exported saga engine API, `ErrTransactionFailed`, `ErrTransactionNotFound`, `ErrTransactionDisappeared`, `SubmitAndWait` concurrent-safe usage, the `ctx` versus `WithTimeout` distinction, and that sentinel errors may be wrapped so callers SHOULD use `errors.Is`.
- [x] 3.2 Update `docs/saga.md`, `docs/modules/saga.md`, and `saga/README.md` with `SubmitAndWait` usage, `Query` not-found semantics, return semantics, and when to choose it over `Submit`.
- [x] 3.3 Document that `SubmitAndWait` depends on at least one running engine instance connected to the same store to advance the transaction; do not describe it as requiring the current instance to be the executor.
- [x] 3.4 Document the current queue overflow / one-shot recovery edge case that can leave a submitted transaction pending until a later recovery opportunity or caller timeout, and distinguish this from the case where a running instance can still advance timed-out transactions via `timeoutScanner`.
- [x] 3.5 Review examples or inline comments that currently imply polling `Query` is the only way to wait for a final result, and align them with the new API where appropriate.
