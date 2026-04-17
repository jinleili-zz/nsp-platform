# Saga 模块

> 包路径：`github.com/jinleili-zz/nsp-platform/saga`

## 功能说明

Saga 模块解决以下问题：

- **分布式事务**：跨服务的长事务拆分为多个本地事务，通过补偿保证最终一致性
- **同步/异步步骤**：支持同步 HTTP 调用和异步轮询两种步骤类型
- **自动补偿**：步骤失败时自动按逆序执行已完成步骤的补偿操作
- **故障恢复**：引擎重启后自动恢复未完成的事务
- **超时处理**：支持事务级别超时，超时后自动触发补偿

---

## 核心概念

### 事务状态

```mermaid
stateDiagram-v2
    [*] --> pending: Submit
    pending --> running: 开始执行
    running --> succeeded: 所有步骤成功
    running --> compensating: 步骤失败
    compensating --> failed: 补偿完成
    succeeded --> [*]
    failed --> [*]
```

| 状态 | 说明 |
|------|------|
| `pending` | 事务已创建，等待执行 |
| `running` | 事务正在执行步骤 |
| `compensating` | 步骤失败，正在执行补偿 |
| `succeeded` | 所有步骤执行成功 |
| `failed` | 补偿完成，事务失败 |

### 步骤状态

| 状态 | 说明 |
|------|------|
| `pending` | 步骤未开始 |
| `running` | 步骤正在执行 |
| `polling` | 异步步骤等待轮询结果 |
| `succeeded` | 步骤执行成功 |
| `failed` | 步骤执行失败 |
| `compensating` | 步骤正在补偿 |
| `compensated` | 步骤补偿完成 |
| `skipped` | 步骤被跳过 |

### 步骤类型

| 类型 | 说明 |
|------|------|
| `StepTypeSync` | 同步步骤，HTTP 调用立即返回结果 |
| `StepTypeAsync` | 异步步骤，HTTP 调用受理后需轮询确认最终状态 |

---

## 核心接口

```go
// Engine 引擎入口
type Engine struct { /* ... */ }

func NewEngine(cfg *Config) (*Engine, error)
func (e *Engine) Start(ctx context.Context) error
func (e *Engine) Stop() error
func (e *Engine) Submit(ctx context.Context, def *SagaDefinition) (string, error)
func (e *Engine) SubmitAndWait(ctx context.Context, def *SagaDefinition) (string, *TransactionStatus, error)
func (e *Engine) Query(ctx context.Context, txID string) (*TransactionStatus, error)

var ErrTransactionFailed error
var ErrTransactionNotFound error
var ErrTransactionDisappeared error

// SagaBuilder 构建器
func NewSaga(name string) *SagaBuilder
func (b *SagaBuilder) AddStep(step Step) *SagaBuilder
func (b *SagaBuilder) WithTimeout(seconds int) *SagaBuilder
func (b *SagaBuilder) WithPayload(payload map[string]any) *SagaBuilder
func (b *SagaBuilder) Build() (*SagaDefinition, error)
```

---

## 配置项

### Config

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `DSN` | `string` | **必填** | PostgreSQL 连接串 |
| `WorkerCount` | `int` | `4` | 协调器并发 Worker 数 |
| `PollBatchSize` | `int` | `20` | 轮询器每次扫描任务数 |
| `PollScanInterval` | `time.Duration` | `3s` | 轮询器扫描间隔 |
| `CoordScanInterval` | `time.Duration` | `5s` | 协调器扫描间隔 |
| `HTTPTimeout` | `time.Duration` | `30s` | 单次 HTTP 调用超时时间；若事务 deadline 更早，前向步骤会先受事务级超时约束 |
| `HTTPClient` | `*http.Client` | `nil` | 可选，自定义出站 HTTP client；非 nil 时忽略 `HTTPTimeout`，但事务级超时仍会通过 request context 生效 |
| `InstanceID` | `string` | 自动生成 | 实例唯一标识 |
| `CredentialStore` | `auth.CredentialStore` | `nil` | 可选，用于步骤出站 AK/SK 签名 |
| `Logger` | `logger.Logger` | `logger.Platform()` | 可选，SAGA 运行时日志出口；未显式注入时默认走 platform logger，并在后台路径优先重建事务 trace 上下文 |

