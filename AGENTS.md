- # Git 提交规范

所有提交到 GitHub 的变更，必须通过 Pull Request（PR）方式合并，禁止直接推送到 main 分支。

操作流程：
1. 从 main 切出新分支（命名规范：`<type>/<short-description>`，例如 `feat/add-auth`、`fix/broker-crash`、`proposal/xxx`）
2. 在新分支上完成开发并提交（commit）
3. 将分支推送到 remote：`git push -u origin <branch-name>`
4. 使用 `gh pr create` 创建 PR，目标分支为 main
5. 不得使用 `git push origin main` 直接推送

- # 背景

我在开发 NSP 项目，这是一个基于 Go 的微服务平台。
我需要在公共基础库（nsp-common）中封装一个统一的 AK/SK 认证模块，
基于 Gin 作为 HTTP 框架，供所有微服务使用。

---

## 技术选型（已确定）

- HTTP 框架：github.com/gin-gonic/gin v1.10.0
- 签名算法：HMAC-SHA256
- 防重放：时间戳（±5分钟容忍窗口）+ Nonce 一次性校验
- 凭证存储：接口设计，提供内存实现，预留数据库/Redis扩展点
- Go 版本：>= 1.21

---

## 目录结构

代码放在如下目录：

nsp-common/
└── pkg/
    └── auth/
        ├── store.go         # 凭证存储接口 + 内存实现
        ├── nonce.go         # Nonce 防重放接口 + 内存实现
        ├── aksk.go          # 签名/验证核心逻辑（不依赖 Gin）
        ├── middleware.go    # Gin 中间件适配层
        └── auth_test.go     # 单元测试

---

## 核心需求

### 1. 凭证模型（store.go）

定义 Credential 结构体，包含以下字段：
- AccessKey string  // AK，公开标识，随请求传输
- SecretKey string  // SK，私钥，仅服务端存储，永远不传输
- Label     string  // 描述，例如 "nsp-order-service"
- Enabled   bool    // 是否启用，false 时拒绝认证

定义 CredentialStore 接口：
- GetByAK(ctx context.Context, ak string) (*Credential, error)
  找不到返回 nil, nil；出错返回 nil, err

提供 MemoryStore 内存实现：
- 初始化时接收 []*Credential 预加载
- 使用 sync.RWMutex 保证并发安全
- 提供 Add(cred *Credential) 方法支持运行时动态注册

---

### 2. Nonce 防重放（nonce.go）

定义 NonceStore 接口：
- CheckAndStore(ctx context.Context, nonce string, ttl time.Duration) (used bool, err error)
  未使用：存储并返回 false；已使用且未过期：返回 true

提供 MemoryNonceStore 内存实现：
- 使用 sync.Mutex 保证并发安全
- 内部用 map[string]time.Time 存储 nonce 与过期时间
- 启动后台 goroutine，每 5 分钟清理一次过期 nonce，防止内存无限增长
- 生产环境建议替换为 Redis 实现（接口已预留）

---

### 3. 请求头规范（aksk.go）

定义以下常量：

Authorization    : "NSP-HMAC-SHA256 AK=<ak>, Signature=<signature>"
X-NSP-Timestamp  : Unix 秒级时间戳字符串
X-NSP-Nonce      : 16 字节随机 hex 字符串
X-NSP-SignedHeaders : 参与签名的请求头列表，小写，分号分隔，已排序
                      默认值："content-type;x-nsp-nonce;x-nsp-timestamp"

---

### 4. 签名字符串构造规范（aksk.go）

StringToSign 由以下内容按行拼接（每行以 \n 结尾，最后一行无 \n）：

  Line 1: HTTP Method（大写，如 POST）
  Line 2: Canonical URI（仅 Path 部分，空则填 /）
  Line 3: Canonical Query String（参数名和参数值均排序，格式 a=1&b=2，无参数则为空字符串）
  Line 4: Canonical Headers（参与签名的请求头，格式为 key:value\n，key 小写，按 SignedHeaders 列表顺序）
  Line 5: SignedHeaders（分号分隔的请求头名列表）
  Line 6: hex(SHA256(body))（body 为空则填空内容的 SHA256）

签名计算：
  signature = hex(HMAC-SHA256(SK, StringToSign))

---

### 5. Signer 客户端签名器（aksk.go）

