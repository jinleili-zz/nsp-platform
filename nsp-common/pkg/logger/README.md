# logger

NSP 平台统一日志模块，基于 [zap](https://github.com/uber-go/zap) 实现，提供结构化日志、日志分类、上下文追踪、多输出目标和动态级别调整能力。

## 目录

- [快速开始](#快速开始)
- [初始化配置](#初始化配置)
  - [预置配置](#预置配置)
  - [Config 完整字段](#config-完整字段)
- [日志级别与格式](#日志级别与格式)
- [日志分类](#日志分类)
  - [Access 日志](#access-日志)
  - [Platform 日志](#platform-日志)
  - [Business 日志](#business-日志)
  - [分类独立配置](#分类独立配置)
- [上下文追踪](#上下文追踪)
- [字段常量](#字段常量)
- [多输出目标](#多输出目标)
- [日志文件轮转](#日志文件轮转)
- [子 Logger（With / WithGroup）](#子-loggerwith--withgroup)
- [动态调整日志级别](#动态调整日志级别)
- [框架适配（WriterAdapter）](#框架适配writeradapter)
- [全局便捷函数](#全局便捷函数)

---

## 快速开始

```go
import "github.com/yourorg/nsp-common/pkg/logger"

func main() {
    // 初始化（生产默认配置）
    if err := logger.Init(logger.DefaultConfig("my-service")); err != nil {
        log.Fatal(err)
    }
    defer logger.Sync()

    logger.Info("server started", "port", 8080)
    logger.Warn("high memory usage", "used_mb", 512)
    logger.Error("database error", "error", err)
}
```

输出示例（JSON 格式）：

```json
{"level":"info","timestamp":"2026-03-02T10:00:00.000+0800","message":"server started","service":"my-service","port":8080}
```

---

## 初始化配置

### 预置配置

| 函数 | 适用场景 | 级别 | 格式 | 采样 |
|------|----------|------|------|------|
| `DefaultConfig(name)` | 生产环境 | info | JSON | 开启 |
| `DevelopmentConfig(name)` | 本地开发 | debug | console（彩色） | 关闭 |
| `MultiOutputConfig(name, file)` | 多目标输出 | info | stdout=console / file=JSON | 开启 |

```go
// 生产环境
logger.Init(logger.DefaultConfig("order-service"))

// 本地开发（彩色 console 输出）
logger.Init(logger.DevelopmentConfig("order-service"))

// 同时输出到 stdout 和文件
logger.Init(logger.MultiOutputConfig("order-service", "/var/log/app.log"))
```

### Config 完整字段

```go
cfg := &logger.Config{
    ServiceName:      "my-service", // 必填，注入每条日志的 service 字段
    Level:            logger.LevelInfo,
    Format:           logger.FormatJSON,
    OutputPaths:      []string{"stdout"},       // 简单模式
    Outputs:          []logger.OutputConfig{},  // 高级模式，优先级高于 OutputPaths
    Rotation:         logger.DefaultRotationConfig(),
    EnableCaller:     true,  // 记录调用文件和行号
    EnableStackTrace: true,  // error 级别自动附加堆栈
    Development:      false,
    Sampling: &logger.SamplingConfig{
        Initial:    100, // 每秒前 100 条完整输出
        Thereafter: 10,  // 超出后每 10 条输出 1 条
    },
    Categories: map[logger.Category]logger.CategoryConfig{
        // 见下方日志分类章节
    },
}
```

---

## 日志级别与格式

**级别**（从低到高）：`debug` → `info` → `warn` → `error`

```go
logger.LevelDebug  // "debug"
logger.LevelInfo   // "info"
logger.LevelWarn   // "warn"
logger.LevelError  // "error"
```

**格式**：

```go
logger.FormatJSON    // 生产环境推荐，机器可读
logger.FormatConsole // 开发环境推荐，人类友好
```

---

## 日志分类

日志分类将不同场景的日志隔离管理，每个分类可以配置独立的输出路径和日志级别。每条日志自动附加 `log_category` 字段标识来源。

三种内置分类：

| 分类 | 常量 | 典型场景 |
|------|------|----------|
| Access | `CategoryAccess` | HTTP/gRPC 请求记录 |
| Platform | `CategoryPlatform` | 数据库、消息队列、缓存、框架内部 |
| Business | `CategoryBusiness` | 业务逻辑、领域事件、操作审计 |

### Access 日志

记录每一次外部请求的关键信息：

```go
func RequestLogMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        next.ServeHTTP(w, r)

        logger.Access().InfoContext(r.Context(), "request completed",
            logger.FieldHTTPMethod, r.Method,
            logger.FieldPath,       r.URL.Path,
            logger.FieldHTTPStatus, http.StatusOK,
            logger.FieldLatencyMS,  time.Since(start).Milliseconds(),
            logger.FieldPeerAddr,   r.RemoteAddr,
            logger.FieldUserAgent,  r.UserAgent(),
            logger.FieldRequestID,  r.Header.Get("X-Request-ID"),
        )
    })
}
```

输出示例：

```json
{
  "level": "info",
  "timestamp": "2026-03-02T10:00:00.000+0800",
  "message": "request completed",
  "service": "my-service",
  "log_category": "access",
  "http_method": "GET",
  "path": "/api/v1/orders",
  "http_status": 200,
  "latency_ms": 45,
  "peer_addr": "10.0.0.1:52341",
  "request_id": "req-abc-123"
}
```

### Platform 日志

记录基础设施和框架层的运行状态：

```go
// 慢查询警告
logger.Platform().Warn("slow db query",
    logger.FieldModule,    "user-repository",
    logger.FieldMethod,    "FindByID",
    logger.FieldLatencyMS, 3200,
)

// 连接错误
logger.Platform().Error("redis connection failed",
    logger.FieldModule, "cache",
    logger.FieldError,  err.Error(),
    logger.FieldCode,   "CONN_REFUSED",
)
```

### Business 日志

记录业务领域事件和操作审计：

```go
// 业务操作记录
logger.Business().InfoContext(ctx, "order created",
    logger.FieldBizDomain, "order",
    logger.FieldBizID,     "ORD-2026-001",
    logger.FieldOperation, "create",
    logger.FieldUserID,    "USR-42",
)

// 支付失败
logger.Business().WarnContext(ctx, "payment failed",
    logger.FieldBizDomain, "payment",
    logger.FieldBizID,     "PAY-9876",
    logger.FieldOperation, "charge",
    logger.FieldCode,      "INSUFFICIENT_BALANCE",
)
```

### 分类独立配置

通过 `Config.Categories` 为每个分类指定独立的输出路径和日志级别：

```go
cfg := &logger.Config{
    ServiceName: "my-service",
    Level:       logger.LevelInfo,
    OutputPaths: []string{"stdout"}, // 全局默认，Platform 分类使用此配置

    Categories: map[logger.Category]logger.CategoryConfig{
        // Access 日志：写入独立文件，info 级别
        logger.CategoryAccess: {
            Level:       logger.LevelInfo,
            OutputPaths: []string{"/var/log/my-service/access.log"},
        },
        // Business 日志：写入独立文件，debug 级别记录更多细节
        logger.CategoryBusiness: {
            Level:       logger.LevelDebug,
            OutputPaths: []string{"/var/log/my-service/business.log"},
        },
        // CategoryPlatform 不配置 → 自动继承全局 OutputPaths（stdout）
    },
}
logger.Init(cfg)
```

**配置继承规则**（优先级从高到低）：

1. 分类 `Outputs`（高级多输出配置）
2. 分类 `OutputPaths`（简单路径配置）
3. 全局 `Outputs`
4. 全局 `OutputPaths`

未在 `Categories` 中配置的分类会自动 fallback 到全局 logger，并自动附加 `log_category` 字段。

---

## 上下文追踪

在请求入口将 `trace_id` / `span_id` 注入 context，后续使用 `*Context` 方法打日志时自动携带：

```go
// 在 HTTP 中间件 / RPC 拦截器中注入
ctx = logger.ContextWithTraceID(ctx, traceID)
ctx = logger.ContextWithSpanID(ctx, spanID)

// 使用 Context 方法打日志，自动附加 trace_id 和 span_id
logger.InfoContext(ctx, "processing order", "order_id", "ORD-001")
logger.Business().ErrorContext(ctx, "payment timeout", logger.FieldBizID, "PAY-999")
```

将 logger 存入 context，实现跨函数传递：

```go
// 创建含请求级字段的子 logger，存入 context
reqLogger := logger.Platform().With(logger.FieldRequestID, requestID)
ctx = logger.ContextWithLogger(ctx, reqLogger)

// 在任意下游函数中取出
l := logger.FromContext(ctx)
l.Info("downstream operation")
```

---

## 字段常量

所有字段键均定义为常量，避免拼写错误：

```go
// 通用字段
logger.FieldService     // "service"     — 服务名（自动注入）
logger.FieldLogCategory // "log_category" — 日志分类（自动注入）
logger.FieldTraceID     // "trace_id"
logger.FieldSpanID      // "span_id"
logger.FieldRequestID   // "request_id"
logger.FieldUserID      // "user_id"
logger.FieldModule      // "module"
logger.FieldMethod      // "method"
logger.FieldPath        // "path"
logger.FieldCode        // "code"
logger.FieldLatencyMS   // "latency_ms"
logger.FieldError       // "error"
logger.FieldPeerAddr    // "peer_addr"

// Access 日志专用
logger.FieldHTTPMethod  // "http_method"
logger.FieldHTTPStatus  // "http_status"

// Business 日志专用
logger.FieldBizDomain   // "biz_domain"
logger.FieldBizID       // "biz_id"
logger.FieldOperation   // "operation"
```

---

## 多输出目标

使用 `Outputs` 字段实现精细化的多目标输出，每个目标可以有独立的格式和日志级别：

```go
cfg := &logger.Config{
    ServiceName: "my-service",
    Level:       logger.LevelDebug,
    Outputs: []logger.OutputConfig{
        {
            // stdout：console 格式，所有级别
            Type:   logger.OutputTypeStdout,
            Format: logger.FormatConsole,
            Level:  logger.LevelDebug,
        },
        {
            // 应用日志文件：JSON 格式，info 及以上
            Type:     logger.OutputTypeFile,
            Path:     "/var/log/app.log",
            Format:   logger.FormatJSON,
            Level:    logger.LevelInfo,
            Rotation: logger.DefaultRotationConfig(),
        },
        {
            // 错误日志文件：只记录 error
            Type:     logger.OutputTypeFile,
            Path:     "/var/log/error.log",
            Format:   logger.FormatJSON,
            Level:    logger.LevelError,
            Rotation: logger.DefaultRotationConfig(),
        },
    },
}
```

---

## 日志文件轮转

文件输出默认启用轮转（基于 [lumberjack](https://github.com/natefinch/lumberjack)）：

```go
rotation := &logger.RotationConfig{
    MaxSize:    100, // 单文件最大 100 MB
    MaxBackups: 7,   // 最多保留 7 个备份
    MaxAge:     30,  // 备份最多保留 30 天
    Compress:   true,  // gzip 压缩历史文件
    LocalTime:  true,  // 使用本地时间命名备份文件
}
```

`DefaultRotationConfig()` 返回上述默认值。

---

## 子 Logger（With / WithGroup）

`With` 创建携带固定字段的子 logger，适合在模块或请求处理函数内复用：

```go
// 模块级子 logger
repoLogger := logger.Platform().With(
    logger.FieldModule, "order-repository",
)
repoLogger.Info("query executed", logger.FieldLatencyMS, 12)
repoLogger.Error("query failed", logger.FieldError, err.Error())

// 带上下文的子 logger
func ProcessOrder(ctx context.Context, orderID string) {
    l := logger.Business().WithContext(ctx).With(
        logger.FieldBizDomain, "order",
        logger.FieldBizID,     orderID,
    )
    l.Info("start processing")
    // ... 后续调用 l.Info / l.Error 均自动携带上述字段
}
```

`WithGroup` 将后续字段归入一个命名分组，用于组织复杂的结构化数据：

```go
logger.Platform().WithGroup("db").Info("query",
    "sql",      "SELECT * FROM orders",
    "duration", "12ms",
)
// 输出: {"db": {"sql": "SELECT * FROM orders", "duration": "12ms"}}
```

---

## 动态调整日志级别

运行时无需重启即可更改日志级别，适合与配置中心集成：

```go
// 临时开启 debug 排查问题
logger.SetLevel("debug")

// 排查完毕，恢复 info
logger.SetLevel("info")

// 获取当前级别
fmt.Println(logger.GetLevel()) // "info"
```

---

## 框架适配（WriterAdapter）

`WriterAdapter` 将 `io.Writer` 接口桥接到 logger，适用于 Gin、GORM、Asynq 等框架：

```go
// Gin 访问日志
ginWriter := logger.NewWriterAdapter(
    logger.Access(),
    logger.WithLevel("info"),
    logger.WithPrefix("[gin]"),
)
gin.DefaultWriter = ginWriter

// GORM 慢查询日志（带 context 追踪）
gormWriter := logger.NewWriterAdapter(
    logger.Platform(),
    logger.WithLevel("warn"),
    logger.WithPrefix("[gorm]"),
    logger.WithContext(ctx),
)
```

---

## 全局便捷函数

`Init` 初始化以后，可以直接使用包级函数，无需持有 logger 实例：

```go
// 基本日志
logger.Debug("msg", "k", "v")
logger.Info("msg", "k", "v")
logger.Warn("msg", "k", "v")
logger.Error("msg", "k", "v")
logger.Fatal("msg", "k", "v") // 打印后调用 os.Exit(1)

// 带 context（自动提取 trace_id / span_id）
logger.DebugContext(ctx, "msg")
logger.InfoContext(ctx, "msg")
logger.WarnContext(ctx, "msg")
logger.ErrorContext(ctx, "msg")

// 分类日志
logger.Access()   // → CategoryAccess Logger
logger.Platform() // → CategoryPlatform Logger
logger.Business() // → CategoryBusiness Logger

// 创建子 logger
logger.With("module", "auth")
logger.WithGroup("request")

// 维护
logger.SetLevel("debug")
logger.GetLevel()
logger.Sync()
```