### Step 配置

| 字段名 | 类型 | 说明 |
|--------|------|------|
| `Name` | `string` | 步骤名称（日志用） |
| `Type` | `StepType` | `sync` 或 `async` |
| `ActionMethod` | `string` | 正向操作 HTTP 方法 |
| `ActionURL` | `string` | 正向操作 URL（支持模板） |
| `ActionPayload` | `map[string]any` | 正向操作请求体（支持模板） |
| `CompensateMethod` | `string` | 补偿操作 HTTP 方法 |
| `CompensateURL` | `string` | 补偿操作 URL（支持模板） |
| `CompensatePayload` | `map[string]any` | 补偿操作请求体（支持模板） |
| `MaxRetry` | `int` | 最大重试次数（默认 3） |

### 异步步骤额外配置

| 字段名 | 类型 | 说明 |
|--------|------|------|
| `PollURL` | `string` | 轮询 URL（支持模板） |
| `PollMethod` | `string` | 轮询 HTTP 方法（默认 GET） |
| `PollIntervalSec` | `int` | 轮询间隔秒数（默认 5） |
| `PollMaxTimes` | `int` | 最大轮询次数（默认 60） |
| `PollSuccessPath` | `string` | 成功判断的 JSONPath |
| `PollSuccessValue` | `string` | 成功判断的期望值 |
| `PollFailurePath` | `string` | 失败判断的 JSONPath |
| `PollFailureValue` | `string` | 失败判断的期望值 |

---

## 快速使用

### 可运行示例

仓库内提供了一个可直接运行的 saga 示例：

- `examples/saga-demo/main.go`：同一个 demo 中同时演示 `Submit` + `Query` 轮询，以及 `SubmitAndWait` 阻塞等待终态

运行方式：

```bash
SAGA_EXAMPLE_DSN="$SAGA_EXAMPLE_DSN" go run ./examples/saga-demo
```

说明：

- 示例会优先读取 `SAGA_EXAMPLE_DSN`，未设置时回退读取 `TEST_DSN`
- 示例启动时会自动尝试执行 `saga/migrations/saga.sql`
- 示例内部自带本地 HTTP demo 服务，不需要额外准备业务服务 URL

### 观测工具（第一期）

除了 SDK 查询接口外，仓库还提供了一个只读终端观测命令 `sagactl`，用于直接查看
SAGA 持久化状态，避免排障时手写 SQL。

基础用法：

```bash
sagactl [--dsn <dsn>] <list|failed|show|watch> [flags]
```

连接方式：
- 通过 `--dsn` 传入只读 PostgreSQL DSN
- 或设置环境变量 `SAGA_OBSERVER_DSN`

子命令：
- `list [--status <status>] [--limit N]`
  用途：查看事务列表，按创建时间倒序返回
  `--status` 可选值：`pending`、`running`、`compensating`、`succeeded`、`failed`
  `--limit` 默认值：`100`
- `failed [--limit N]`
  用途：查看最近失败事务，按 `COALESCE(finished_at, updated_at)` 倒序返回
  `--limit` 默认值：`100`
- `show <tx-id>`
  用途：查看单个事务详情，包括事务摘要、步骤状态和关联 poll task
- `watch <tx-id> [--interval 3s]`
  用途：循环刷新 `show` 视图，适合观察轮询和补偿阶段
  `--interval` 默认值：`3s`

帮助和错误处理：
- `sagactl list -h`、`sagactl watch -h` 等帮助命令不依赖数据库连接
- 未知子命令或参数错误时，会优先返回本地帮助或参数错误，而不是先尝试连接数据库

第一期边界：
- 只读，不会修改 `saga_*` 表
- 默认结果上限为 100
- `watch` 展示的是当前持久化快照自动刷新，不是完整事件历史
- `show` / `watch` 中的事务摘要和步骤详情来自同一个只读 SQL 快照，不会混合不同时间点的数据