提供 Signer 结构体：
- NewSigner(ak, sk string) *Signer
- Sign(req *http.Request) error
  自动完成以下步骤：
  1. 填充 X-NSP-Timestamp（当前 Unix 时间戳）
  2. 填充 X-NSP-Nonce（16字节随机 hex）
  3. 读取并 hash body，读完后用 io.NopCloser 还原 req.Body
  4. 确定 SignedHeaders 列表，填充 X-NSP-SignedHeaders 请求头
  5. 构造 StringToSign
  6. 计算 HMAC-SHA256 签名
  7. 填充 Authorization 请求头

---

### 6. Verifier 服务端验证器（aksk.go）

提供 VerifierConfig 结构体：
- TimestampTolerance time.Duration  // 时间戳容忍偏差，默认 5 分钟
- NonceTTL           time.Duration  // Nonce 存储有效期，默认 15 分钟

提供 Verifier 结构体：
- NewVerifier(store CredentialStore, nonces NonceStore, cfg *VerifierConfig) *Verifier
  cfg 为 nil 时使用默认值
- Verify(req *http.Request) (*Credential, error)
  按以下顺序执行验证，任意步骤失败立即返回对应错误：
  1. 解析 Authorization 头，提取 AK 和客户端签名
  2. 验证 X-NSP-Timestamp 是否在容忍窗口内
  3. 验证 X-NSP-Nonce 是否已被使用（调用 NonceStore）
  4. 用 AK 查询凭证（调用 CredentialStore），Enabled=false 视为不存在
  5. 读取并 hash body，读完后还原 req.Body
  6. 重新构造 StringToSign，计算期望签名
  7. 用 hmac.Equal 对比签名（防时序攻击）

---

### 7. 错误定义（aksk.go）

定义以下哨兵错误（使用 errors.New）：

ErrMissingAuthHeader  // Authorization 头缺失
ErrInvalidAuthFormat  // Authorization 头格式错误
ErrMissingTimestamp   // X-NSP-Timestamp 头缺失或格式错误
ErrMissingNonce       // X-NSP-Nonce 头缺失
ErrTimestampExpired   // 时间戳超出容忍窗口
ErrNonceReused        // Nonce 已被使用
ErrAKNotFound         // AK 不存在或已禁用
ErrSignatureMismatch  // 签名不匹配

错误到 HTTP 状态码的映射规则：
- 400 BadRequest     : ErrMissingAuthHeader / ErrInvalidAuthFormat /
                       ErrMissingTimestamp / ErrMissingNonce
- 401 Unauthorized   : ErrTimestampExpired / ErrNonceReused /
                       ErrAKNotFound / ErrSignatureMismatch
- 500 InternalError  : 其他所有错误

---

### 8. Gin 中间件（middleware.go）

提供 MiddlewareOption 结构体：
- Skipper      func(c *gin.Context) bool    // 返回 true 则跳过认证
- OnAuthFailed func(c *gin.Context, err error) // 自定义失败响应，nil 则用默认

提供 AKSKAuthMiddleware(verifier *Verifier, opt *MiddlewareOption) gin.HandlerFunc
执行逻辑：
1. 若 Skipper 返回 true，直接 c.Next() 放行
2. 调用 verifier.Verify(c.Request)
3. 失败：调用 OnAuthFailed，然后 c.Abort()
4. 成功：
   a. c.Set("nsp.auth.credential", cred)           // 写入 gin.Context
   b. 将凭证写入标准 context，更新 c.Request        // 写入标准 context

提供以下辅助函数：
- CredentialFromGin(c *gin.Context) (*Credential, bool)
  从 gin.Context 取凭证，供 Handler 层使用

- ContextWithCredential(ctx context.Context, cred *Credential) context.Context
  将凭证写入标准 context

- CredentialFromContext(ctx context.Context) (*Credential, bool)
  从标准 context 取凭证，供 Service / Repository 层使用

Context Key 使用包内私有空结构体类型（type credentialContextKey struct{}），
避免与其他包 Key 冲突。

---

### 9. 测试（auth_test.go）

覆盖以下测试场景：

1. 签名验证正常流程
   构造合法请求 → Sign → Verify → 返回正确凭证

2. 签名篡改检测
   Sign 后修改 body → Verify → 返回 ErrSignatureMismatch

3. 时间戳过期
   手动设置一个超出容忍窗口的时间戳 → Verify → 返回 ErrTimestampExpired

4. Nonce 重放
   同一请求发送两次 → 第二次 Verify → 返回 ErrNonceReused

