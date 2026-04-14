## Context

`taskqueue/asynqbroker` 是 taskqueue 抽象层在 asynq 上的实现。当前 `ConsumerConfig` 暴露了 `Concurrency`、`Queues`、`StrictPriority`、`Logger`、`RuntimeLogger` 五个参数，但未暴露 asynq 的 `TaskCheckInterval`（空闲队列轮询间隔）。asynq 默认值为 1s。对于延迟敏感场景，用户需要更低延迟；对于低频批处理场景，用户可能希望降低轮询频率以减轻 Redis 压力。

## Goals / Non-Goals

**Goals:**
- 在 `ConsumerConfig` 中新增 `TaskCheckInterval` 字段，允许用户配置该值
- 强制约束合法范围：最小 200ms、最大 2s
- 零值（未设置）时保持 asynq 默认行为（1s），不改变现有用户体验
- 补充单元测试，覆盖边界值校验与传递逻辑
- 更新模块文档

**Non-Goals:**
- 不暴露 asynq 的 `DelayedTaskCheckInterval`（scheduled/retry 队列轮询间隔），后续按需再加
- 不修改 `taskqueue.Consumer` 接口层的抽象定义，本次改动仅涉及 asynqbroker 实现
- 不引入运行时动态修改该参数的能力

## Decisions

### 1. 字段位置：放在 `ConsumerConfig` 而非新建配置结构

**选择**: 直接在 `asynqbroker.ConsumerConfig` 增加 `TaskCheckInterval time.Duration` 字段

**理由**: `ConsumerConfig` 已经是 asynq consumer 的配置入口，新增字段保持一致性，零值兼容不破坏现有调用方。

**替代方案**: 新建 `PerformanceConfig` 子结构嵌套 —— 过度设计，仅一个字段不需要额外层级。

### 2. 校验策略：clamp 模式

**选择**: 在 `NewConsumer` 中执行 clamp（钳位），小于 200ms 则设为 200ms，大于 2s 则设为 2s，零值不设置（让 asynq 用默认值）。

**理由**:
- 与现有 `Concurrency` 的处理风格一致（`<= 0` 时设为默认值 2）
- 对调用方友好，不会因配置略微越界就启动失败
- clamp 比返回 error 更符合 Go 基础设施库的惯用风格

**替代方案**: 返回 error —— 对于这种有明确合理边界的配置，clamp 已足够，返回 error 增加调用方处理负担。

### 3. 边界值定义

| 边界 | 值 | 说明 |
|------|------|------|
| 最小值 | 200ms | 避免过于频繁的轮询对 Redis 造成压力 |
| 最大值 | 2s | 保证任务延迟在可接受范围内 |
| 默认值 | 0（不传） | 保持 asynq 内部默认 1s |

这三个值以导出常量形式定义在 `asynqbroker` 包中，方便调用方引用。

### 4. 测试方式

由于 `asynq.Server` 不暴露 `Config` 的读取接口，无法直接断言内部生效值。测试分两层：

- **单元测试**: 验证 `ConsumerConfig` 的校验/clamp 逻辑（提取为内部辅助函数）
- **集成测试**: 使用 miniredis，设置小 interval 后发布任务，确认 consumer 能正常处理（现有测试模式已覆盖）

## Risks / Trade-offs

- **[Risk] 用户设置过低 interval 增加 Redis 负载** → 通过 200ms 下限强制约束
- **[Risk] asynq 版本升级可能改变 TaskCheckInterval 语义** → 当前钉死 asynq v0.26.0，升级时需复审
- **[Trade-off] clamp 而非报错可能让用户不知道配置被修正** → 在运行时日志中输出 warn 级别提示