示例：

```bash
go run ./cmd/sagactl --dsn "postgres://user:pass@localhost:5432/nsp?sslmode=disable" list --status compensating
go run ./cmd/sagactl --dsn "postgres://user:pass@localhost:5432/nsp?sslmode=disable" failed
go run ./cmd/sagactl --dsn "postgres://user:pass@localhost:5432/nsp?sslmode=disable" show <tx-id>
go run ./cmd/sagactl --dsn "postgres://user:pass@localhost:5432/nsp?sslmode=disable" watch --interval 1s <tx-id>
go run ./cmd/sagactl show -h
```

### 引擎初始化

```go
package main

import (
    "context"
    "log"
    "time"

    _ "github.com/lib/pq"
    "github.com/jinleili-zz/nsp-platform/saga"
)

func main() {
    ctx := context.Background()

    // 创建引擎
    engine, err := saga.NewEngine(&saga.Config{
        DSN:         "postgres://user:pass@localhost:5432/nsp?sslmode=disable",
        WorkerCount: 4,
        HTTPTimeout: 30 * time.Second,
    })
    if err != nil {
        log.Fatal(err)
    }

    // 启动后台任务（协调器 + 轮询器）
    if err := engine.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer engine.Stop()

    // 业务代码...
    select {}
}
```

### 注入自定义 HTTP Client

```go
customClient := &http.Client{
    Timeout: 10 * time.Second,
}

engine, err := saga.NewEngine(&saga.Config{
    DSN:        "postgres://user:pass@localhost:5432/nsp?sslmode=disable",
    HTTPClient: customClient,
})
```

当 `HTTPClient` 非 nil 时，SAGA 的 action、compensation 和 poll 请求都会复用该 client，`HTTPTimeout` 配置会被忽略。

### 同步步骤事务

```go
func createOrderTransaction(engine *saga.Engine, ctx context.Context) (string, error) {
    def, err := saga.NewSaga("order-checkout").
        AddStep(saga.Step{
            Name:             "扣减库存",
            Type:             saga.StepTypeSync,
            ActionMethod:     "POST",
            ActionURL:        "http://stock-service/api/v1/stock/deduct",
            ActionPayload:    map[string]any{"sku_id": "SKU-001", "count": 2},
            CompensateMethod: "POST",
            CompensateURL:    "http://stock-service/api/v1/stock/rollback",
            CompensatePayload: map[string]any{"sku_id": "SKU-001", "count": 2},
        }).
        AddStep(saga.Step{
            Name:             "创建订单",
            Type:             saga.StepTypeSync,
            ActionMethod:     "POST",
            ActionURL:        "http://order-service/api/v1/orders",
            ActionPayload:    map[string]any{"user_id": "U-001", "sku_id": "SKU-001"},
            CompensateMethod: "DELETE",
            // 使用模板变量：引用上一步的响应
            CompensateURL:    "http://order-service/api/v1/orders/{action_response.order_id}",
        }).
        AddStep(saga.Step{
            Name:             "扣款",
            Type:             saga.StepTypeSync,
            ActionMethod:     "POST",
            ActionURL:        "http://payment-service/api/v1/pay",
            ActionPayload:    map[string]any{
                "user_id":  "U-001",
                "order_id": "{step[1].action_response.order_id}",
                "amount":   199.99,
            },
            CompensateMethod: "POST",
            CompensateURL:    "http://payment-service/api/v1/refund",
            CompensatePayload: map[string]any{
                "order_id": "{step[1].action_response.order_id}",
            },
        }).
        WithTimeout(300).  // 5 分钟超时
        Build()

    if err != nil {
        return "", err
    }

    return engine.Submit(ctx, def)
}
```

### 异步步骤事务