5. AK 不存在
   使用未注册的 AK → Verify → 返回 ErrAKNotFound

6. AK 已禁用
   Enabled=false 的凭证 → Verify → 返回 ErrAKNotFound

7. Authorization 头缺失
   不调用 Sign，直接 Verify → 返回 ErrMissingAuthHeader

8. Gin 中间件集成测试
   使用 httptest 搭建 Gin 测试服务器
   合法请求 → 200，且 Handler 能通过 CredentialFromGin 取到凭证
   非法请求 → 401

9. Skipper 豁免
   命中 Skipper 的路由不携带签名 → 200 正常放行

测试工具：使用 net/http/httptest 构造请求和响应，不依赖任何 mock 框架。

---

## 输出要求

1. 按文件分别输出完整代码，每个文件顶部注释标注文件名和包名
2. 所有导出的类型、函数、方法均需有 godoc 注释
3. 不得省略任何实现细节，不得用注释代替代码
4. 所有代码输出完毕后，提供需要在 go.mod 中添加的依赖声明      你编写代码完成后，要进行完整测试，另外代码中的middleware/trace部分是基于net/http的你需要也修改下
- # 背景

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
- # 背景

我在开发 NSP 项目，这是一个基于 Go 的微服务平台，包含 4 个业务服务，
容器化多实例部署在 Kubernetes 上。
我需要在公共基础库（nsp-common）中封装一个统一的分布式链路追踪模块，
以 SDK 形式嵌入每个业务服务，不依赖任何外部 APM 系统。

---

## 技术选型（已确定）

- HTTP 框架：github.com/gin-gonic/gin v1.10.0
- 日志库：已有统一日志模块，提供 Logger 接口（字段化结构日志）
- Go 版本：>= 1.21
- 部署环境：Kubernetes，每个 Pod 的 hostname 即为实例唯一标识
- 不引入 OpenTelemetry，不依赖任何第三方追踪库

---

## 目录结构

nsp-common/
└── pkg/
    └── trace/
        ├── context.go      # TraceContext 结构体 + context.Context 集成
        ├── generator.go    # TraceID / SpanID 生成
        ├── propagator.go   # HTTP 请求头读取与写入
        ├── middleware.go   # Gin 服务端中间件
        ├── client.go       # HTTP 客户端封装（出站请求自动注入）
        └── trace_test.go   # 单元测试

---

## HTTP 请求头规范

### 采用 B3 Multi Header 命名，但不透传 ParentSpanId

使用 B3 标准的 Header 命名（保证业界可读性和将来的兼容性），
但采用现代传播语义（每个服务独立生成 SpanId），
不沿用 B3 原始的 Shared Span 模型，因此不透传 X-B3-ParentSpanId。

服务间调用只传递以下三个请求头：

  X-B3-TraceId : {TraceID}    // 全程透传，不变
  X-B3-SpanId  : {SpanID}     // 发送方自己的 SpanId，接收方存为 ParentSpanId
  X-B3-Sampled : 1            // 采样标志，1=采样 0=不采样

同时兼容 X-Request-ID：

  入口处理逻辑（优先级从高到低）：
    1. 有 X-B3-TraceId → 直接作为 TraceID（链路中间节点）
    2. 无 X-B3-TraceId 但有 X-Request-Id → 用 X-Request-Id 作为 TraceID
    3. 两者都没有 → 生成新 TraceID（入口节点）

  响应头同时写回：
    X-B3-TraceId  = TraceID
    X-Request-Id  = TraceID   ← 兼容只认 X-Request-Id 的客户端和网关

### 接收方处理逻辑

  TraceID      ← header["X-B3-TraceId"]   // 原样读取或新生成
  ParentSpanId ← header["X-B3-SpanId"]    // 上游 SpanId 即为本服务 ParentSpanId
  SpanId       ← 本服务自己生成新的 SpanId  // 每个服务独立生成，不复用
  Sampled      ← header["X-B3-Sampled"] == "1"

### 为什么不透传 X-B3-ParentSpanId

  X-B3-ParentSpanId 透传是 Zipkin 早期 Shared Span 模型的产物：
    客户端和服务端共用同一个 SpanId
    导致下游无法自行推断父子关系，只能由上游显式传递 ParentSpanId

  本方案采用现代独立 Span 模型：
    每个服务独立生成自己的 SpanId
    下游收到上游的 X-B3-SpanId 直接作为自己的 ParentSpanId
    父子关系自然清晰，无需额外传递
    与 W3C Traceparent 标准语义一致

