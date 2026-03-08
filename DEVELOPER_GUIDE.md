# NSP Platform 开发者指南

> 模块路径：`github.com/paic/nsp-common`
> Go 版本：1.24+

---

## 目录

- [一、平台架构总览](#一平台架构总览)
- [二、快速开始](#二快速开始)
- [三、模块详细说明](#三模块详细说明)
  - [3.1 Logger 模块](#31-logger-模块)
  - [3.2 Auth 模块](#32-auth-模块)
  - [3.3 Lock 模块](#33-lock-模块)
  - [3.4 Config 模块](#34-config-模块)
  - [3.5 Trace 模块](#35-trace-模块)
  - [3.6 Saga 模块](#36-saga-模块)
  - [3.7 TaskQueue 模块](#37-taskqueue-模块)
- [四、中间件链与请求生命周期](#四中间件链与请求生命周期)
- [五、FAQ 与故障排查](#五faq-与故障排查)

---

## 一、平台架构总览

### 模块关系图

```
┌─────────────────────────────────────────────────────┐
│                   nsp-demo（示例应用）                │
│  cmd/server  cmd/testclient  cmd/lock-demo  ...      │
└───────────────────────┬─────────────────────────────┘
                        │ import
┌───────────────────────▼─────────────────────────────┐
│              nsp-common（公共基础库）                  │
│                                                     │
│  ┌─────────┐  ┌──────┐  ┌──────┐  ┌────────┐      │
│  │ logger  │  │trace │  │ auth │  │ config │      │
│  └─────────┘  └──────┘  └──────┘  └────────┘      │
│                                                     │
│  ┌──────┐  ┌───────────┐  ┌───────────────────┐   │
│  │ lock │  │   saga    │  │    taskqueue      │   │
│  └──────┘  └───────────┘  └───────────────────┘   │
└─────────────────────────────────────────────────────┘
```

### 各模块职责

| 模块 | 包路径 | 职责 |
|------|--------|------|
| **logger** | `pkg/logger` | 统一结构化日志，支持三分类体系（Access/Platform/Business）和日志轮转 |
| **trace** | `pkg/trace` | 分布式链路追踪，B3 协议 Header 传播，自动关联日志 trace_id |
| **auth** | `pkg/auth` | AK/SK HMAC-SHA256 签名认证，防篡改、防重放 |
| **config** | `pkg/config` | 统一配置加载，支持热更新、环境变量覆盖、严格校验 |
| **lock** | `pkg/lock` | 分布式锁，基于 Redis Cluster，支持 Watchdog 自动续期 |
| **saga** | `pkg/saga` | SAGA 分布式事务，自动补偿回滚，支持同步/异步步骤 |
| **taskqueue** | `pkg/taskqueue` | 异步任务队列与 Workflow 编排，支持 Asynq / RocketMQ |

### 设计原则

1. **接口优先**：所有模块对外暴露接口，实现可替换（Logger/Lock/Broker/Store 均如此）
2. **Context 驱动**：trace_id、认证凭证、配置均通过 `context.Context` 在层间传递
3. **可观测性内建**：每个模块自带结构化日志字段常量，统一 trace_id 关联
4. **并发安全**：所有全局状态均通过 `sync.RWMutex` 保护
5. **失败隔离**：热更新回调 panic 隔离、Watchdog 续期失败仅记录不退出

---

## 二、快速开始

以下是一个集成了 Logger、Trace、Auth 三个核心模块的最简示例：

```go
package main

import (
    "github.com/gin-gonic/gin"
    "github.com/paic/nsp-common/pkg/auth"
    "github.com/paic/nsp-common/pkg/logger"
    "github.com/paic/nsp-common/pkg/trace"
)

func main() {
    // 1. 初始化日志（生产用 DefaultConfig，开发用 DevelopmentConfig）
    if err := logger.Init(logger.DefaultConfig("my-service")); err != nil {
        panic(err)
    }
    defer logger.Sync()

    // 2. 获取实例 ID（读取 HOSTNAME 环境变量，K8s 中即为 Pod 名称）
    instanceId := trace.GetInstanceId()

    // 3. 初始化 AK/SK 凭证存储
    credStore := auth.NewMemoryStore([]*auth.Credential{
        {AccessKey: "my-ak", SecretKey: "my-sk", Label: "client-a", Enabled: true},
    })
    nonceStore := auth.NewMemoryNonceStore()
    defer nonceStore.Stop()
    verifier := auth.NewVerifier(credStore, nonceStore, nil)

    // 4. 组装 Gin 路由和中间件
    r := gin.New()
    r.Use(trace.TraceMiddleware(instanceId))   // 注入 trace_id / span_id
    r.Use(auth.AKSKAuthMiddleware(verifier, &auth.MiddlewareOption{
        Skipper: auth.NewSkipperByPath("/health"),
    }))

    r.GET("/health", func(c *gin.Context) {
        c.JSON(200, gin.H{"status": "ok"})
    })

    r.POST("/api/v1/orders", func(c *gin.Context) {
        ctx := c.Request.Context()

        cred, _ := auth.CredentialFromGin(c)
        logger.InfoContext(ctx, "order created", "operator", cred.Label)

        c.JSON(200, gin.H{"message": "ok"})
    })

    logger.Info("server starting", "addr", ":8080")
    r.Run(":8080")
}
```

---

## 三、模块详细说明

---

### 3.1 Logger 模块

#### 功能说明

Logger 模块为 NSP 平台微服务提供统一的结构化日志能力，基于 [Zap](https://github.com/uber-go/zap) 实现，对外暴露兼容标准库 `log/slog` 风格的接口。核心特性：

- **结构化输出**：JSON 或彩色 Console 两种格式
- **三级分类体系**：Access / Platform / Business 日志可独立配置输出路径和级别
- **Context 感知**：自动从 `context.Context` 提取 `trace_id` / `span_id`
- **动态级别**：运行时调整日志级别，无需重启
- **日志轮转**：内置 Lumberjack，支持按大小/天数轮转并压缩

#### 核心接口

```go
// import "github.com/paic/nsp-common/pkg/logger"

type Logger interface {
    // 基础日志方法，args 支持 key-value 交替或 slog.Attr
    Debug(msg string, args ...any)
    Info(msg string, args ...any)
    Warn(msg string, args ...any)
    Error(msg string, args ...any)
    Fatal(msg string, args ...any)  // 打印后调用 os.Exit(1)

    // 带 Context 的日志方法，自动注入 trace_id / span_id
    DebugContext(ctx context.Context, msg string, args ...any)
    InfoContext(ctx context.Context, msg string, args ...any)
    WarnContext(ctx context.Context, msg string, args ...any)
    ErrorContext(ctx context.Context, msg string, args ...any)

    // 派生子 Logger
    With(args ...any) Logger             // 附加固定字段
    WithGroup(name string) Logger        // 字段分组（嵌套 JSON）
    WithContext(ctx context.Context) Logger // 提取 trace 字段并返回子 Logger

    // 运行时控制
    SetLevel(level string) error         // "debug"/"info"/"warn"/"error"
    GetLevel() string
    Sync() error                         // 刷新缓冲，程序退出前必须调用
    Handler() slog.Handler               // 获取底层 slog.Handler
}
```

**全局函数**（对应 `GetLogger().<方法>`）：

```go
logger.Init(cfg)             // 初始化全局 Logger（程序启动时调用一次）
logger.GetLogger()           // 获取全局 Logger 实例
logger.Info(msg, args...)    // 等同于 GetLogger().Info(...)
logger.With(args...)         // 等同于 GetLogger().With(...)
logger.Sync()                // 程序退出前调用
```

**分类 Logger 访问**（需先调用 `InitMultiCategory`）：

```go
logger.Access()    // HTTP 访问日志
logger.Platform()  // 基础设施日志（asynq / saga / redis / db）
logger.Business()  // 业务逻辑日志（与 logger.Biz() 等价）
logger.ForCategory(logger.CategoryBusiness) // 按 LogCategory 枚举访问
```

#### 配置项

**`Config`** — 单 Logger 模式（`Init` 使用）

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `ServiceName` | `string` | —（必填）| 服务名，写入每条日志 |
| `Level` | `Level` | `"info"` | 全局最低日志级别 |
| `Format` | `Format` | `"json"` | 输出格式：`"json"` / `"console"` |
| `OutputPaths` | `[]string` | `["stdout"]` | 简单模式输出目标 |
| `Outputs` | `[]OutputConfig` | `nil` | 高级模式，优先级高于 OutputPaths |
| `Rotation` | `*RotationConfig` | 见下表 | 文件轮转配置（OutputPaths 模式） |
| `EnableCaller` | `bool` | `true` | 输出调用方文件名和行号 |
| `EnableStackTrace` | `bool` | `true` | Error 级别自动附加堆栈 |
| `Development` | `bool` | `false` | 开发模式（彩色输出、DPanic 触发 panic） |
| `Sampling` | `*SamplingConfig` | Initial=100, Thereafter=10 | 高吞吐采样，nil 表示不采样 |

**`RotationConfig`** — 日志轮转

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `MaxSize` | `int` | `100` | 单文件最大 MB，超出后轮转 |
| `MaxBackups` | `int` | `7` | 最多保留几个旧文件 |
| `MaxAge` | `int` | `30` | 旧文件最多保留天数 |
| `Compress` | `bool` | `true` | 旧文件是否 gzip 压缩 |
| `LocalTime` | `bool` | `true` | 轮转文件名使用本地时间 |

**`OutputConfig`** — 多输出高级模式

| 字段 | 类型 | 说明 |
|------|------|------|
| `Type` | `OutputType` | `"stdout"` / `"stderr"` / `"file"` |
| `Path` | `string` | Type 为 `"file"` 时必填 |
| `Level` | `Level` | 此输出独立级别，空则继承全局 |
| `Format` | `Format` | 此输出独立格式，空则继承全局 |
| `Rotation` | `*RotationConfig` | 仅 Type 为 `"file"` 时有效 |

**`MultiCategoryConfig`** — 分类 Logger 模式（`InitMultiCategory` 使用）

| 字段 | 类型 | 说明 |
|------|------|------|
| `ServiceName` | `string` | 必填 |
| `Development` | `bool` | 全局开发模式开关 |
| `Access` | `*CategoryConfig` | nil 则使用默认配置 |
| `Platform` | `*CategoryConfig` | nil 则使用默认配置 |
| `Business` | `*CategoryConfig` | nil 则使用默认配置（自动开启 EnableCaller） |

#### 快速使用

**场景一：单 Logger（最简上手）**

```go
package main

import (
    "context"
    "github.com/paic/nsp-common/pkg/logger"
)

func main() {
    cfg := logger.DefaultConfig("order-service")
    if err := logger.Init(cfg); err != nil {
        panic(err)
    }
    defer logger.Sync()

    logger.Info("service started", "port", 8080)
    logger.Warn("config missing, using defaults", "key", "timeout")

    // 附加固定字段，返回子 Logger
    bizLog := logger.With("module", "order", "version", "v1")
    bizLog.Info("order created", "order_id", "ORD-001", "amount", 99.9)

    // 从 context 自动提取 trace_id / span_id
    ctx := context.Background()
    logger.InfoContext(ctx, "processing payment", "order_id", "ORD-001")
}
```

**场景二：分类 Logger（推荐生产方案）**

```go
func main() {
    cfg := logger.FileMultiCategoryConfig("order-service", "/var/log/order")
    if err := logger.InitMultiCategory(cfg); err != nil {
        panic(err)
    }
    defer logger.SyncAll()

    // → /var/log/order/access.log
    logger.Access().Info("request received",
        "http_method", "POST",
        "http_path",   "/api/v1/orders",
        "http_status", 200,
        "http_latency_ms", 12,
    )

    // → /var/log/order/platform.log
    logger.Platform().Info("task enqueued",
        "component", "asynq",
        "task_type", "send-notification",
        "task_id",   "task-001",
    )

    // → /var/log/order/app.log
    logger.Business().Info("order created",
        "order_id", "ORD-001",
        "user_id",  "U-123",
        "amount",   299.0,
    )
}
```

**场景三：多输出（控制台 + 文件）**

```go
cfg := logger.MultiOutputConfig("order-service", "/var/log/order/app.log")
if err := logger.Init(cfg); err != nil {
    panic(err)
}
defer logger.Sync()
// stdout 输出 console 格式，文件输出 JSON 格式，文件自动轮转
```

#### 标准字段常量

```go
// 通用字段
logger.FieldService      // "service"
logger.FieldTraceID      // "trace_id"
logger.FieldSpanID       // "span_id"
logger.FieldUserID       // "user_id"
logger.FieldModule       // "module"
logger.FieldMethod       // "method"
logger.FieldError        // "error"
logger.FieldLatencyMS    // "latency_ms"

// 访问日志专用字段
logger.FieldHTTPMethod   // "http_method"
logger.FieldHTTPPath     // "http_path"
logger.FieldHTTPStatus   // "http_status"
logger.FieldHTTPLatency  // "http_latency_ms"
logger.FieldClientIP     // "client_ip"

// 平台组件专用字段
logger.FieldComponent    // "component"
logger.FieldTaskType     // "task_type"
logger.FieldTaskID       // "task_id"
logger.FieldWorkflowID   // "workflow_id"
logger.FieldStepName     // "step_name"

// 分类标识
logger.FieldCategory     // "category"
```

#### AccessLogEntry 与 LogAccess

访问日志推荐通过 `AccessLogEntry` 结构体统一记录，而非手动拼字段：

```go
// AccessLogEntry 访问日志结构体
type AccessLogEntry struct {
    Method    string // HTTP 方法
    Path      string // 请求路径
    Query     string // 查询参数
    ClientIP  string // 客户端 IP
    Status    int    // HTTP 状态码
    BodySize  int    // 响应体大小（字节）
    LatencyMS int64  // 请求延迟（毫秒）
    TraceID   string // 追踪 ID
}

// LogAccess 向 Access Logger 写入一条访问日志
func LogAccess(ctx context.Context, entry *AccessLogEntry)

// TraceIDFromContext 从 Context 提取 trace_id 字符串（用于 AccessLogEntry.TraceID）
func TraceIDFromContext(ctx context.Context) string
```

**自定义访问日志中间件示例：**

```go
func accessLogMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        c.Next()

        logger.LogAccess(c.Request.Context(), &logger.AccessLogEntry{
            Method:    c.Request.Method,
            Path:      c.Request.URL.Path,
            Query:     c.Request.URL.RawQuery,
            ClientIP:  c.ClientIP(),
            Status:    c.Writer.Status(),
            BodySize:  c.Writer.Size(),
            LatencyMS: time.Since(start).Milliseconds(),
            TraceID:   logger.TraceIDFromContext(c.Request.Context()),
        })
    }
}
```

#### 注意事项

1. **`Sync()` 不可省略**：Zap 使用缓冲写入，程序退出前若不调用 `Sync()` / `SyncAll()`，末尾日志可能丢失。
2. **`Init` 与 `InitMultiCategory` 互斥**：两者都会设置全局 Logger，只应调用其中一个。`InitMultiCategory` 调用后，`GetLogger()` 返回的是 Business Logger。
3. **采样配置**：`DefaultConfig` 默认开启采样（每秒前 100 条全量，之后每 10 条取 1 条），开发环境用 `DevelopmentConfig`（无采样）。
4. **动态调级**：`logger.SetLevel("debug")` 立即生效，无需重启，适合线上问题排查。

---

### 3.2 Auth 模块

#### 功能说明

Auth 模块为 NSP 平台提供 **AK/SK（Access Key / Secret Key）签名认证**，采用 HMAC-SHA256 算法，防止请求篡改和重放攻击。核心特性：

- **防篡改**：请求方法、路径、查询参数、指定 Headers、请求体均纳入签名
- **防重放**：时间戳窗口（默认 ±5 分钟）+ 一次性 Nonce（默认 15 分钟 TTL）双重防护
- **常量时间比较**：使用 `hmac.Equal` 防止 Timing Attack
- **灵活跳过**：支持按路径或路径前缀豁免认证

#### 核心接口

```go
// import "github.com/paic/nsp-common/pkg/auth"

// 客户端——请求签名
signer := auth.NewSigner(accessKey, secretKey)
err := signer.Sign(req)  // 原地修改 *http.Request 的 Headers

// 服务端——请求验证
verifier := auth.NewVerifier(credStore, nonceStore, &auth.VerifierConfig{
    TimestampTolerance: 5 * time.Minute,
    NonceTTL:           15 * time.Minute,
})
cred, err := verifier.Verify(req)

// Gin 中间件
r.Use(auth.AKSKAuthMiddleware(verifier, &auth.MiddlewareOption{
    Skipper:      auth.NewSkipperByPath("/health", "/metrics"),
    OnAuthFailed: func(c *gin.Context, err error) { /* 自定义响应 */ },
}))

// 从 Context 获取已认证凭证
cred, ok := auth.CredentialFromGin(c)         // gin.Context
cred, ok = auth.CredentialFromContext(ctx)    // context.Context
```

**内置实现**

```go
// 内存凭证存储（开发/测试用）
store := auth.NewMemoryStore([]*auth.Credential{
    {AccessKey: "ak1", SecretKey: "sk1", Label: "service-a", Enabled: true},
})
store.Add(&auth.Credential{...}) // 运行时动态添加

// 内存 Nonce 存储（单实例开发/测试用）
nonceStore := auth.NewMemoryNonceStore()
defer nonceStore.Stop() // 停止后台清理 goroutine
```

#### 认证请求 Headers

| Header | 格式 | 示例 |
|--------|------|------|
| `Authorization` | `NSP-HMAC-SHA256 AK=<ak>, Signature=<sig>` | `NSP-HMAC-SHA256 AK=myak, Signature=abc123...` |
| `X-NSP-Timestamp` | Unix 秒（字符串） | `1741267200` |
| `X-NSP-Nonce` | 16 字节随机 Hex（32 字符） | `a3f2c1d4e5b6a7f8c9d0e1f2a3b4c5d6` |
| `X-NSP-SignedHeaders` | 参与签名的 Header 列表（小写，`;` 分隔） | `content-type;x-nsp-nonce;x-nsp-timestamp` |

#### 签名算法详解

```
StringToSign =
    HTTP_METHOD\n
    CANONICAL_URI\n
    CANONICAL_QUERY_STRING\n
    CANONICAL_HEADERS
    SIGNED_HEADERS\n
    HEX(SHA256(BODY))

Signature = HMAC-SHA256(SecretKey, StringToSign)  →  hex 编码
```

| 部分 | 规则 |
|------|------|
| `HTTP_METHOD` | 大写，如 `POST` |
| `CANONICAL_URI` | 请求路径，空则为 `/` |
| `CANONICAL_QUERY_STRING` | 参数名排序后 URL 编码 |
| `CANONICAL_HEADERS` | SignedHeaders 中每个 Header 的 `小写名:去首尾空格值\n` |
| `HEX(SHA256(BODY))` | 请求体 SHA256 后十六进制，Body 为空时为空字节的哈希 |

#### 错误码映射

| 错误 | HTTP 状态码 | 说明 |
|------|------------|------|
| `ErrMissingAuthHeader` | 400 | 缺少 Authorization Header |
| `ErrInvalidAuthFormat` | 400 | Authorization 格式错误 |
| `ErrMissingTimestamp` | 400 | 缺少或格式错误的时间戳 |
| `ErrMissingNonce` | 400 | 缺少 Nonce Header |
| `ErrTimestampExpired` | 401 | 时间戳超出容忍窗口 |
| `ErrNonceReused` | 401 | Nonce 重复使用（重放攻击）|
| `ErrAKNotFound` | 401 | AK 不存在或已禁用 |
| `ErrSignatureMismatch` | 401 | 签名不匹配 |
| 其他内部错误 | 500 | 存储访问失败等 |

#### 快速使用

**服务端：注册中间件**

```go
credStore := auth.NewMemoryStore([]*auth.Credential{
    {AccessKey: "order-service-ak", SecretKey: "super-secret-key", Label: "order-service", Enabled: true},
})
nonceStore := auth.NewMemoryNonceStore()
defer nonceStore.Stop()

verifier := auth.NewVerifier(credStore, nonceStore, nil)

r := gin.New()
r.Use(auth.AKSKAuthMiddleware(verifier, &auth.MiddlewareOption{
    Skipper: auth.NewSkipperByPath("/health"),
}))

r.POST("/api/v1/orders", func(c *gin.Context) {
    cred, _ := auth.CredentialFromGin(c)
    c.JSON(200, gin.H{"created_by": cred.Label})
})
```

**客户端：签名并发送请求**

```go
signer := auth.NewSigner("order-service-ak", "super-secret-key")

body, _ := json.Marshal(map[string]any{"user_id": "U-123"})
req, _ := http.NewRequest("POST", "http://localhost:8080/api/v1/orders", bytes.NewReader(body))
req.Header.Set("Content-Type", "application/json")

if err := signer.Sign(req); err != nil {
    panic(err)
}

resp, err := http.DefaultClient.Do(req)
```

#### 注意事项

1. **生产环境必须使用 Redis NonceStore**：`MemoryNonceStore` 不跨进程共享，多实例部署时需替换为 Redis 实现。
2. **SecretKey 不传输**：SK 仅本地用于签名计算，永不出现在网络报文中。
3. **请求体大小限制**：Auth 模块对请求体限制为 **10 MB**，超出时返回 `ErrBodyTooLarge`（500）。
4. **`Stop()` 必须调用**：`MemoryNonceStore` 启动了后台 goroutine，程序退出前应调用 `nonceStore.Stop()`。

---

### 3.3 Lock 模块

#### 功能说明

Lock 模块为 NSP 平台提供**分布式锁**能力，基于 Redis + [redsync](https://github.com/go-redsync/redsync) 实现。核心特性：

- **防重入竞争**：同名锁在整个分布式集群中互斥
- **持有者专属释放**：每次加锁生成唯一 Token，只有持有者才能释放
- **抖动重试**：加锁失败后等待 `RetryDelay + 随机抖动`，防止惊群效应
- **Watchdog 自动续期**：加锁成功后启动后台 goroutine，每 `TTL/3` 自动续期

#### 核心接口

```go
// import "github.com/paic/nsp-common/pkg/lock"

type Client interface {
    New(name string, opts ...func(*LockOption)) Lock
    Close() error
}

type Lock interface {
    Acquire(ctx context.Context) error    // 阻塞直到加锁成功或重试耗尽
    TryAcquire(ctx context.Context) error // 非阻塞，失败立即返回 ErrNotAcquired
    Release(ctx context.Context) error    // 原子释放（只有持有者才能释放）
    Renew(ctx context.Context) error      // 重置 TTL 至初始值
    Name() string
}

// 哨兵错误
lock.ErrNotAcquired  // 加锁失败
lock.ErrLockExpired  // 续期或释放时锁已过期
```

#### 配置项

**`LockOption`** — 通过 Functional Options 设置

| 字段 | 默认值 | Option 函数 | 说明 |
|------|--------|-------------|------|
| `TTL` | `8s` | `lock.WithTTL(d)` | 锁自动过期时间 |
| `RetryCount` | `32` | `lock.WithRetryCount(n)` | `Acquire` 最大重试次数 |
| `RetryDelay` | `100ms` | `lock.WithRetryDelay(d)` | 重试基础等待，实际 = RetryDelay + 随机抖动[0, RetryDelay/2) |
| `EnableWatchdog` | `false` | `lock.WithWatchdog()` | 加锁成功后启动自动续期，每 TTL/3 触发一次 |

**`RedisOption`** — 生产环境（Redis Cluster）

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `Addrs` | `[]string` | —（必填）| Cluster 节点地址列表 |
| `Password` | `string` | `""` | Redis AUTH 密码 |
| `PoolSize` | `int` | `10` | 每节点连接池大小 |
| `DialTimeout` | `time.Duration` | `5s` | 建连超时 |
| `ReadTimeout` | `time.Duration` | `3s` | 单命令读超时 |
| `WriteTimeout` | `time.Duration` | `3s` | 单命令写超时 |

#### 锁命名规范

推荐格式：`{domain}:{resource_type}:{resource_id}`

```
order:pay:ORD-123        // 订单支付锁
inventory:stock:SKU-456  // 库存扣减锁
cron:job:daily-report    // 定时任务防并发锁
```

#### 快速使用

**场景一：标准加锁（带 Watchdog）**

```go
client, err := lock.NewRedisClient(lock.RedisOption{
    Addrs:    []string{"redis-0:6379", "redis-1:6379", "redis-2:6379"},
    Password: "your-redis-password",
})
if err != nil {
    panic(err)
}
defer client.Close()

l := client.New(
    "order:pay:ORD-001",
    lock.WithTTL(10*time.Second),
    lock.WithWatchdog(), // 每 3.3s 自动续期
)

if err := l.Acquire(ctx); err != nil {
    if errors.Is(err, lock.ErrNotAcquired) {
        return errors.New("order is being processed by another node")
    }
    return err
}
defer l.Release(ctx) // Release 同时停止 Watchdog goroutine

// 执行业务逻辑
```

**场景二：非阻塞尝试加锁**

```go
l := client.New("cron:job:daily-report", lock.WithTTL(30*time.Second))

if err := l.TryAcquire(ctx); err != nil {
    if errors.Is(err, lock.ErrNotAcquired) {
        logger.Info("job already running on another node, skipping")
        return nil
    }
    return err
}
defer l.Release(ctx)
```

**场景三：本地开发（单节点 Redis）**

```go
client, err := lock.NewStandaloneRedisClient(lock.StandaloneRedisOption{
    Addr: "localhost:6379",
})
```

#### 注意事项

1. **Redis Cluster 不支持多 DB**：`RedisOption` 刻意不提供 `DB` 字段，仅支持 DB 0。
2. **`New` 每次返回独立 Token**：每次调用 `client.New(...)` 返回一个新的锁实例，不要跨 goroutine 共用同一个 `Lock` 实例。
3. **Watchdog + defer Release**：启用 Watchdog 时，`defer l.Release(ctx)` 是释放 goroutine 的唯一途径，**不能省略**。
4. **`ctx` 超时会中断 Acquire**：超时后返回 `ctx.Err()`，而非 `ErrNotAcquired`，注意区分。
5. **生产与开发分离**：`NewStandaloneRedisClient` 仅供开发和 Demo 使用，生产环境必须使用 `NewRedisClient`。

---

### 3.4 Config 模块

#### 功能说明

Config 模块为 NSP 平台提供**统一的配置加载与热更新**能力，基于 [Viper](https://github.com/spf13/viper) 封装实现。核心特性：

- **严格解析**：使用 `UnmarshalExact`，配置文件中出现未知字段时报错
- **热更新**：`Watch=true` 时监听文件变更，自动触发 `OnChange` 回调
- **去抖动**：50ms 窗口合并多次连续 fsnotify 事件
- **环境变量覆盖**：设置 `EnvPrefix` 后，`NSP_SERVER_PORT` 自动覆盖 `server.port`
- **Panic 隔离**：单个 `OnChange` 回调 panic 不会影响其余回调执行

#### 核心接口

```go
// import "github.com/paic/nsp-common/pkg/config"

type Loader interface {
    Load(target any) error                    // 读取并反序列化到 target（必须为指针）
    OnChange(fn func(apply func(any) error)) // 注册热更新回调
    Close()                                   // 停止监听，释放资源（幂等）
}

loader, err := config.New(config.Option{...})
```

#### 配置项

**`Option`**

| 字段 | 类型 | 说明 |
|------|------|------|
| `ConfigFile` | `string` | 配置文件完整路径，与 `ConfigName+ConfigPaths` 互斥，优先级更高 |
| `ConfigName` | `string` | 配置文件名（不含扩展名），须与 `ConfigPaths` 配合使用 |
| `ConfigPaths` | `[]string` | 配置文件搜索路径列表 |
| `ConfigType` | `string` | 文件格式（`"yaml"` / `"json"` / `"toml"`），空则由扩展名自动推断 |
| `Defaults` | `map[string]any` | 默认值，支持 dot notation（`"server.port": 8080`） |
| `Watch` | `bool` | 是否启用热更新 |
| `EnvPrefix` | `string` | 环境变量前缀，`.` 映射为 `_`（`server.port` → `NSP_SERVER_PORT`） |

> 结构体字段须标注 `mapstructure` 标签：`Port int \`mapstructure:"port"\``

#### 快速使用

**场景一：一次性加载**

```go
type AppConfig struct {
    Server struct {
        Host string `mapstructure:"host"`
        Port int    `mapstructure:"port"`
    } `mapstructure:"server"`
}

loader, err := config.New(config.Option{
    ConfigFile: "./config/config.yaml",
    Defaults:   map[string]any{"server.port": 8080},
})
if err != nil {
    panic(err)
}
defer loader.Close()

var cfg AppConfig
if err := loader.Load(&cfg); err != nil {
    panic(err)
}
```

**场景二：热更新**

```go
loader, err := config.New(config.Option{
    ConfigFile: "./config/config.yaml",
    Watch:      true,
    EnvPrefix:  "NSP",
})
defer loader.Close()

var cfg AppConfig
loader.Load(&cfg)

loader.OnChange(func(apply func(any) error) {
    var newCfg AppConfig
    if err := apply(&newCfg); err != nil {
        logger.Error("config reload failed", "error", err)
        return
    }
    cfg = newCfg  // 注意并发安全，生产环境请用 atomic 或 RWMutex
    logger.Info("config reloaded")
})
```

**场景三：环境变量覆盖**

```bash
export NSP_SERVER_PORT=9090
export NSP_DATABASE_PASSWORD=prod-secret
```

```go
loader, err := config.New(config.Option{
    ConfigFile: "./config/config.yaml",
    EnvPrefix:  "NSP",
    // server.port → NSP_SERVER_PORT（自动将 "." 替换为 "_"）
})
```

#### 注意事项

1. **严格模式**：`Load` 使用 `UnmarshalExact`，配置文件中出现结构体无对应字段的 key 时报错。
2. **热更新并发安全**：`OnChange` 回调中更新业务变量时，需自行保证并发安全。
3. **Watch 在首次 `Load` 后启动**：文件监听在第一次成功 `Load` 后才开始。
4. **K8s Secret 热更新**：Viper 内部已正确处理 K8s Secret 挂载的原子符号链接替换，无需额外适配。

#### 支持的配置格式

| 格式 | 扩展名 | 说明 |
|------|--------|------|
| YAML | `.yaml`, `.yml` | 推荐，可读性好 |
| JSON | `.json` | 通用格式 |
| TOML | `.toml` | 适合简单配置 |
| HCL | `.hcl` | HashiCorp 配置语言 |
| ENV | `.env` | 环境变量文件 |
| Properties | `.properties` | Java 风格配置 |

#### Kubernetes 部署示例

通过环境变量注入敏感配置（密码、密钥），配置文件中留空：

```yaml
# config/config.yaml
redis:
  addrs:
    - "redis-0.redis.svc.cluster.local:6379"
  password: ""  # 通过环境变量 NSP_REDIS_PASSWORD 注入
database:
  password: ""  # 通过环境变量 NSP_DATABASE_PASSWORD 注入
```

```yaml
# deployment.yaml
env:
  - name: NSP_REDIS_PASSWORD
    valueFrom:
      secretKeyRef:
        name: app-secrets
        key: redis-password
  - name: NSP_DATABASE_PASSWORD
    valueFrom:
      secretKeyRef:
        name: app-secrets
        key: db-password
```

---

### 3.5 Trace 模块

#### 功能说明

Trace 模块为 NSP 平台提供**分布式链路追踪**能力，遵循 [B3 规范](https://github.com/openzipkin/b3-propagation)进行 Header 传播。核心特性：

- **B3 协议兼容**：使用 `X-B3-TraceId` / `X-B3-SpanId` 标准 Header
- **独立 Span 模型**：每个服务始终生成新的 SpanId，上游 SpanId 作为 ParentSpanId
- **Logger 自动联动**：中间件注入后，`InfoContext(ctx, ...)` 自动携带 `trace_id` / `span_id`
- **出站自动注入**：`TracedClient` 封装 HTTP 客户端，发出的请求自动携带追踪 Header
- **兼容 X-Request-Id**：支持网关以 `X-Request-Id` 传入 TraceID

#### 核心接口与类型

```go
// import "github.com/paic/nsp-common/pkg/trace"

type TraceContext struct {
    TraceID      string // 全链路唯一 ID，32位 hex（128bit）
    SpanId       string // 本服务本次处理的 ID，16位 hex（64bit）
    ParentSpanId string // 上游服务的 SpanId，root span 时为空
    InstanceId   string // 服务实例标识（来自 HOSTNAME 环境变量）
    Sampled      bool   // 是否采样，默认 true
}

func (tc *TraceContext) IsRoot() bool
func (tc *TraceContext) LogFields() map[string]string

// Gin 中间件
trace.TraceMiddleware(instanceId string) gin.HandlerFunc

// Context 操作
trace.ContextWithTrace(ctx, tc) context.Context
trace.TraceFromContext(ctx) (*TraceContext, bool)
trace.MustTraceFromContext(ctx) *TraceContext  // 不存在返回空结构体，不 panic
trace.TraceFromGin(c) (*TraceContext, bool)

// Header 传播
trace.Extract(r *http.Request, instanceId string) *TraceContext
trace.Inject(req *http.Request, tc *TraceContext)
trace.InjectResponse(w http.ResponseWriter, tc *TraceContext)

// ID 生成
instanceId := trace.GetInstanceId() // 读取 HOSTNAME 或 os.Hostname()
traceID    := trace.NewTraceID()    // 32位 hex
spanId     := trace.NewSpanId()    // 16位 hex
```

**带追踪能力的 HTTP 客户端**

```go
client := trace.NewTracedClient(nil) // nil 使用默认 30s 超时
resp, err := client.Get(ctx, url)
resp, err = client.Post(ctx, url, "application/json", body)
resp, err = client.Do(req)
```

#### 传播 Header 说明

| Header | 方向 | 格式 | 说明 |
|--------|------|------|------|
| `X-B3-TraceId` | 入站 / 出站 / 响应 | 32位 hex | 全链路唯一 ID |
| `X-B3-SpanId` | 入站 / 出站 | 16位 hex | 入站时作为 ParentSpanId，出站时传自己的 SpanId |
| `X-B3-Sampled` | 入站 / 出站 | `"1"` / `"0"` | 采样标志，默认 `"1"` |
| `X-Request-Id` | 入站 / 响应 | 32位 hex | 兼容网关，可用作 TraceID |

#### 三跳调用链完整示意

```
Gateway（入口，无上游头）
  TraceID = T1（新生成），SpanId = S1，ParentSpanId = ""
  出站请求头: X-B3-TraceId=T1, X-B3-SpanId=S1

    ↓ 调用 Order Service

Order Service
  TraceID = T1（继承），SpanId = S2（新生成），ParentSpanId = S1
  出站请求头: X-B3-TraceId=T1, X-B3-SpanId=S2

    ↓ 调用 Stock Service

Stock Service
  TraceID = T1，SpanId = S3（新生成），ParentSpanId = S2

日志查询（WHERE trace_id=T1）：
  S1 parent="" → Gateway（root）
  S2 parent=S1 → Order
  S3 parent=S2 → Stock
```

#### 快速使用

**注册中间件**

```go
instanceId := trace.GetInstanceId()

r := gin.New()
r.Use(trace.TraceMiddleware(instanceId)) // 必须在 Logger 中间件之前
```

**在 Service 层传播追踪**

```go
var httpClient = trace.NewTracedClient(nil)

func callStockService(ctx context.Context, skuID string) error {
    // 自动注入 X-B3-TraceId / X-B3-SpanId
    resp, err := httpClient.Get(ctx, "http://stock-service/api/v1/stock/"+skuID)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    return nil
}
```

#### 注意事项

1. **中间件注册顺序**：`TraceMiddleware` 必须在 Logger 中间件**之前**注册。
2. **`GetInstanceId()` 只调用一次**：服务启动时调用一次后传入中间件，避免每次请求重新读取。
3. **TraceID 格式校验**：`Extract` 内部对入站 Header 做"32位合法 hex"格式验证，格式不符时自动生成新 ID。
4. **K8s 环境**：Pod 的 `HOSTNAME` 即为 Pod 名称，`instance_id` 字段可精确定位到具体 Pod。

---

### 3.6 Saga 模块

#### 功能说明

Saga 模块为 NSP 平台提供 **SAGA 分布式事务**能力。通过预定义正向操作（Action）和补偿操作（Compensate），在任意步骤失败时自动按倒序触发已成功步骤的补偿，实现最终一致性。核心特性：

- **自动补偿回滚**：任意步骤失败，已执行步骤按倒序自动补偿
- **同步/异步两种步骤**：同步步骤立即完成，异步步骤通过轮询 API 确认结果
- **模板变量**：步骤 URL 和 Payload 支持引用上游步骤响应和全局 Payload
- **持久化状态**：事务和步骤状态写入 PostgreSQL，引擎重启后自动恢复
- **分布式并发安全**：轮询任务使用 `FOR UPDATE SKIP LOCKED` 保证多实例安全

#### 状态机

```
事务：  pending ──► running ──► succeeded
                       └──► compensating ──► failed

步骤（同步）：  pending ──► running ──► succeeded
                                └──► failed ──► compensating ──► compensated

步骤（异步）：  pending ──► running ──► polling ──► succeeded
                                           └──► failed ──► compensating ──► compensated
```

#### 核心接口

```go
// import "github.com/paic/nsp-common/pkg/saga"

engine, err := saga.NewEngine(&saga.Config{DSN: "postgres://..."})
engine.Start(ctx)               // 启动协调器和轮询器后台任务
txID, err := engine.Submit(ctx, def)  // 提交事务
status, err := engine.Query(ctx, txID) // 查询状态
engine.Stop()                   // 优雅停止

// Builder
def, err := saga.NewSaga("name").
    WithPayload(map[string]any{...}).
    WithTimeout(300).
    AddStep(saga.Step{...}).
    Build()
```

#### 配置项

**`Config`**

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `DSN` | `string` | —（必填）| PostgreSQL 连接串 |
| `WorkerCount` | `int` | `4` | 协调器并发 Worker 数 |
| `PollBatchSize` | `int` | `20` | 每次轮询扫描任务数 |
| `PollScanInterval` | `time.Duration` | `3s` | 轮询任务扫描间隔 |
| `CoordScanInterval` | `time.Duration` | `5s` | 协调器扫描间隔 |
| `HTTPTimeout` | `time.Duration` | `30s` | 步骤 HTTP 请求超时 |

**`Step`** 关键字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `Name` | `string` | 步骤名称 |
| `Type` | `StepType` | `StepTypeSync`（默认）/ `StepTypeAsync` |
| `ActionMethod` / `ActionURL` | `string` | 正向操作，必填，支持模板 |
| `CompensateMethod` / `CompensateURL` | `string` | 补偿操作，必填，支持模板 |
| `PollURL` / `PollSuccessPath` / `PollSuccessValue` | `string` | 异步步骤必填 |
| `PollIntervalSec` / `PollMaxTimes` | `int` | 默认 5s / 60次 |
| `MaxRetry` | `int` | 步骤最大重试次数，默认 3 |

#### 模板变量语法

| 语法 | 说明 |
|------|------|
| `{action_response.field}` | 当前步骤响应字段 |
| `{step[N].action_response.field}` | 第 N 步（0-based）的响应字段 |
| `{transaction.payload.field}` | 全局 Payload 字段 |
| `$.field` / `$.nested.field` | JSONPath（用于 PollSuccessPath）|

#### 快速使用

**场景一：纯同步步骤事务**

```go
def, err := saga.NewSaga("order-checkout").
    AddStep(saga.Step{
        Name:              "扣减库存",
        Type:              saga.StepTypeSync,
        ActionMethod:      "POST",
        ActionURL:         "http://stock-service/api/v1/stock/deduct",
        ActionPayload:     map[string]any{"item_id": "SKU-001", "count": 2},
        CompensateMethod:  "POST",
        CompensateURL:     "http://stock-service/api/v1/stock/rollback",
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
    WithPayload(map[string]any{"amount": 299.0}).
    WithTimeout(300).
    Build()

txID, err := engine.Submit(ctx, def)
```

**场景二：含异步轮询步骤**

```go
def, err := saga.NewSaga("device-config-apply").
    AddStep(saga.Step{
        Name:              "下发配置",
        Type:              saga.StepTypeAsync,
        ActionMethod:      "POST",
        ActionURL:         "http://device-service/api/v1/config/apply",
        ActionPayload:     map[string]any{"device_id": "DEV-001"},
        CompensateMethod:  "POST",
        CompensateURL:     "http://device-service/api/v1/config/rollback",
        CompensatePayload: map[string]any{"device_id": "DEV-001"},
        PollURL:           "http://device-service/api/v1/config/status?task_id={action_response.task_id}",
        PollIntervalSec:   10,
        PollMaxTimes:      30,
        PollSuccessPath:   "$.status",
        PollSuccessValue:  "success",
        PollFailurePath:   "$.status",
        PollFailureValue:  "failed",
    }).
    Build()
```

**场景三：查询状态**

```go
status, err := engine.Query(ctx, txID)
fmt.Printf("事务 %s 状态: %s\n", status.ID, status.Status)
for _, step := range status.Steps {
    fmt.Printf("  步骤[%d] %s: %s\n", step.Index, step.Name, step.Status)
}
```

#### 注意事项

1. **幂等性要求**：每个 Action 和 Compensate 接口必须幂等，引擎可能重试同一步骤。
2. **补偿不可失败**：Compensate 接口可靠性要求高于 Action，补偿失败需人工介入。
3. **数据库迁移**：使用前需先建表，参考下方 SQL 或 `nsp-common/migrations/saga.sql`。
4. **引擎重启恢复**：持久化到 PostgreSQL 的未完成事务会在引擎重启后自动恢复执行。

#### 数据库表结构

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

#### 错误类型

```go
var (
    ErrNoSteps                    = errors.New("saga must have at least one step")
    ErrAsyncStepMissingPollConfig = errors.New("async step must have PollURL, PollSuccessPath, and PollSuccessValue")
    ErrStepMissingAction          = errors.New("step must have ActionMethod and ActionURL")
    ErrStepMissingCompensate      = errors.New("step must have CompensateMethod and CompensateURL")
)
```

---

### 3.7 TaskQueue 模块

#### 功能说明

TaskQueue 模块为 NSP 平台提供**异步任务队列与 Workflow 编排**能力。核心特性：

- **接口抽象**：`Broker` / `Consumer` 解耦消息队列实现，支持 Asynq 和 RocketMQ
- **有序 Workflow**：步骤按 `StepOrder` 依次执行，前一步完成后自动触发下一步入队
- **自动重试**：步骤失败按 `MaxRetries` 自动重试，耗尽后标记 Workflow 失败
- **手动重试**：支持通过 `RetryStep` 对已失败步骤单独重试
- **优先级队列**：四档优先级映射到不同队列

#### 架构角色

```
Orchestrator（编排侧）                  Worker（执行侧）
┌──────────────────────────────┐        ┌─────────────────────────────┐
│  Engine.SubmitWorkflow(...)  │        │  Consumer.Handle(type, fn)  │
│         │ 入队                │        │         │ 接收任务           │
│         ▼                    │        │         ▼                   │
│  Broker.Publish(step[0])─────┼──────►│  HandlerFunc(ctx, payload)  │
│                              │        │         │                   │
│  Engine.HandleCallback(...)◄─┼────────┤  CallbackSender.Success/Fail│
│         │ 驱动状态机           │        └─────────────────────────────┘
│         ▼                    │
│  Broker.Publish(step[N+1])   │
└──────────────────────────────┘
```

#### 核心接口

```go
// import "github.com/paic/nsp-common/pkg/taskqueue"

type Broker interface {
    Publish(ctx context.Context, task *Task) (*TaskInfo, error)
    Close() error
}

type Consumer interface {
    Handle(taskType string, handler HandlerFunc)
    Start(ctx context.Context) error
    Stop() error
}

type HandlerFunc func(ctx context.Context, payload *TaskPayload) (*TaskResult, error)

// Engine — 编排侧
engine, err := taskqueue.NewEngine(cfg, broker)
workflowID, err := engine.SubmitWorkflow(ctx, def)
err = engine.HandleCallback(ctx, cb)
resp, err := engine.QueryWorkflow(ctx, workflowID)
err = engine.RetryStep(ctx, stepID)

// CallbackSender — Worker 侧
sender := engine.NewCallbackSender()
sender.Success(ctx, taskID, result)
sender.Fail(ctx, taskID, errorMsg)
```

#### 配置项

**`Config`**

| 字段 | 类型 | 说明 |
|------|------|------|
| `DSN` | `string` | PostgreSQL 连接串，必填 |
| `CallbackQueue` | `string` | Worker 回调消息的目标队列名 |
| `QueueRouter` | `QueueRouterFunc` | 自定义队列路由函数，nil 使用默认规则 |

**优先级常量**

| 常量 | 值 | 说明 |
|------|----|------|
| `PriorityLow` | 1 | 低优先级 |
| `PriorityNormal` | 3 | 普通优先级（默认）|
| `PriorityHigh` | 6 | 高优先级 |
| `PriorityCritical` | 9 | 紧急优先级 |

#### 快速使用

**编排侧：提交 Workflow**

```go
broker, _ := asynqbroker.NewBroker(asynqbroker.Config{RedisAddr: "localhost:6379"})
defer broker.Close()

engine, _ := taskqueue.NewEngine(&taskqueue.Config{
    DSN:           "postgres://...",
    CallbackQueue: "task-callbacks",
}, broker)
defer engine.Stop()

engine.Migrate(context.Background()) // 首次部署执行一次

params, _ := json.Marshal(map[string]any{"vlan_id": 100})
workflowID, err := engine.SubmitWorkflow(ctx, &taskqueue.WorkflowDefinition{
    Name:         "network-provisioning",
    ResourceType: "switch",
    ResourceID:   "SW-001",
    Steps: []taskqueue.StepDefinition{
        {TaskType: "create_vlan", TaskName: "创建VLAN", Params: string(params), QueueTag: "switch", Priority: taskqueue.PriorityHigh},
        {TaskType: "bind_ports",  TaskName: "绑定端口",  Params: string(params), QueueTag: "switch"},
    },
})
```

**Worker 侧：注册处理函数**

```go
consumer, _ := asynqbroker.NewConsumer(asynqbroker.ConsumerConfig{
    RedisAddr:   "localhost:6379",
    Queues:      map[string]int{"tasks_switch_high": 6, "tasks_switch": 3},
    Concurrency: 10,
})

sender := taskqueue.NewCallbackSenderFromBroker(broker, "task-callbacks")

consumer.Handle("create_vlan", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    var params struct {
        VlanID int `json:"vlan_id"`
    }
    json.Unmarshal(payload.Params, &params)

    // 执行业务逻辑 ...

    sender.Success(ctx, payload.TaskID, map[string]any{"vlan_id": params.VlanID})
    return &taskqueue.TaskResult{Message: "vlan created"}, nil
})

consumer.Start(context.Background())
```

**编排侧：消费 Callback 驱动状态机**

```go
callbackConsumer.Handle("task_callback", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
    var cb taskqueue.CallbackPayload
    json.Unmarshal(payload.Params, &cb)
    engine.HandleCallback(ctx, &cb)
    return &taskqueue.TaskResult{Message: "ok"}, nil
})
```

#### 注意事项

1. **Callback 队列必须被消费**：Orchestrator 侧必须消费 `CallbackQueue`，否则工作流永远停在 Running 状态。
2. **Worker 内必须发送 Callback**：无论成功还是失败，Handler 都必须调用 `sender.Success` 或 `sender.Fail`。
3. **TaskType 需一致**：`StepDefinition.TaskType` 与 `consumer.Handle(taskType, ...)` 注册的 key 必须完全一致。
4. **Broker 实现可替换**：将 `asynqbroker.NewBroker(...)` 替换为 `rocketmqbroker.NewBroker(...)` 即可切换消息队列。

#### 队列路由

`DefaultQueueRouter` 的路由规则（QueueTag + Priority → 队列名）：

| QueueTag | Priority | 队列名 |
|----------|----------|--------|
| `""` | Normal | `tasks` |
| `""` | High | `tasks_high` |
| `""` | Critical | `tasks_critical` |
| `"huawei"` | Normal | `tasks_huawei` |
| `"huawei"` | High | `tasks_huawei_high` |
| `"cisco"` | Critical | `tasks_cisco_critical` |

自定义路由：

```go
engine, _ := taskqueue.NewEngine(&taskqueue.Config{
    DSN: "...",
    QueueRouter: func(queueTag string, priority taskqueue.Priority) string {
        return fmt.Sprintf("my_queue_%s_%d", queueTag, priority)
    },
}, broker)
```

#### 数据库表结构

```sql
CREATE TABLE IF NOT EXISTS workflows (
    id              VARCHAR(64)  PRIMARY KEY,
    name            VARCHAR(128) NOT NULL,
    resource_type   VARCHAR(64)  NOT NULL,
    resource_id     VARCHAR(128) NOT NULL,
    status          VARCHAR(20)  NOT NULL DEFAULT 'pending',
    total_steps     INT          NOT NULL DEFAULT 0,
    completed_steps INT          NOT NULL DEFAULT 0,
    failed_steps    INT          NOT NULL DEFAULT 0,
    error_message   TEXT,
    metadata        JSONB,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_workflows_status   ON workflows(status);
CREATE INDEX IF NOT EXISTS idx_workflows_resource ON workflows(resource_type, resource_id);

CREATE TABLE IF NOT EXISTS step_tasks (
    id              VARCHAR(64)  PRIMARY KEY,
    workflow_id     VARCHAR(64)  NOT NULL REFERENCES workflows(id),
    step_order      INT          NOT NULL,
    task_type       VARCHAR(128) NOT NULL,
    task_name       VARCHAR(256),
    params          TEXT,
    status          VARCHAR(20)  NOT NULL DEFAULT 'pending',
    priority        INT          NOT NULL DEFAULT 3,
    queue_tag       VARCHAR(64),
    broker_task_id  VARCHAR(128),
    result          TEXT,
    error_message   TEXT,
    retry_count     INT          NOT NULL DEFAULT 0,
    max_retries     INT          NOT NULL DEFAULT 3,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    queued_at       TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_step_tasks_workflow ON step_tasks(workflow_id, step_order);
CREATE INDEX IF NOT EXISTS idx_step_tasks_status   ON step_tasks(status);
```

---

## 四、中间件链与请求生命周期

### 推荐中间件注册顺序

```go
r := gin.New()
r.Use(middleware.GinRecovery())            // 1. 捕获 panic，防止服务崩溃
r.Use(trace.TraceMiddleware(instanceId))   // 2. 注入 trace_id / span_id
r.Use(logger.AccessLogMiddleware())        // 3. 记录访问日志（需 trace 先注入）
r.Use(auth.AKSKAuthMiddleware(verifier, opt)) // 4. 验证 AK/SK 签名
// Handler 接收到完整的请求上下文
```

### Context 流转图

```
HTTP 请求
    │
    ▼
[Recovery]
    │ 捕获 panic，返回 500
    ▼
[TraceMiddleware]
    │ Extract TraceContext（继承或新建 TraceID，新建 SpanId）
    │ context.WithValue(ctx, traceKey, tc)
    │ logger.ContextWithTraceID(ctx, tc.TraceID)
    │ logger.ContextWithSpanID(ctx, tc.SpanId)
    │ 响应头写入 X-B3-TraceId / X-Request-Id
    ▼
[AccessLogMiddleware]
    │ 从 ctx 读取 trace_id / span_id 写入访问日志
    ▼
[AKSKAuthMiddleware]
    │ 验证签名，成功后：
    │ c.Set("nsp.auth.credential", cred)
    │ context.WithValue(ctx, credentialKey, cred)
    ▼
[Handler]
    │ 可从 ctx 获取：
    │   trace.TraceFromContext(ctx)      → *TraceContext
    │   auth.CredentialFromContext(ctx)  → *Credential
    │   logger.InfoContext(ctx, ...)     → 自动带 trace_id / span_id
    ▼
HTTP 响应
```

### 服务间调用的 Context 传播

```go
func (h *Handler) CreateOrder(c *gin.Context) {
    ctx := c.Request.Context() // 已包含 trace + credential

    // 调用下游服务，自动传播 trace Header
    resp, err := httpClient.Post(ctx, "http://stock/deduct", "application/json", body)

    // 日志自动带 trace_id
    logger.Business().InfoContext(ctx, "order created", "order_id", "ORD-001")

    // 传给 service 层
    result, err := h.orderService.Create(ctx, req)
}

func (s *OrderService) Create(ctx context.Context, req *CreateOrderReq) (*Order, error) {
    // service 层同样可以用 ctx
    cred, _ := auth.CredentialFromContext(ctx)
    logger.Business().InfoContext(ctx, "processing", "operator", cred.Label)
    return nil, nil
}
```

---

## 五、FAQ 与故障排查

**Q：日志中没有 `trace_id` 字段，如何排查？**

A：检查以下两点：
1. `TraceMiddleware` 是否在 Logger 中间件**之前**注册
2. 日志调用是否使用了 `xxxContext(ctx, ...)` 形式，普通 `logger.Info(...)` 不会自动提取 trace 信息

---

**Q：AK/SK 认证失败返回 401，但签名看起来是正确的？**

A：常见原因：
1. **时间戳偏差**：客户端和服务端时钟差超过 5 分钟，检查 NTP 同步
2. **Nonce 重复**：同一 Nonce 在 15 分钟内被使用第二次，每次请求需生成新 Nonce
3. **Header 大小写**：`X-NSP-SignedHeaders` 中的 Header 名必须小写
4. **Body 被修改**：签名后请求体不能再被修改，检查 HTTP 框架是否有 Body 处理逻辑

---

**Q：分布式锁 `Acquire` 一直阻塞不返回？**

A：`Acquire` 默认最多重试 32 次，每次间隔约 100ms，总计约 3.2 秒。若超出，返回 `ErrNotAcquired`。若确实阻塞：
1. 检查 Redis 连接是否正常
2. 传入的 `ctx` 是否设置了合理的 Deadline：`ctx, cancel := context.WithTimeout(ctx, 5*time.Second)`
3. 检查是否有其他节点持有锁但未释放（如程序崩溃），等待 TTL 自动过期

---

**Q：Saga 事务提交后状态一直是 `pending`，未变为 `running`？**

A：检查：
1. `engine.Start(ctx)` 是否已调用——引擎的协调器后台任务需要 `Start` 后才开始工作
2. PostgreSQL 连接是否正常
3. `saga_transactions` 表是否存在——参考 `migrations/saga.sql` 初始化表结构

---

**Q：TaskQueue 的 Workflow 一直处于 Running 状态，步骤不推进？**

A：最常见原因是编排侧没有消费 `CallbackQueue`。需要确认：
1. 编排侧已注册并启动对 `CallbackQueue` 的消费
2. Worker 侧 Handler 内确实调用了 `sender.Success` 或 `sender.Fail`
3. Broker（Redis / RocketMQ）连接正常，消息未积压

---

**Q：Config 热更新不生效？**

A：检查：
1. `Option.Watch` 是否设置为 `true`
2. `loader.Load(&cfg)` 是否成功调用过一次（热更新在首次 Load 成功后才启动监听）
3. 文件修改后等待 50ms 去抖动窗口，若仍不触发，检查 inotify 是否支持（某些 NFS 挂载不支持 fsnotify）

---

**Q：多实例部署时，如何保证 AK/SK 的 Nonce 防重放有效？**

A：`MemoryNonceStore` 不跨进程共享，多实例下同一 Nonce 可在不同实例通过验证。**生产环境必须实现 Redis 版本的 `NonceStore`**，用 `SETNX` + TTL 原子操作保证全局唯一性：

```go
// 示例：Redis NonceStore 实现思路
func (s *RedisNonceStore) CheckAndStore(ctx context.Context, nonce string, ttl time.Duration) (bool, error) {
    key := "nsp:nonce:" + nonce
    // SETNX 成功说明 nonce 是新的（返回 false = 未使用）
    // SETNX 失败说明 nonce 已存在（返回 true = 已使用）
    ok, err := s.rdb.SetNX(ctx, key, 1, ttl*2).Result()
    if err != nil {
        return false, err
    }
    return !ok, nil // SetNX 返回 true 表示设置成功（未使用），取反
}
```
