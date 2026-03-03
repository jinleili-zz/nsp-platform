# Code Review: Lock SDK

**Commits:** `8bfee37` · `4be49d4`
**Date:** 2026-03-03
**Reviewer:** Claude Code

---

## Overview

These two commits introduce a distributed lock SDK (`nsp-common/pkg/lock`) backed by
redsync + Redis, with a clean interface/implementation separation, 14 unit tests, and a
demo program. The overall design is solid, but there are several issues worth addressing.

---

## Design & Architecture

**Strengths:**

- Clean interface separation (`Client`/`Lock`) — business code never imports redsync/go-redis types.
- Good use of functional options with sensible defaults.
- The comment explaining why `ClusterClient` is used instead of Redlock multi-node is
  excellent and prevents future confusion.
- Watchdog goroutine lifecycle is correct — `Stop()` waits on `<-w.done`, ensuring the
  goroutine exits before `Release` returns.

---

## Issues

### High

**1. Missing `Close()` on `Client` interface (resource leak)**

Neither `Client` nor `redisClient` exposes a way to close the underlying connection pool.
This will leak goroutines and connections when the client is no longer needed (e.g., test
teardown, graceful shutdown).

```go
// Suggested addition to Client interface:
Close() error
```

**2. `TryAcquire` does not start the watchdog (behavioral inconsistency)**

`Acquire` starts the watchdog when `EnableWatchdog` is true, but `TryAcquire` does not.
If a caller uses `TryAcquire` with `WithWatchdog()`, the lock will silently expire without
renewal — a subtle footgun.

---

### Medium

**3. `NewRedisClient` connectivity check has no timeout**

```go
// redis.go
if _, err := clusterClient.ClusterInfo(context.Background()).Result(); err != nil {
```

`context.Background()` has no deadline. If the cluster is slow to respond, the constructor
blocks forever. Should use `context.WithTimeout(context.Background(), opt.DialTimeout)`.

**4. Multiple `Acquire` calls leak the watchdog goroutine**

If a caller calls `Acquire` twice without `Release` in between (misuse, but plausible in
error-recovery paths), `l.watchdog` is overwritten without stopping the previous goroutine:

```go
// Acquire sets l.watchdog = newWatchdog(...) unconditionally.
// No check for an existing running watchdog.
```

**5. `watchdog` field not protected by `muMu` in `Release`**

```go
// Release
if l.watchdog != nil {    // ← not under muMu
    l.watchdog.Stop()
    l.watchdog = nil      // ← not under muMu
}
```

`l.watchdog` is written by `Acquire` and read/nulled by `Release`. If these are called
concurrently from different goroutines, this is a data race. Guard with `muMu` or a
dedicated mutex.

**6. `log.Printf` in watchdog is inconsistent with the project logger**

```go
// redis.go — watchdog goroutine
log.Printf("lock watchdog: renew failed: %v", err)
```

The project uses the zap-based `nsp-common/pkg/logger`. Either accept a logger via
`newWatchdog(interval, renewFn, logger)` or use the global `logger.Warn`. Using stdlib
`log` means watchdog errors are invisible in structured log pipelines.

---

### Low

**7. `NewStandaloneRedisClient` stores `client: nil`**

```go
return &redisClient{rs: rs, client: nil}, nil
```

The underlying `*redis.Client` is not retained anywhere in the struct, so the standalone
connection pool cannot be closed (compounds issue #1). Having the same struct type
represent two different configurations is also confusing. Consider a separate
`standaloneRedisClient` struct, or store the plain client as an `io.Closer` field.

**8. Placeholder import path in demo**

```go
// nsp-demo/cmd/lock-demo/main.go
import "github.com/yourorg/nsp-common/pkg/lock"
```

`yourorg` is a placeholder. Verify this matches the actual `go.mod` module path before
landing.

**9. `RetryDelay` struct field comment omits jitter**

`LockOption.RetryDelay` says "base wait time between Acquire retries" but does not mention
the `[0, RetryDelay/2)` jitter. The `WithRetryDelay` godoc does. The struct field comment
is what most IDEs show on hover — keep them consistent.

---

## Tests

**Strengths:**

- Good coverage across the happy path, context cancellation/timeout, TTL expiry, watchdog,
  and mutual exclusion.
- Using `miniredis.FastForward` to simulate TTL expiry is exactly the right approach —
  avoids flaky real-sleep tests.
- `TestMutualExclusion` intentionally uses a non-atomic read-modify-write (load → sleep →
  store) to prove the lock works under contention. The design is correct.

**Minor issues:**

**10. `TestWithWatchdog` sleeps 2.5 s in real time — slow and potentially flaky**

Consider a shorter TTL (e.g., 200 ms with a watchdog interval of ~66 ms) and a real sleep
of ~500 ms to keep CI fast while still exercising multiple renewal cycles.

**11. `newTestClient` explicitly discards `mr`**

```go
func newTestClient(t *testing.T) Client {
    mr, client := getMiniRedis(t)
    _ = mr
    return client
}
```

Minor nit — consider a brief comment or a simpler internal helper that does not return `mr`
for the common case.

---

## Summary Table

| Severity | Issue |
|---|---|
| High | No `Close()` on `Client` — connection/goroutine leak |
| High | `TryAcquire` does not start watchdog when `EnableWatchdog` is true |
| Medium | `NewRedisClient` connectivity check uses `context.Background()` (no timeout) |
| Medium | Multiple `Acquire` calls leak the watchdog goroutine |
| Medium | `watchdog` field unprotected by `muMu` in `Release` — data race |
| Medium | `log.Printf` in watchdog — inconsistent with project logger |
| Low | `StandaloneRedisClient` stores `client: nil` — confusing + amplifies leak |
| Low | Placeholder `yourorg` import path in demo |
| Low | `RetryDelay` struct comment omits jitter |
| Low | `TestWithWatchdog` sleeps 2.5 s real time |