---

## 核心概念约定

### TraceID
- 标识一次完整的请求链路，全程唯一不变
- 格式：16 字节随机数，hex 编码为 32 位字符串
- 示例：4bf92f3577b34da6a3ce929d0e0e4736
- 与 B3 标准格式一致（128bit）

### SpanId
- 标识当前服务对本次请求的一次处理，每个服务独立生成，不复用上游的值
- 格式：8 字节随机数，hex 编码为 16 位字符串
- 示例：00f067aa0ba902b7
- 生成方式：crypto/rand 随机生成，不携带任何业务语义
- 与 B3 标准格式一致（64bit）

### ParentSpanId
- 记录调用当前服务的上游服务的 SpanId
- 来源：从入站请求头 X-B3-SpanId 字段直接读取后赋值
- 无上游（root span）时为空字符串
- 关键约定：ParentSpanId 永远不出现在 HTTP 请求头中，
  只存在于当前服务的 TraceContext 内部和日志字段中

### InstanceId
- 标识处理当前请求的具体服务实例
- 来源：服务启动时读取环境变量 HOSTNAME（k8s pod 名称）
- 不参与请求头传播，只记录在本服务的日志字段中

---

## 核心需求

### 1. TraceContext 结构体（context.go）

定义 TraceContext 结构体：
  TraceID      string   // 全链路唯一标识
  SpanId       string   // 当前服务本次处理的标识
  ParentSpanId string   // 上游服务的 SpanId，root span 时为空
  InstanceId   string   // 当前服务实例标识（来自 HOSTNAME 环境变量）
  Sampled      bool     // 是否采样

使用包内私有空结构体类型作为 context key（type traceContextKey struct{}），
避免与其他包的 key 冲突。

提供以下函数：

// ContextWithTrace 将 TraceContext 注入标准 context
func ContextWithTrace(ctx context.Context, tc *TraceContext) context.Context

// TraceFromContext 从 context 中取出 TraceContext
// 不存在时返回 nil, false
func TraceFromContext(ctx context.Context) (*TraceContext, bool)

// MustTraceFromContext 从 context 中取出 TraceContext
// 不存在时返回一个空的 TraceContext（所有字段为空字符串），不 panic
func MustTraceFromContext(ctx context.Context) *TraceContext

提供 TraceContext 的以下方法：

// IsRoot 判断是否为根 Span（ParentSpanId 为空）
func (tc *TraceContext) IsRoot() bool

// LogFields 返回适合写入结构化日志的字段 map
// 固定包含：trace_id / span_id / instance_id
// ParentSpanId 不为空时额外包含：parent_span_id
// Sampled=false 时所有字段照常返回（采样控制由日志层决定是否输出）
func (tc *TraceContext) LogFields() map[string]string

---

### 2. ID 生成（generator.go）

使用 crypto/rand 实现，保证随机性：

// NewTraceID 生成 32 位 hex 字符串的 TraceID（16字节随机数）
func NewTraceID() string

// NewSpanId 生成 16 位 hex 字符串的 SpanId（8字节随机数）
func NewSpanId() string

// GetInstanceId 读取当前实例标识
// 优先读取环境变量 HOSTNAME
// HOSTNAME 为空时 fallback 到 os.Hostname()
// 两者都失败时返回 "unknown"
func GetInstanceId() string

---

### 3. 传播器（propagator.go）

定义请求头常量：

  HeaderTraceID    = "X-B3-TraceId"     // B3 标准命名
  HeaderSpanId     = "X-B3-SpanId"      // B3 标准命名
  HeaderSampled    = "X-B3-Sampled"     // B3 标准命名
  HeaderRequestID  = "X-Request-Id"     // 兼容网关和老客户端

注意：不定义 X-B3-ParentSpanId 常量，本方案不透传该字段。

提供以下函数：

// Extract 从入站 HTTP 请求中提取 TraceContext
//
// TraceID 来源优先级：
//   1. X-B3-TraceId 有值 → 直接使用（链路中间节点）
//   2. X-B3-TraceId 无值但 X-Request-Id 有值 → 用 X-Request-Id 作为 TraceID
//   3. 两者都无 → 生成新 TraceID（入口节点，root span）
//
// SpanId：始终为本服务生成新的 SpanId，不复用任何请求头中的值
// ParentSpanId：直接赋值为请求头中 X-B3-SpanId 的值（上游的 SpanId）
// instanceId 由调用方传入（服务启动时调用 GetInstanceId() 初始化一次）
func Extract(r *http.Request, instanceId string) *TraceContext

