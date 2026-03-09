# 背景

我在开发 NSP 项目，这是一个基于 Go 的微服务平台，包含 4 个业务服务。
我需要在公共基础库（nsp-common）中封装一个 SAGA 分布式事务模块，
以 SDK 形式嵌入业务进程，不作为独立服务部署。

---

## 技术选型（已确定）

- 运行模式：嵌入业务进程，后台 goroutine 驱动
- 事务模式：SAGA + 基于补偿的回滚
- 持久化：PostgreSQL（使用 database/sql + lib/pq 驱动）
- HTTP 调用：标准库 net/http
- Go 版本：>= 1.25
- 并发安全：数据库行锁（FOR UPDATE SKIP LOCKED）保证多实例安全

---

## 目录结构

nsp-common/
└── pkg/
    └── saga/
        ├── engine.go        # 引擎入口，对外暴露 API
        ├── definition.go    # Saga / Step 定义结构体
        ├── coordinator.go   # 协调器：状态机驱动
        ├── executor.go      # 执行器：HTTP 调用
        ├── poller.go        # 轮询器：异步步骤查询
        ├── store.go         # 数据库操作（PostgreSQL）
        ├── template.go      # 模板变量渲染
        ├── jsonpath.go      # JSONPath 解析（轮询结果判断）
        └── saga_test.go     # 集成测试

---

## 数据库表结构

### 表一：saga_transactions（全局事务表）

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

status 合法值：
  pending / running / compensating / succeeded / failed

CREATE INDEX idx_saga_tx_status ON saga_transactions(status);
CREATE INDEX idx_saga_tx_timeout ON saga_transactions(timeout_at)
    WHERE status IN ('running', 'compensating');

### 表二：saga_steps（子事务步骤表）

CREATE TABLE saga_steps (
    id                  VARCHAR(64)  PRIMARY KEY,
    transaction_id      VARCHAR(64)  NOT NULL REFERENCES saga_transactions(id),
    step_index          INT          NOT NULL,
    name                VARCHAR(128) NOT NULL,
    step_type           VARCHAR(20)  NOT NULL,       -- sync / async
    status              VARCHAR(20)  NOT NULL,

    action_method       VARCHAR(10)  NOT NULL,
    action_url          TEXT         NOT NULL,
    action_payload      JSONB,
    action_response     JSONB,

    compensate_method   VARCHAR(10)  NOT NULL,
    compensate_url      TEXT         NOT NULL,
    compensate_payload  JSONB,

    poll_url            TEXT,
    poll_method         VARCHAR(10)  DEFAULT 'GET',
    poll_interval_sec   INT          DEFAULT 5,
    poll_max_times      INT          DEFAULT 60,
    poll_count          INT          NOT NULL DEFAULT 0,
    poll_success_path   TEXT,
    poll_success_value  TEXT,
    poll_failure_path   TEXT,
    poll_failure_value  TEXT,
    next_poll_at        TIMESTAMPTZ,

    retry_count         INT          NOT NULL DEFAULT 0,
    max_retry           INT          NOT NULL DEFAULT 3,
    last_error          TEXT,
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,

    UNIQUE (transaction_id, step_index)
);

status 合法值：
  pending / running / polling / succeeded /
  failed / compensating / compensated / skipped

CREATE INDEX idx_saga_steps_tx    ON saga_steps(transaction_id, step_index);
CREATE INDEX idx_saga_steps_poll  ON saga_steps(next_poll_at)
    WHERE status = 'polling';

### 表三：saga_poll_tasks（轮询任务表）

CREATE TABLE saga_poll_tasks (
    id              BIGSERIAL    PRIMARY KEY,
    step_id         VARCHAR(64)  NOT NULL REFERENCES saga_steps(id),
    transaction_id  VARCHAR(64)  NOT NULL,
    next_poll_at    TIMESTAMPTZ  NOT NULL,
    locked_until    TIMESTAMPTZ,
    locked_by       VARCHAR(64),
    UNIQUE (step_id)
);

CREATE INDEX idx_poll_tasks_next ON saga_poll_tasks(next_poll_at)
    WHERE locked_until IS NULL OR locked_until < NOW();

---

## 核心需求

### 1. 定义层（definition.go）

#### StepType 枚举
const (
    StepTypeSync  StepType = "sync"
    StepTypeAsync StepType = "async"
)

#### StepStatus / TxStatus 枚举
定义所有合法的状态值常量，与数据库中的字符串一一对应。

#### Step 结构体
字段与 saga_steps 表的列一一对应，额外包含：
- Type           StepType
- MaxRetry       int
所有轮询相关字段仅在 Type == StepTypeAsync 时有效。

