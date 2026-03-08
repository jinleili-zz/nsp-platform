# Logger 模块

> 包路径：`github.com/paic/nsp-common/pkg/logger`

## 功能说明

Logger 模块解决以下问题：

- **统一日志格式**：所有微服务使用一致的 JSON 结构化日志格式
- **分类日志输出**：Access / Platform / Business 三类日志可独立配置
- **链路追踪集成**：自动从 Context 提取 trace_id、span_id
- **动态级别调整**：运行时切换日志级别，无需重启服务
- **高性能输出**：基于 Zap 实现，支持采样和异步写入

---

## 核心接口

```go
// Logger 主日志接口
type Logger interface {
    // 基础日志方法（支持 key-value 或 slog.Attr）
    Debug(msg string, args ...any)
    Info(msg string, args ...any)
    Warn(msg string, args ...any)
    Error(msg string, args ...any)
    Fatal(msg string, args ...any)  // 记录后调用 os.Exit(1)

    // Context 感知日志方法（自动提取 trace_id/span_id）
    DebugContext(ctx context.Context, msg string, args ...any)
    InfoContext(ctx context.Context, msg string, args ...any)
    WarnContext(ctx context.Context, msg string, args ...any)
    ErrorContext(ctx context.Context, msg string, args ...any)

    // 派生 Logger
    With(args ...any) Logger           // 附加固定字段
    WithGroup(name string) Logger      // 字段分组
    WithContext(ctx context.Context) Logger  // 从 Context 提取字段

    // 管理方法
    Sync() error                       // 刷新缓冲区
    SetLevel(level string) error       // 动态设置级别
    GetLevel() string                  // 获取当前级别
    Handler() slog.Handler             // 获取底层 Handler

    // 分类访问（用于多类别日志场景）
    Access() Logger                    // 访问日志
    Platform() Logger                  // 平台日志
    Business() Logger                  // 业务日志
}
```

---

## 配置项

### 基础配置 `Config`

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `Level` | `Level` | `"info"` | 日志级别：debug/info/warn/error |
| `Format` | `Format` | `"json"` | 输出格式：json/console |
| `ServiceName` | `string` | **必填** | 服务名称，每条日志都会携带 |
| `OutputPaths` | `[]string` | `["stdout"]` | 输出目标：stdout/stderr/文件路径 |
| `Outputs` | `[]OutputConfig` | `nil` | 高级多输出配置（与 OutputPaths 互斥） |
| `Rotation` | `*RotationConfig` | 见下表 | 日志轮转配置 |
| `EnableCaller` | `bool` | `true` | 是否记录调用位置 |
| `EnableStackTrace` | `bool` | `true` | Error 级别是否记录堆栈 |
| `Development` | `bool` | `false` | 开发模式（彩色输出、详细堆栈） |
| `Sampling` | `*SamplingConfig` | 见下表 | 采样配置（高吞吐场景） |

### 日志轮转配置 `RotationConfig`

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `MaxSize` | `int` | `100` | 单文件最大 MB 数 |
| `MaxBackups` | `int` | `7` | 保留的历史文件数 |
| `MaxAge` | `int` | `30` | 历史文件保留天数 |
| `Compress` | `bool` | `true` | 是否 gzip 压缩历史文件 |
| `LocalTime` | `bool` | `true` | 文件名使用本地时间 |

### 采样配置 `SamplingConfig`

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `Initial` | `int` | `100` | 每秒完整输出的日志条数 |
| `Thereafter` | `int` | `10` | 超出后每 N 条输出一条 |

---

## 快速使用

### 单一 Logger 模式

```go
package main

import (
    "context"

    "github.com/paic/nsp-common/pkg/logger"
)

func main() {
    // 生产环境配置
    cfg := logger.DefaultConfig("order-service")
    if err := logger.Init(cfg); err != nil {
        panic(err)
    }
    defer logger.Sync()

    // 基础日志
    logger.Info("服务启动", "port", 8080)

    // 带字段的日志
    orderLogger := logger.With("module", "order")
    orderLogger.Info("创建订单", "order_id", "ORD-001", "user_id", "U-123")

    // Context 感知日志（自动携带 trace_id）
    ctx := logger.ContextWithTraceID(context.Background(), "abc123def456")
    logger.InfoContext(ctx, "处理请求", "action", "create_order")

    // 动态调整级别
    if err := logger.SetLevel("debug"); err != nil {
        logger.Error("设置级别失败", logger.FieldError, err)
    }
}
```

### 多类别日志模式