```go
func deviceConfigTransaction(engine *saga.Engine, ctx context.Context) (string, error) {
    def, err := saga.NewSaga("device-config").
        AddStep(saga.Step{
            Name:             "下发配置",
            Type:             saga.StepTypeAsync,  // 异步步骤
            ActionMethod:     "POST",
            ActionURL:        "http://device-service/api/v1/config/apply",
            ActionPayload:    map[string]any{"device_id": "DEV-001", "config": "..."},
            CompensateMethod: "POST",
            CompensateURL:    "http://device-service/api/v1/config/rollback",
            CompensatePayload: map[string]any{"device_id": "DEV-001"},
            // 轮询配置
            PollURL:          "http://device-service/api/v1/config/status?task_id={action_response.task_id}",
            PollMethod:       "GET",
            PollIntervalSec:  10,   // 每 10 秒轮询一次
            PollMaxTimes:     30,   // 最多轮询 30 次（5 分钟）
            PollSuccessPath:  "$.status",
            PollSuccessValue: "success",
            PollFailurePath:  "$.status",
            PollFailureValue: "failed",
        }).
        Build()

    if err != nil {
        return "", err
    }

    return engine.Submit(ctx, def)
}
```

### 查询事务状态

```go
func queryTransaction(engine *saga.Engine, ctx context.Context, txID string) {
    status, err := engine.Query(ctx, txID)
    if errors.Is(err, saga.ErrTransactionNotFound) {
        logger.WarnContext(ctx, "事务不存在", "tx_id", txID)
        return
    }
    if err != nil {
        logger.ErrorContext(ctx, "查询事务失败", logger.FieldError, err)
        return
    }

    logger.InfoContext(ctx, "事务状态",
        "tx_id", status.ID,
        "status", status.Status,
        "current_step", status.CurrentStep,
    )

    for _, step := range status.Steps {
        logger.InfoContext(ctx, "步骤状态",
            "tx_id", status.ID,
            "step_index", step.Index,
            "step_name", step.Name,
            "step_status", step.Status,
        )
        if step.LastError != "" {
            logger.WarnContext(ctx, "步骤错误",
                "tx_id", status.ID,
                "step_index", step.Index,
                logger.FieldError, step.LastError,
            )
        }
    }
}
```

### 阻塞等待终态

```go
func submitAndWait(engine *saga.Engine, ctx context.Context, def *saga.SagaDefinition) error {
    txID, status, err := engine.SubmitAndWait(ctx, def)
    if errors.Is(err, saga.ErrTransactionFailed) {
        logger.WarnContext(ctx, "事务最终失败",
            "tx_id", txID,
            "status", status.Status,
            "last_error", status.LastError,
        )
        return err
    }
    if errors.Is(err, saga.ErrTransactionDisappeared) {
        logger.ErrorContext(ctx, "事务在等待期间消失",
            "tx_id", txID,
            logger.FieldError, err,
        )
        return err
    }
    if err != nil {
        return err
    }

    logger.InfoContext(ctx, "事务执行成功", "tx_id", txID, "status", status.Status)
    return nil
}
```

说明：

- `Submit` 适合“先提交、后观测”的异步接入方式，返回成功只表示事务已入库。
- `SubmitAndWait` 适合调用方需要当前请求直接拿到最终结果的场景。
- `Query` 在事务不存在时返回 `ErrTransactionNotFound`；调用方应使用 `errors.Is(err, saga.ErrTransactionNotFound)` 判断。
- `SubmitAndWait` 只控制当前调用的提交与等待生命周期；事务自身超时仍由 `WithTimeout` / `TimeoutSec` 决定。
- 当事务进入 `running` 后，同步步骤出站请求和异步步骤等待都会感知事务级超时；到达事务 deadline 时会优先在当前执行路径内切换到补偿流程。
- `ErrTransactionFailed`、`ErrTransactionNotFound` 与 `ErrTransactionDisappeared` 可能被包装，调用方应使用 `errors.Is` 判断。
- 当 `SubmitAndWait` 因上下文取消、查询基础设施错误或 `ErrTransactionDisappeared` 返回错误时，`status` 只保证是“最后一次已知状态”，可能为 `nil`；调用方在读取 `status.Status` 前应先判空。
- `SubmitAndWait` 可被多个 goroutine 并发安全调用。
- `SubmitAndWait` 依赖至少一个连接同一存储的运行中 engine 实例推进事务，不要求必须由当前实例执行。
- 当前实现下，如果事务已持久化但未被执行者接手，事务可能长时间停留在 `pending`；`timeoutScanner` 主要负责兜底处理未被活跃执行者接手、或实例崩溃后遗留的超时事务。