// Inject 向出站 HTTP 请求注入追踪信息
//
// 写入规则：
//   X-B3-TraceId = tc.TraceID          // 透传 TraceID
//   X-B3-SpanId  = tc.SpanId           // 传自己的 SpanId（下游存为 ParentSpanId）
//   X-B3-Sampled = "1" 或 "0"
//
// 注意：不写入 X-B3-ParentSpanId
func Inject(req *http.Request, tc *TraceContext)

// InjectResponse 向 HTTP 响应写入追踪信息（供中间件调用）
//
// 写入规则：
//   X-B3-TraceId = tc.TraceID
//   X-Request-Id = tc.TraceID          // 兼容只认 X-Request-Id 的客户端
func InjectResponse(w http.ResponseWriter, tc *TraceContext)

---

### 4. Gin 服务端中间件（middleware.go）

提供 TraceMiddleware(instanceId string) gin.HandlerFunc

执行逻辑：
1. 调用 Extract(c.Request, instanceId) 提取或生成 TraceContext
2. 将 TraceContext 注入 context：
   a. 写入标准 context，并更新 c.Request：
      ctx := ContextWithTrace(c.Request.Context(), tc)
      c.Request = c.Request.WithContext(ctx)
   b. 同时写入 gin.Context（供不使用标准 context 的 Handler 直接访问）：
      c.Set("nsp.trace", tc)
3. 调用 InjectResponse 向响应头写入追踪信息
4. 调用 c.Next()

提供辅助函数：

// TraceFromGin 从 gin.Context 取出 TraceContext
// 先尝试从 gin.Context 取，取不到再从 c.Request.Context() 取
// 两者都取不到时返回 nil, false
func TraceFromGin(c *gin.Context) (*TraceContext, bool)

---

### 5. HTTP 客户端封装（client.go）

提供 TracedClient 结构体，封装 *http.Client：

// NewTracedClient 创建带追踪能力的 HTTP 客户端
// inner 为 nil 时使用默认 http.Client（30s 超时）
func NewTracedClient(inner *http.Client) *TracedClient

提供以下方法，签名与标准库保持一致：

// Do 发送请求
// 自动从 req.Context() 中取出 TraceContext 并调用 Inject 注入请求头
// context 中无 TraceContext 时不注入，正常发送
func (c *TracedClient) Do(req *http.Request) (*http.Response, error)

// Get 封装 GET 请求
func (c *TracedClient) Get(ctx context.Context, url string) (*http.Response, error)

// Post 封装 POST 请求
func (c *TracedClient) Post(ctx context.Context, url string,
    contentType string, body io.Reader) (*http.Response, error)

注入逻辑说明（在 Do 方法内）：
  从 req.Context() 取出 TraceContext
  调用 Inject(req, tc)
  X-B3-SpanId 写入的是 tc.SpanId（本服务的 SpanId，下游存为 ParentSpanId）
  不写入 X-B3-ParentSpanId

---

### 6. 日志集成约定（以注释形式写在 context.go 末尾）

TraceContext 不直接依赖日志库，通过 LogFields() 与日志模块解耦：

  tc.LogFields() 返回示例：
  {
    "trace_id":      "4bf92f3577b34da6a3ce929d0e0e4736",
    "span_id":       "00f067aa0ba902b7",
    "parent_span_id":"a3f2b1c4d5e6f7a8",   // 有上游时才有此字段
    "instance_id":   "order-pod-7d9f2b"
  }

  日志完整输出示例：
  {
    "time":           "2026-02-27T11:22:59Z",
    "level":          "info",
    "service":        "nsp-order",
    "trace_id":       "4bf92f3577b34da6a3ce929d0e0e4736",
    "span_id":        "00f067aa0ba902b7",
    "parent_span_id": "a3f2b1c4d5e6f7a8",
    "instance_id":    "order-pod-7d9f2b",
    "msg":            "处理订单"
  }

  通过 trace_id 关联同一链路的所有日志
  通过 parent_span_id → span_id 的关系还原调用树
  通过 instance_id 定位到具体 Pod 实例

---

### 7. 测试（trace_test.go）

覆盖以下场景：

1. Extract：无任何追踪头时
   → 生成新 TraceID，ParentSpanId 为空（root span），SpanId 非空