#### SagaDefinition 结构体
- ID          string           // 全局事务 ID，由引擎生成（UUID）
- Name        string           // 事务名称，仅用于日志
- Steps       []Step           // 有序步骤列表
- TimeoutSec  int              // 事务整体超时秒数，0 表示不限

#### Builder 模式
提供 NewSaga(name string) *SagaBuilder
支持链式调用 .AddStep(step Step) *SagaBuilder
提供 .Build() *SagaDefinition 完成构建并校验（Step 数量 >= 1，
async 类型步骤必须填写 PollURL / PollSuccessPath / PollSuccessValue）

---

### 2. 数据库层（store.go）

定义 Store 接口，包含以下方法：

// 事务操作
CreateTransaction(ctx context.Context, tx *Transaction) error
GetTransaction(ctx context.Context, id string) (*Transaction, error)
UpdateTransactionStatus(ctx context.Context, id, status, lastError string) error
UpdateTransactionStep(ctx context.Context, id string, currentStep int) error

// 步骤操作
CreateSteps(ctx context.Context, steps []*Step) error
GetSteps(ctx context.Context, txID string) ([]*Step, error)
GetStep(ctx context.Context, stepID string) (*Step, error)
UpdateStepStatus(ctx context.Context, stepID, status, lastError string) error
UpdateStepResponse(ctx context.Context, stepID string, response map[string]any) error
IncrementStepRetry(ctx context.Context, stepID string) error
IncrementStepPollCount(ctx context.Context, stepID string, nextPollAt time.Time) error

// 轮询任务操作
CreatePollTask(ctx context.Context, task *PollTask) error
DeletePollTask(ctx context.Context, stepID string) error

// 崩溃恢复：查询需要恢复的事务
ListRecoverableTransactions(ctx context.Context) ([]*Transaction, error)

// 超时事务扫描
ListTimedOutTransactions(ctx context.Context) ([]*Transaction, error)

// 轮询任务扫描（FOR UPDATE SKIP LOCKED，每次最多取 batchSize 条）
AcquirePollTasks(ctx context.Context, instanceID string, batchSize int) ([]*PollTask, error)
ReleasePollTask(ctx context.Context, stepID string) error

提供 PostgresStore 实现上述接口，使用 database/sql。
所有涉及状态变更的操作必须在数据库事务中执行，保证原子性。

---

### 3. 模板渲染（template.go）

实现 RenderTemplate(tpl string, data map[string]any) (string, error)

支持以下模板语法：
  {action_response.field_name}   → 从当前步骤的 action_response 中取值
  {step[0].action_response.field} → 从指定步骤的 action_response 中取值
  {transaction.payload.field}    → 从全局事务 payload 中取值

用于渲染以下字段：
  - compensate_url
  - compensate_payload（JSON 字符串中的模板）
  - poll_url

渲染时接收完整的上下文数据（所有步骤的 action_response + 全局 payload）。
模板变量找不到时返回 error，不静默忽略。

---

### 4. JSONPath 解析（jsonpath.go）

实现 ExtractByPath(data map[string]any, path string) (string, error)

支持简单 JSONPath 语法：
  $.status              → 顶层字段
  $.result.code         → 嵌套字段
  $.items[0].status     → 数组索引

用于解析轮询响应，判断是否匹配 poll_success_value 或 poll_failure_value。
不引入第三方 JSONPath 库，自行实现上述三种语法即可。

---

### 5. 执行器（executor.go）

定义 Executor 结构体，依赖：
- http.Client
- Store
- 超时配置（单次 HTTP 调用超时，默认 30s）

#### 正向执行（同步步骤）
func (e *Executor) ExecuteStep(ctx context.Context, tx *Transaction, step *Step) error

执行流程：
1. UpdateStepStatus → running
2. 渲染 action_url 和 action_payload（支持模板变量）
3. 发起 HTTP 请求，携带请求头：
   - Content-Type: application/json
   - X-Saga-Transaction-Id: {tx.ID}
   - X-Idempotency-Key: {step.ID}    ← 幂等 Key
4. HTTP 状态码 2xx → 解析响应体，UpdateStepResponse，UpdateStepStatus → succeeded
5. 非 2xx → IncrementStepRetry
   retry_count < max_retry → 返回可重试错误
   retry_count >= max_retry → UpdateStepStatus → failed，返回终止错误

#### 正向执行（异步步骤）
func (e *Executor) ExecuteAsyncStep(ctx context.Context, tx *Transaction, step *Step) error

执行流程：
1. UpdateStepStatus → running
2. 发起 POST 请求（与同步步骤相同）
3. 2xx → 记录 action_response，UpdateStepStatus → polling
4. 创建 PollTask（next_poll_at = NOW() + poll_interval_sec）
5. 非 2xx → 同同步步骤失败处理