```go
package main

import (
    "github.com/paic/nsp-common/pkg/logger"
)

func main() {
    // 文件分类输出配置
    cfg := logger.FileMultiCategoryConfig("order-service", "/var/log/order")
    // 生成：
    //   /var/log/order/access.log   - HTTP 访问日志
    //   /var/log/order/platform.log - 框架/基础设施日志
    //   /var/log/order/app.log      - 业务逻辑日志

    if err := logger.InitMultiCategory(cfg); err != nil {
        panic(err)
    }
    defer logger.SyncAll()

    // 访问日志（HTTP 请求/响应）
    logger.Access().Info("HTTP Request",
        logger.FieldHTTPMethod, "GET",
        logger.FieldHTTPPath, "/api/orders",
        logger.FieldHTTPStatus, 200,
        logger.FieldHTTPLatency, 45,
    )

    // 平台日志（框架组件）
    logger.Platform().Info("Redis 连接成功",
        logger.FieldComponent, "redis",
        "pool_size", 10,
    )

    // 业务日志（应用逻辑）
    logger.Business().Info("订单创建成功",
        "order_id", "ORD-001",
        "amount", 199.99,
    )
}
```

---

## 与其他模块集成

### 与 Trace 模块集成

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/paic/nsp-common/pkg/logger"
    "github.com/paic/nsp-common/pkg/trace"
)

func setupRouter() *gin.Engine {
    router := gin.New()

    // Trace 中间件会将 trace_id/span_id 写入 Context
    router.Use(trace.TraceMiddleware(trace.GetInstanceId()))

    router.GET("/api/orders", func(c *gin.Context) {
        // InfoContext 自动从 Context 提取 trace_id/span_id
        logger.InfoContext(c.Request.Context(), "查询订单列表")

        // 输出示例：
        // {"level":"info","msg":"查询订单列表","trace_id":"abc123","span_id":"def456"}
    })

    return router
}
```

### 使用 AccessLogEntry 记录访问日志

```go
import (
    "time"

    "github.com/gin-gonic/gin"
    "github.com/paic/nsp-common/pkg/logger"
)

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

---

## 日志分类体系

### 三类日志说明

| 类别 | 输出文件 | 典型场景 | EnableCaller |
|------|----------|----------|--------------|
| Access | `access.log` | HTTP 请求/响应、网关流量 | false |
| Platform | `platform.log` | Redis、Asynq、SAGA、数据库 | false |
| Business | `app.log` | Handler、Service、Repository | true |

### 使用场景

```go
// Access - 记录 HTTP 流量
logger.Access().Info("HTTP Request",
    logger.FieldHTTPMethod, "POST",
    logger.FieldHTTPPath, "/api/orders",
    logger.FieldHTTPStatus, 201,
    logger.FieldHTTPLatency, 45,
    logger.FieldClientIP, "10.0.0.1",
)

// Platform - 记录基础设施事件
logger.Platform().Info("任务执行完成",
    logger.FieldComponent, "asynq",
    logger.FieldTaskType, "email:send",
    logger.FieldTaskID, "task-123",
    logger.FieldLatencyMS, 120,
)

// Business - 记录业务逻辑
logger.Business().Info("订单创建成功",
    "order_id", "ORD-001",
    "user_id", "U-123",
    "amount", 199.99,
    "payment_method", "alipay",
)
```

---

## 注意事项

### 性能提示

- 生产环境务必启用 Sampling，高 QPS 下可显著降低 I/O 压力
- 使用 `With()` 预绑定固定字段，避免重复创建
- Error 级别的 StackTrace 开销较大，按需开启

### 常见错误

```go
// 错误：直接拼接字符串
logger.Info("用户登录: " + userID)  // 性能差，无法结构化查询

// 正确：使用字段化
logger.Info("用户登录", "user_id", userID)

// 错误：忽略 Sync 调用
func main() {
    logger.Init(cfg)
    // 缺少 defer logger.Sync()，程序退出时可能丢失日志
}

// 正确：始终 defer Sync
func main() {
    logger.Init(cfg)
    defer logger.Sync()
}
```

### 安全要点

- 不要记录敏感信息（密码、密钥、身份证号等）
- 生产环境关闭 `IncludeQuery`，避免 URL 参数泄露
- 日志文件权限应限制为 `0600`

---

## 标准字段常量

```go
// 通用字段
const (
    FieldService    = "service"      // 服务名
    FieldTraceID    = "trace_id"     // 追踪 ID
    FieldSpanID     = "span_id"      // Span ID
    FieldUserID     = "user_id"      // 用户 ID
    FieldRequestID  = "request_id"   // 请求 ID
    FieldModule     = "module"       // 模块名
    FieldError      = "error"        // 错误信息
    FieldLatencyMS  = "latency_ms"   // 延迟(ms)
)

// HTTP 访问日志字段
const (
    FieldHTTPMethod   = "http_method"     // HTTP 方法
    FieldHTTPPath     = "http_path"       // HTTP 路径
    FieldHTTPStatus   = "http_status"     // HTTP 状态码
    FieldHTTPLatency  = "http_latency_ms" // HTTP 延迟
    FieldClientIP     = "client_ip"       // 客户端 IP
    FieldUserAgent    = "user_agent"      // User-Agent
)

// 平台日志字段
const (
    FieldComponent  = "component"   // 组件名
    FieldTaskType   = "task_type"   // 任务类型
    FieldTaskID     = "task_id"     // 任务 ID
    FieldQueue      = "queue"       // 队列名
    FieldRetryCount = "retry_count" // 重试次数
)
```