2. Extract：有 X-B3-TraceId 无 X-B3-SpanId 时
   → 继承 TraceID，ParentSpanId 为空，SpanId 新生成

3. Extract：有 X-B3-TraceId 有 X-B3-SpanId 时
   → 继承 TraceID，ParentSpanId = 请求头中的 X-B3-SpanId，SpanId 新生成

4. Extract：无 X-B3-TraceId 但有 X-Request-Id 时
   → TraceID = X-Request-Id 的值，ParentSpanId 为空

5. Extract：每次调用都生成新的 SpanId，不复用请求头中的 X-B3-SpanId

6. Inject：验证写入的头字段
   → 有 X-B3-TraceId（= tc.TraceID）
   → 有 X-B3-SpanId（= tc.SpanId，不是 tc.ParentSpanId）
   → 无 X-B3-ParentSpanId（确认不透传）

7. InjectResponse：验证响应头
   → 有 X-B3-TraceId
   → 有 X-Request-Id（= TraceID）

8. 完整透传链路（核心测试）：
   模拟 gateway → order → stock 三跳调用
   
   验证 TraceID：三跳 TraceID 完全相同
   验证父子关系：
     gateway.ParentSpanId == ""
     order.ParentSpanId   == gateway.SpanId
     stock.ParentSpanId   == order.SpanId
   验证 SpanId 唯一性：
     gateway.SpanId / order.SpanId / stock.SpanId 互不相同
   验证请求头无 X-B3-ParentSpanId：
     每跳的出站请求头中不含 X-B3-ParentSpanId 字段

9. LogFields：
   ParentSpanId 为空时，返回的 map 不含 parent_span_id 字段
   ParentSpanId 不为空时，返回的 map 含 parent_span_id 字段

10. MustTraceFromContext：
    context 中无 TraceContext 时，返回空结构体（非 nil），不 panic

11. TracedClient.Do：
    自动注入追踪头，验证下游收到的请求头：
      X-B3-TraceId 正确
      X-B3-SpanId 正确（= 发送方的 SpanId）
      无 X-B3-ParentSpanId

12. Gin 中间件集成（使用 httptest）：
    验证响应头含 X-B3-TraceId 和 X-Request-Id
    验证 Handler 内可通过 TraceFromGin 取到 TraceContext
    验证 Handler 内可通过 c.Request.Context() 取到 TraceContext
    验证两种方式取到的是同一个 TraceContext

---

## 完整调用链示例（以注释形式附在 propagator.go 末尾）

gateway → order → stock 三跳的完整字段变化：

  gateway（入口，无上游）：
    入站请求头：无追踪头
    TraceID      = 新生成 T1
    SpanId       = 新生成 S1
    ParentSpanId = ""
    出站请求头：
      X-B3-TraceId = T1
      X-B3-SpanId  = S1
      X-B3-Sampled = 1
    响应头：
      X-B3-TraceId = T1
      X-Request-Id = T1

  order（中间节点）：
    入站请求头：
      X-B3-TraceId = T1
      X-B3-SpanId  = S1
    TraceID      = T1        ← 继承自请求头
    SpanId       = 新生成 S2  ← 自己生成，不复用 S1
    ParentSpanId = S1        ← 来自请求头 X-B3-SpanId
    出站请求头：
      X-B3-TraceId = T1
      X-B3-SpanId  = S2      ← 传自己的 SpanId
      X-B3-Sampled = 1
      （无 X-B3-ParentSpanId）

  stock（末端节点）：
    入站请求头：
      X-B3-TraceId = T1
      X-B3-SpanId  = S2
    TraceID      = T1
    SpanId       = 新生成 S3
    ParentSpanId = S2        ← 来自请求头 X-B3-SpanId
    出站请求头：无下游调用

  通过日志还原调用树：
    WHERE trace_id = T1 ORDER BY timestamp
    S1（parent=""）   → gateway （root）
    S2（parent=S1）   → order   （gateway 的子节点）
    S3（parent=S2）   → stock   （order 的子节点）

---

## 输出要求

1. 按文件分别输出完整代码，每个文件顶部注释标注文件名和包名
2. 所有导出的类型、函数、方法均需有 godoc 注释
3. 不得省略任何实现细节，不得用注释代替代码
4. 代码输出完毕后，提供需要在 go.mod 中添加的依赖声明
5. 如有设计决策需要说明，以注释形式写在对应文件内，不在代码块外单独解释
- /quest
