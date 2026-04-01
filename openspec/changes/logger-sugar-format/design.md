## Context

当前 `logger` 包的 `Logger` 接口仅提供 slog 风格的结构化日志方法（key-value pair）。`zapLogger` 内部已持有 `*zap.Logger`，而 `zap` 本身提供了 `SugaredLogger`，支持 `printf` 风格 (`Debugf`) 和松散 key-value 风格 (`Debugw`)。需要在现有结构上以最小侵入的方式将这些能力暴露给调用方。

## Goals / Non-Goals

**Goals:**
- 新增 `SugaredLogger` 接口，覆盖 `f`（格式化）和 `w`（弱类型 key-value）两种调用风格
- `Logger` 接口增加 `Sugar() SugaredLogger` 入口方法
- 全局包级快捷函数（`logger.Infof / logger.Infow` 等）
- 分类 logger（Access / Platform / Business）透传 `Sugar()` 能力
- 无新增外部依赖

**Non-Goals:**
- 不修改或废弃现有 `Logger` 接口的任何方法
- 不支持 `%w` error wrapping（由调用方在 message 内处理）
- 不为 `SugaredLogger` 实现独立的 `With / WithGroup / WithContext`（直接复用底层 `zap.SugaredLogger.With`）

## Decisions

### 1. 独立 `SugaredLogger` 接口，而非扩展 `Logger` 接口

**方案对比：**
- A) 直接在 `Logger` 上新增 `Infof/Infow` — 破坏所有现有实现，需同步修改 mock、测试替身
- B) `Logger.Sugar() SugaredLogger` 返回独立接口 — **选择此方案**

**理由：** `Logger` 是项目中跨模块的公共契约，最小化接口膨胀。`Sugar()` 作为可选入口，不强制已有代码适配。

### 2. 底层直接委托 `zap.SugaredLogger`

`zapLogger.Sugar()` 调用 `l.zlogger.Sugar()`，将调用栈 skip 保持与现有方法一致（已配置 `AddCallerSkip(2)`）。`zap.SugaredLogger` 内置了格式化和 key-value 处理逻辑，无需重写。

### 3. caller skip 对齐

`zap.SugaredLogger` 内部默认 skip=1（跳过 sugar 包装层）。由于 `zlogger` 已设置 `AddCallerSkip(2)`，`zapSugaredLogger` 的方法本身不额外 skip，使最终 caller 指向业务代码，与现有行为一致。

### 4. 包级全局快捷函数

与现有 `Info / Error` 等并列，新增 `Infof / Infow / Errorf / Errorw` 等，委托给 `GetLogger().Sugar()`，保持调用习惯一致性。

## Risks / Trade-offs

- **格式化字符串注入风险** → `Infof` 直接调用 `zap.SugaredLogger.Infof`，`zap` 内部使用 `fmt.Sprintf`，无额外风险；调用方需注意避免将用户输入作为 format 字符串（与 `fmt.Sprintf` 一样的使用规范）。
- **caller 行号偏移** → 若将来修改 `zapLogger` 的 caller skip 设置，需同步验证 sugar 方法的 caller 行号是否仍正确。使用集成测试场景覆盖。
- **接口膨胀** → `SugaredLogger` 接口约 10 个方法，通过单独接口隔离不影响主 `Logger`。
