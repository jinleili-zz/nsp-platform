# Code Review: Logger Multi-Category Support

**Commits:** `c4becd8` → `9b59ea0`
**Path:** `nsp-common/pkg/logger/`
**Date:** 2026-03-03
**Reviewer:** Claude Code

---

## Overview

This commit adds multi-category logging (`access` / `platform` / `business`) to the NSP
logger package. Each category can be independently configured with its own level, format,
and output destinations. New additions:

- `category.go` — `categoryManager`, `InitMultiCategory`, category accessor functions
  (`Access()`, `Platform()`, `Business()`, `Biz()`, `ForCategory()`), preset constructors,
  and `SyncAll()`
- `access_middleware.go` — `AccessLogEntry`, `LogAccess()`, `AccessLogConfig` with skip-path
  and slow-request support
- `fields.go` — new field constants for HTTP access and platform logs, plus `FieldCategory`
- `category_test.go` — 9 test functions covering init, fallback, file output, level
  routing, and path skipping

The overall design is clean and the fallback-to-global-logger behaviour is a good
compatibility story. A few correctness bugs and several code quality issues need addressing.

---

## Issues

### High

**1. `LogAccess` ignores the `context.Context` parameter**

`LogAccess` accepts a `ctx` but calls `accessLogger.Info/Warn/Error` (not the `Context`
variants), so trace/span IDs present in the context are silently dropped unless the caller
also copies them into `entry.TraceID`/`entry.SpanID` manually.

```go
// access_middleware.go:96-103 — bug
switch {
case entry.Status >= 500:
    accessLogger.Error(msg, args...)   // ctx ignored
case entry.Status >= 400:
    accessLogger.Warn(msg, args...)    // ctx ignored
default:
    accessLogger.Info(msg, args...)    // ctx ignored
}
```

Fix: use the `Context` variants and remove the redundant `TraceID`/`SpanID` fields from
`AccessLogEntry` (or keep them as overrides but prefer context first):

```go
switch {
case entry.Status >= 500:
    accessLogger.ErrorContext(ctx, msg, args...)
case entry.Status >= 400:
    accessLogger.WarnContext(ctx, msg, args...)
default:
    accessLogger.InfoContext(ctx, msg, args...)
}
```

The test `TestLogAccess` passes a context with a trace ID but also copies it manually into
`entry.TraceID`, which masks the bug.

---

**2. `catInitialized` bool is dead code**

`catInitialized` is written in `InitMultiCategory` and reset in `resetCategoryManager`, but
never read anywhere. All callers check `catManager != nil`. This adds confusion about the
intended source of truth.

```go
// category.go:252-253
catManager    *categoryManager
catManagerMu  sync.RWMutex
catInitialized bool   // never read — remove
```

---

### Medium

**3. `SyncAll` silently discards all but the first sync error**

When multiple category loggers fail to sync, only the first error is returned. Use
`errors.Join` (available since Go 1.20) to surface all errors:

```go
// current
if len(errs) > 0 {
    return errs[0]
}

// fix
return errors.Join(errs...)
```

---

**4. Global logger set by `InitMultiCategory` lacks the `log_category` field**

After `InitMultiCategory`, `GetLogger()` returns `businessLogger` (the raw zap logger),
while `Business()` returns `manager.business` which has `.With(FieldCategory, "business")`
applied. A caller who mixes the two APIs gets inconsistent output:

```go
// category.go:330-333
globalMu.Lock()
globalLogger = businessLogger          // no category field
initialized = true
globalMu.Unlock()
```

Fix: set `globalLogger = manager.business` (the one with the `log_category` field already
attached).

---

**5. `"query"` raw string literal in `LogAccess`**

All other fields in `LogAccess` use typed constants, but the query field uses a bare string:

```go
// access_middleware.go:68
args = append(args, "query", entry.Query)   // inconsistent
```

Add `FieldHTTPQuery = "http_query"` to `fields.go` and use it here.

---

**6. `filepath.Join` not used in `FileMultiCategoryConfig`**

String concatenation with `"/"` is not cross-platform:

```go
// category.go — multiple occurrences
Path: logDir + "/access.log",
```

Fix: `filepath.Join(logDir, "access.log")` (requires `import "path/filepath"`).

---

### Low

**7. Redundant caller/stack-trace default logic for Access and Platform categories**

In `buildCategoryLoggerConfig`, the access and platform branches have:

```go
if catCfg == nil || !catCfg.EnableCaller {
    result.EnableCaller = false          // already false — zero value
}
```

Since `result` is a freshly allocated `&Config{}`, `EnableCaller` and `EnableStackTrace`
are already `false`. The condition `!catCfg.EnableCaller` can only be true when
`catCfg.EnableCaller` is already `false`, making the assignment a no-op. The block adds
noise without effect. Remove it — the only logic needed is the `catCfg != nil` branch that
copies values from `catCfg`, which is already handled above.

---

**8. `AccessLogConfig` is defined but disconnected from `LogAccess`**

`AccessLogConfig` (with `SkipPaths`, `SlowRequestThreshold`, `IncludeQuery`, etc.) is a
fully documented struct, but `LogAccess` ignores it entirely. This creates a confusing API:
users who read `DefaultAccessLogConfig()` will assume it affects `LogAccess`, but it doesn't.

Either:
- Add a `WithConfig(*AccessLogConfig) func(context.Context, *AccessLogEntry)` constructor
  that returns a bound `LogAccess`-like function, or
- Document explicitly on `AccessLogConfig` that it is intended for use by framework-specific
  middleware adapters (not yet implemented) and is not consumed by `LogAccess`.

---

## Test Coverage

- The `TestLogAccess` test masks bug #1: it populates both `ctx` with trace ID *and*
  `entry.TraceID` manually. Once the bug is fixed, the test should be updated to rely solely
  on context propagation.
- There is no test for the `SyncAll` error path.
- There is no test verifying that `GetLogger()` returns the same logger instance as
  `Business()` after `InitMultiCategory` (which would have caught issue #4).

---

## Strengths

- Clean fallback to global logger when `InitMultiCategory` hasn't been called preserves
  backwards compatibility without any breaking change.
- Three preset constructors (`Default`, `File`, `Development`) cover the most common
  deployment patterns with good defaults.
- `buildCategoryLoggerConfig` centralises defaults per category, keeping `InitMultiCategory`
  clean.
- `SyncAll` degrades gracefully to `Sync()` when the category manager is absent.
- `LogAccess` routing log level by HTTP status code (info/warn/error) is a practical
  convention that most frameworks follow.
- Test parallelism-safety via `resetGlobalLogger()` / `resetCategoryManager()` in each test
  is correctly applied.
