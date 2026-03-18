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
import "github.com/paic/nsp-common/pkg/lock"
```

`paic` is a placeholder. Verify this matches the actual `go.mod` module path before
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
| Low | Placeholder `paic` import path in demo |
| Low | `RetryDelay` struct comment omits jitter |
| Low | `TestWithWatchdog` sleeps 2.5 s real time |

---

## Review Response (逐条分析)

### #1 Missing `Close()` on `Client` — 确认，需要修

`redisClient` 持有 `*redis.ClusterClient`（或 standalone 的 `*redis.Client`），服务优雅关闭时
没有途径关闭连接池，goroutine 和 TCP 连接会泄漏。

**修改方案：**
- `Client` 接口新增 `Close() error`
- `redisClient` 新增 `closer io.Closer` 字段，在 `NewRedisClient` 和 `NewStandaloneRedisClient` 中赋值
- `Close()` 实现调用 `l.closer.Close()`
- 同时解决 #7（Standalone 的 client 也能正确关闭）

---

### #2 `TryAcquire` 不启动 watchdog — 确认，需要修

`Acquire` 成功后启动 watchdog，`TryAcquire` 成功后没有。
如果调用方传了 `WithWatchdog()` 并用 `TryAcquire`，锁会静默过期。

**修改方案：**
把启动 watchdog 的逻辑提取为 `startWatchdog()` 私有方法，`Acquire` 和 `TryAcquire` 成功后
都调用它。

---

### #3 connectivity check 无超时 — 确认，需要修

`NewRedisClient` 的 `ClusterInfo` 和 `NewStandaloneRedisClient` 的 `Ping` 都用
`context.Background()`，集群慢响应时会无限阻塞。

**修改方案：**
使用 `context.WithTimeout(context.Background(), opt.DialTimeout)` 作为校验的 context。

---

### #4 重复 Acquire 泄漏 watchdog goroutine — 确认，需要修

虽然重复 Acquire 属于误用，但防御性编程应该处理：旧 watchdog 没被 Stop 就被覆盖。

**修改方案：**
在 `startWatchdog()` 里先检查 `l.watchdog != nil`，如果已有则先 `Stop()`。

---

### #5 `watchdog` 字段未受 `muMu` 保护 — 确认，需要修

`Acquire` 写 `l.watchdog`，`Release` 读+清 `l.watchdog`，两处都没加锁。虽然正常使用
不会并发，但 `muMu` 本身就在保护 `mu` 字段的并发访问，把 `watchdog` 也纳入保护范围
成本很低。

**修改方案：**
将 `watchdog` 的读写统一放在 `muMu.Lock()` 保护范围内。

---

### #6 `log.Printf` 与项目 logger 不一致 — 确认，需要修

项目用 zap (`nsp-common/pkg/logger`)，watchdog 用 stdlib `log`，续约失败的日志
不会出现在结构化日志管道中。

**修改方案：**
`newWatchdog` 新增 `logFn func(format string, args ...any)` 参数。
`redisLock` 在创建 watchdog 时传入 `logger.Warn` 或类似的函数。
接口层不依赖具体 logger 实现，由调用方注入。

---

### #7 `NewStandaloneRedisClient` stores `client: nil` — 确认，随 #1 一并解决

引入 `closer io.Closer` 字段后，Standalone 的 `redis.Client` 也会被正确存储并可关闭。

---

### #8 Placeholder `paic` import path — 不需要修

这是项目级的约定，`go.mod` 中通过 `replace` 指令解决，不是 lock 模块的问题。
整个项目（nsp-common、nsp-demo）统一使用 `paic` 占位符，后续统一替换即可。

---

### #9 `RetryDelay` struct field comment omits jitter — 不成立

Review 说 struct field 注释没提 jitter，但实际代码（`lock.go:91-94`）已经写了：

```go
// RetryDelay is the base wait time between Acquire retries.
//
// Actual wait = RetryDelay + random jitter in [0, RetryDelay/2).
// Jitter prevents thundering-herd when multiple nodes retry simultaneously.
```

与 `WithRetryDelay` 的 godoc 内容一致。这条 review 有误，不需要修。

---

### #10 `TestWithWatchdog` sleeps 2.5s — 有道理，但改进空间有限

watchdog 有 `max(TTL/3, 1s)` 的最小间隔限制，TTL=1s 时 interval 被 clamp 到 1s。
如果缩短 TTL 到 500ms 以下，watchdog 第一次 tick (1s) 时锁已过期，测试会失败。

当前 TTL=1s + sleep 2.5s 是能覆盖多次续约的最短配置。可以把 sleep 从 2500ms 缩到
1500ms（仍覆盖 >1 次续约），总耗时节省约 1s，但改进有限。

**修改方案：** 保持不变，不值得为 ~1s 引入风险。

---

### #11 `newTestClient` discards `mr` — 不需要修

纯风格问题，`_ = mr` 表意清晰，不影响功能。

---

## 修改汇总

| # | 问题 | 处置 | 修改说明 |
|---|------|------|---------|
| 1 | Client 缺少 Close() | **已修复** | `Client` 接口新增 `Close() error`；`redisClient` 用 `closer io.Closer` 字段持有底层连接，`Close()` 调用 `closer.Close()` |
| 2 | TryAcquire 不启动 watchdog | **已修复** | 提取 `startWatchdog()` 私有方法，`Acquire` 和 `TryAcquire` 成功后统一调用 |
| 3 | connectivity check 无超时 | **已修复** | `NewRedisClient` 和 `NewStandaloneRedisClient` 均使用 `context.WithTimeout(ctx, opt.DialTimeout)` 做连通性校验；校验失败时调用 `client.Close()` 释放资源 |
| 4 | 重复 Acquire 泄漏 watchdog | **已修复** | `startWatchdog()` 内先检查 `l.wd != nil`，已有则先 `Stop()` 再启动新 goroutine |
| 5 | watchdog 字段无锁保护 | **已修复** | `wd` 字段的读写统一放在 `muMu.Lock()` 保护范围内（`Acquire`/`TryAcquire`/`Release` 三处） |
| 6 | log.Printf 与 logger 不一致 | **已修复** | `newWatchdog` 新增 `logFn func(string, ...any)` 参数；`redisLock.startWatchdog()` 传入 `logger.Warn` 的封装函数 |
| 7 | Standalone client: nil | **已修复** | 随 #1 一并解决，`NewStandaloneRedisClient` 将 `redis.Client` 存入 `closer` 字段 |
| 8 | paic 占位符 | **不修** | 项目级约定，`go.mod` 通过 `replace` 指令解决，所有模块统一使用 `paic` 占位符 |
| 9 | RetryDelay 注释缺 jitter | **不修** | review 有误——`LockOption.RetryDelay` 的 struct 注释（`lock.go:91-94`）已包含 jitter 说明，与 `WithRetryDelay` godoc 一致 |
| 10 | 测试 sleep 2.5s | **不修** | watchdog 有 `max(TTL/3, 1s)` 最小间隔限制，当前 TTL=1s + sleep 2.5s 是覆盖多次续约的最短可行配置，缩短空间有限且会引入 flaky 风险 |
| 11 | newTestClient 弃 mr | **不修** | 纯风格问题，`_ = mr` 表意清晰，不影响功能 |

### 修改涉及文件

| 文件 | 改动点 |
|------|--------|
| `pkg/lock/lock.go` | `Client` 接口新增 `Close() error`；usage example 补充 `defer client.Close()` |
| `pkg/lock/redis.go` | `redisClient` 字段 `client` → `closer io.Closer`；新增 `Close()` 方法；提取 `startWatchdog()`；`TryAcquire` 成功后启动 watchdog；`Release` 中 `wd` 读写加 `muMu` 保护；连通性校验加 timeout 并失败时 close；`newWatchdog` 新增 `logFn` 参数替代 stdlib `log`；引入 `logger.Warn` |
| `pkg/lock/lock_test.go` | `redisClient` 字段名适配 `client` → `closer` |
| `nsp-demo/cmd/lock-demo/main.go` | 补充 `defer client.Close()` |

### 验证结果

- 单元测试：14/14 通过 (`go test ./pkg/lock/... -v -count=1`)
- Demo：5 个场景全部通过 (Docker Redis `redis:7-alpine` on port 6380)