---

## 模板变量语法

| 语法 | 说明 | 示例 |
|------|------|------|
| `{action_response.field}` | 当前步骤的响应字段 | `{action_response.order_id}` |
| `{step[N].action_response.field}` | 指定步骤的响应字段 | `{step[0].action_response.task_id}` |
| `{transaction.payload.field}` | 全局 Payload 字段 | `{transaction.payload.user_id}` |

---

## 数据库表结构

```sql
-- 事务表
CREATE TABLE saga_transactions (
    id              VARCHAR(64)  PRIMARY KEY,
    status          VARCHAR(20)  NOT NULL,
    payload         JSONB,
    current_step    INT          NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    finished_at     TIMESTAMPTZ,
    timeout_at      TIMESTAMPTZ,
    retry_count     INT          NOT NULL DEFAULT 0,
    last_error      TEXT
);

-- 步骤表
CREATE TABLE saga_steps (
    id                  VARCHAR(64)  PRIMARY KEY,
    transaction_id      VARCHAR(64)  NOT NULL REFERENCES saga_transactions(id),
    step_index          INT          NOT NULL,
    name                VARCHAR(128) NOT NULL,
    step_type           VARCHAR(20)  NOT NULL,
    status              VARCHAR(20)  NOT NULL,
    action_method       VARCHAR(10)  NOT NULL,
    action_url          TEXT         NOT NULL,
    action_payload      JSONB,
    action_response     JSONB,
    compensate_method   VARCHAR(10)  NOT NULL,
    compensate_url      TEXT         NOT NULL,
    compensate_payload  JSONB,
    poll_url            TEXT,
    poll_interval_sec   INT          DEFAULT 5,
    poll_max_times      INT          DEFAULT 60,
    poll_count          INT          NOT NULL DEFAULT 0,
    poll_success_path   TEXT,
    poll_success_value  TEXT,
    poll_failure_path   TEXT,
    poll_failure_value  TEXT,
    retry_count         INT          NOT NULL DEFAULT 0,
    max_retry           INT          NOT NULL DEFAULT 3,
    last_error          TEXT,
    UNIQUE (transaction_id, step_index)
);

-- 轮询任务表
CREATE TABLE saga_poll_tasks (
    id              BIGSERIAL    PRIMARY KEY,
    step_id         VARCHAR(64)  NOT NULL REFERENCES saga_steps(id),
    transaction_id  VARCHAR(64)  NOT NULL,
    next_poll_at    TIMESTAMPTZ  NOT NULL,
    locked_until    TIMESTAMPTZ,
    locked_by       VARCHAR(64),
    UNIQUE (step_id)
);
```

---

## 注意事项

### 补偿设计原则

- **幂等性**：补偿操作必须幂等，可能被多次调用
- **可重入**：补偿失败后会重试，设计时考虑部分补偿的情况
- **最终一致**：补偿完成后系统达到一致状态

### 性能提示

- 步骤数量建议控制在 10 个以内
- 异步步骤轮询间隔不宜过短（建议 >= 5s）
- 事务超时时间根据业务实际耗时设置；当前实现会让前向步骤请求与异步等待直接感知事务 deadline

---

## 错误类型

```go
var (
    ErrNoSteps                    = errors.New("saga must have at least one step")
    ErrAsyncStepMissingPollConfig = errors.New("async step must have PollURL, PollSuccessPath, and PollSuccessValue")
    ErrStepMissingAction          = errors.New("step must have ActionMethod and ActionURL")
    ErrStepMissingCompensate      = errors.New("step must have CompensateMethod and CompensateURL")
)
```