#### 补偿执行
func (e *Executor) CompensateStep(ctx context.Context, tx *Transaction, step *Step, allSteps []*Step) error

执行流程：
1. UpdateStepStatus → compensating
2. 渲染 compensate_url 和 compensate_payload（注入所有步骤 action_response）
3. 发起补偿 HTTP 请求，同样携带 X-Idempotency-Key
4. 2xx → UpdateStepStatus → compensated
5. 非 2xx → 重试（指数退避，最多 max_retry 次）
   超过重试次数 → 记录错误，返回补偿失败错误（需告警，不自动处理）

---

### 6. 轮询器（poller.go）

定义 Poller 结构体，依赖：
- http.Client
- Store
- instanceID string    // 当前实例唯一标识（用于分布式锁）
- batchSize int        // 每次扫描最多处理的任务数，默认 20
- scanInterval         // 扫描间隔，默认 3s

func (p *Poller) Start(ctx context.Context)
  启动后台 goroutine，循环执行：
  1. AcquirePollTasks（FOR UPDATE SKIP LOCKED）
  2. 对每个任务启动独立 goroutine 处理
  3. 等待 scanInterval

func (p *Poller) processPollTask(ctx context.Context, task *PollTask)
  处理单个轮询任务：
  1. 查询 step 信息
  2. 渲染 poll_url（支持模板变量）
  3. 发起 HTTP GET 请求
  4. 解析响应，用 ExtractByPath 提取结果：
     - 匹配 poll_success_value → 删除 PollTask，UpdateStepStatus → succeeded
       发送信号通知 Coordinator 该事务可以继续
     - 匹配 poll_failure_value → 删除 PollTask，UpdateStepStatus → failed
       发送信号通知 Coordinator 该事务需要补偿
     - 均不匹配（处理中）：
       poll_count >= poll_max_times → 视为超时失败，同上失败处理
       否则 → IncrementStepPollCount，ReleasePollTask
  5. 任何 HTTP 错误：ReleasePollTask，等待下次扫描重试

Coordinator 与 Poller 之间的通知机制：
  使用 map[txID]chan struct{} 的内存 channel
  Poller 完成轮询后向对应 channel 发送信号
  Coordinator 的事务处理 goroutine 监听此 channel

---

### 7. 协调器（coordinator.go）

定义 Coordinator 结构体，依赖：
- Store
- Executor
- Poller（获取通知 channel）
- workerCount int    // 并发处理事务的 goroutine 数量，默认 4
- scanInterval       // 扫描待处理事务的间隔，默认 5s

func (c *Coordinator) Start(ctx context.Context)
  启动以下后台任务：
  1. 崩溃恢复扫描（启动时执行一次）：
     ListRecoverableTransactions → 对每个事务恢复执行
  2. 超时扫描（每 30s 执行一次）：
     ListTimedOutTransactions → 触发补偿
  3. Worker goroutine 池（workerCount 个）：
     从内部任务队列取事务 ID，驱动状态机

func (c *Coordinator) driveTransaction(ctx context.Context, txID string)
  核心状态机，驱动单个事务执行：

  循环执行：
    1. 从 DB 读取事务和所有步骤（加行锁）
    2. 根据事务 status 分支处理：

       status == pending / running：
         找到第一个 status == pending 的步骤
         无 pending 步骤且所有步骤 succeeded → 标记事务 succeeded，退出
         执行该步骤：
           StepTypeSync  → ExecuteStep
             成功 → current_step++，继续循环
             失败 → 触发补偿流程
           StepTypeAsync → ExecuteAsyncStep
             成功（受理）→ 等待 Poller 通知（监听 channel，带超时）
             收到通知后重新读取步骤状态
               succeeded → current_step++，继续循环
               failed    → 触发补偿流程

       status == compensating：
         找到所有需要补偿的步骤（succeeded 状态，按 step_index 逆序）
         逐步执行 CompensateStep
         所有补偿完成 → 标记事务 failed，退出
         补偿失败 → 记录错误，触发告警，退出（等待人工介入）

       status == succeeded / failed：
         退出（终态，无需处理）

---

### 8. 引擎入口（engine.go）

定义 Config 结构体：
- DSN              string          // PostgreSQL 连接串
- WorkerCount      int             // Coordinator worker 数，默认 4
- PollBatchSize    int             // Poller 每次扫描数量，默认 20
- PollScanInterval time.Duration   // Poller 扫描间隔，默认 3s
- CoordScanInterval time.Duration  // Coordinator 扫描间隔，默认 5s
- HTTPTimeout      time.Duration   // 单次 HTTP 调用超时，默认 30s
- InstanceID       string          // 实例唯一标识，空则自动生成（hostname+pid）

定义 Engine 结构体，对外暴露以下方法：

// NewEngine 初始化引擎（建立 DB 连接，不启动后台任务）
func NewEngine(cfg Config) (*Engine, error)

// Start 启动后台 goroutine（Coordinator + Poller）
// 传入的 ctx cancel 后，所有后台任务优雅退出
func (e *Engine) Start(ctx context.Context) error

// Submit 提交一个 SAGA 事务定义，持久化后异步执行
// 返回全局事务 ID
func (e *Engine) Submit(ctx context.Context, def *SagaDefinition) (string, error)

// Query 查询事务状态
func (e *Engine) Query(ctx context.Context, txID string) (*TransactionStatus, error)

// TransactionStatus 对外暴露的状态视图
type TransactionStatus struct {
    ID          string
    Status      string
    CurrentStep int
    Steps       []StepStatus
    LastError   string
    CreatedAt   time.Time
    FinishedAt  *time.Time
}

type StepStatus struct {
    Index      int
    Name       string
    Status     string
    PollCount  int
    LastError  string
}

---

### 9. 业务方使用示例（engine.go 文件末尾，以注释形式提供）

展示以下两种步骤组合的完整使用示例：

示例一：纯同步步骤事务

  engine.NewSaga("order-checkout").
      AddStep(saga.Step{
          Name:             "扣减库存",
          Type:             saga.StepTypeSync,
          ActionMethod:     "POST",
          ActionURL:        "http://stock-service/api/v1/stock/deduct",
          ActionPayload:    map[string]any{"item_id": "SKU-001", "count": 2},
          CompensateMethod: "POST",
          CompensateURL:    "http://stock-service/api/v1/stock/rollback",
          CompensatePayload: map[string]any{"item_id": "SKU-001", "count": 2},
      }).
      AddStep(saga.Step{
          Name:             "创建订单",
          Type:             saga.StepTypeSync,
          ActionMethod:     "POST",
          ActionURL:        "http://order-service/api/v1/orders",
          ActionPayload:    map[string]any{"user_id": "U-001"},
          CompensateMethod: "DELETE",
          CompensateURL:    "http://order-service/api/v1/orders/{action_response.order_id}",
      }).
      Build()

示例二：含异步轮询步骤的事务

  AddStep(saga.Step{
      Name:             "设备配置下发",
      Type:             saga.StepTypeAsync,
      ActionMethod:     "POST",
      ActionURL:        "http://device-service/api/v1/config/apply",
      ActionPayload:    map[string]any{"device_id": "DEV-001"},
      CompensateMethod: "POST",
      CompensateURL:    "http://device-service/api/v1/config/rollback",
      PollURL:          "http://device-service/api/v1/config/status?task_id={action_response.task_id}",
      PollMethod:       "GET",
      PollIntervalSec:  10,
      PollMaxTimes:     30,
      PollSuccessPath:  "$.status",
      PollSuccessValue: "success",
      PollFailurePath:  "$.status",
      PollFailureValue: "failed",
  })

---

### 10. 测试（saga_test.go）

使用 net/http/httptest 搭建 mock 服务端，覆盖以下场景：

1. 全同步事务正常完成
   所有步骤顺序执行成功，事务最终状态 succeeded

2. 同步步骤失败触发补偿
   Step-1 成功，Step-2 失败
   验证：Step-1 的补偿接口被调用，事务最终状态 failed

3. 异步步骤轮询成功
   POST 返回受理，经过 N 次 GET 轮询后返回 success
   验证：事务继续执行后续步骤并最终 succeeded

4. 异步步骤轮询失败触发补偿
   GET 轮询返回 failed
   验证：已完成步骤的补偿接口被调用

5. 异步步骤轮询超时
   poll_count 达到 poll_max_times 仍未得到明确结果
   验证：视为失败，触发补偿

6. 步骤重试
   步骤前两次返回 500，第三次返回 200
   验证：最终成功，retry_count == 2

7. 超时事务补偿
   手动将 timeout_at 设置为过去时间
   验证：Coordinator 扫描到后触发补偿

8. 崩溃恢复
   模拟事务 running 状态下引擎重启
   验证：重启后事务从断点处继续执行

测试使用真实 PostgreSQL（通过环境变量 TEST_DSN 配置连接串）。
每个测试用例前清空相关表，保证隔离性。

---

## 输出要求

1. 按文件分别输出完整代码，每个文件顶部注释标注文件名和包名
2. 所有导出的类型、函数、方法均需有 godoc 注释
3. 不得省略任何实现细节，不得用注释代替代码
4. 所有代码输出完毕后，提供需要在 go.mod 中添加的依赖声明
5. 提供完整的数据库建表 SQL（单独输出为 migrations/saga.sql）
