# NSP-Demo HTTP Application

基于 `nsp-common/pkg/logger` 封装的 HTTP 应用示例，演示如何在 HTTP 服务中集成分布式追踪日志。

## 新增功能：AK/SK 认证模块

本项目已集成 `nsp-common/pkg/auth` 提供的统一 AK/SK 认证模块，支持：
- HMAC-SHA256 请求签名
- 时间戳防重放（±5分钟容忍窗口）
- Nonce 一次性校验
- Gin 中间件集成

## 项目结构

```
nsp-demo/
├── cmd/
│   └── server/
│       └── main.go           # HTTP 服务入口
├── internal/
│   ├── handler/
│   │   ├── handler.go        # HTTP 请求处理器
│   │   └── handler_test.go   # 处理器测试
│   └── middleware/
│       ├── trace.go          # 分布式追踪中间件 (支持 net/http 和 Gin)
│       ├── logger.go         # 日志中间件
│       ├── recovery.go       # Panic 恢复中间件
│       └── middleware_test.go # 中间件测试
├── go.mod
└── README.md

nsp-common/
└── pkg/
    ├── auth/                 # AK/SK 认证模块
    │   ├── store.go          # 凭证存储接口 + 内存实现
    │   ├── nonce.go          # Nonce 防重放接口 + 内存实现
    │   ├── aksk.go           # 签名/验证核心逻辑
    │   ├── middleware.go     # Gin 中间件适配层
    │   └── auth_test.go      # 单元测试
    └── logger/               # 日志模块
        └── ...
```

## 功能特性

### 1. AK/SK 认证支持

- **HMAC-SHA256 签名**: 基于请求头、URI、Query、Body 计算签名
- **时间戳防重放**: ±5分钟容忍窗口，防止重放攻击
- **Nonce 一次性校验**: 16字节随机 hex，15分钟有效期
- **Gin 中间件集成**: 支持 Skipper 豁免、自定义错误处理

### 2. 分布式追踪支持

- **自动生成 Trace ID**: 每个请求自动生成唯一的 trace_id (32 位十六进制)
- **自动生成 Span ID**: 每个请求自动生成唯一的 span_id (16 位十六进制)
- **透传 Trace ID**: 支持通过 `X-Trace-ID` Header 传入上游 trace_id
- **响应头返回**: 在响应头中返回 `X-Trace-ID` 和 `X-Span-ID`

### 2. 结构化日志输出

每条日志自动包含：
- `service`: 服务名称
- `trace_id`: 分布式追踪 ID
- `span_id`: 当前请求 Span ID
- `timestamp`: ISO8601 格式时间戳
- `level`: 日志级别
- `caller`: 调用位置（文件名:行号）

### 3. HTTP 请求日志

自动记录每个请求的：
- 请求开始：method, path, peer_addr
- 请求完成：method, path, code, latency_ms, response_size

### 4. 多路输出支持

- **控制台输出**: 人可读格式，适合开发调试
- **文件输出**: JSON 格式，适合日志聚合系统
- **独立配置**: 每个输出可配置独立的格式、级别和轮转策略

### 5. 日志文件分片 (基于 Lumberjack)

- **按大小切割**: 达到指定大小自动切割
- **保留策略**: 可配置保留文件数量和天数
- **压缩支持**: 可选 gzip 压缩历史日志

## 快速开始

### 编译运行

```bash
cd nsp-demo

# 开发模式（彩色控制台输出）
go run ./cmd/server/main.go -dev

# 生产模式（JSON 格式输出到 stdout）
go run ./cmd/server/main.go

# 多路输出模式（控制台 + 文件）
go run ./cmd/server/main.go -log-file=/var/log/nsp-demo/app.log

# 指定端口
go run ./cmd/server/main.go -addr=:9090
```

### 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-addr` | `:8080` | HTTP 服务监听地址 |
| `-dev` | `false` | 开发模式（彩色输出） |
| `-log-file` | `""` | 日志文件路径（启用多路输出） |

### API 端点

| 端点 | 方法 | 描述 |
|------|------|------|
| `/health` | GET | 健康检查 |
| `/hello?name=xxx` | GET | Hello 接口 |
| `/user?id=xxx` | GET | 用户查询 |
| `/error` | GET | 模拟错误 |
| `/panic` | GET | 模拟 Panic |

### 测试请求

```bash
# 健康检查
curl http://localhost:8080/health

# 带参数请求
curl "http://localhost:8080/hello?name=World"

# 查看响应头中的追踪信息
curl -i http://localhost:8080/health

# 传入自定义 Trace ID
curl -H "X-Trace-ID: my-custom-trace-id" http://localhost:8080/health
```

## 日志输出示例

### 开发模式 (Console)

```
2026-02-26T17:57:03+0800  INFO  server/main.go:68     server starting      {"service":"nsp-demo","addr":":8097"}
2026-02-26T17:57:05+0800  INFO  middleware/logger.go:43  request started   {"service":"nsp-demo","trace_id":"1bad8708c82f1e9ddfea435e3df714a3","span_id":"40facefced8cb97d","method":"GET","path":"/health"}
2026-02-26T17:57:05+0800  INFO  handler/handler.go:28    health check      {"service":"nsp-demo","trace_id":"1bad8708c82f1e9ddfea435e3df714a3","span_id":"40facefced8cb97d"}
```

### 生产模式 (JSON)

```json
{"level":"info","timestamp":"2026-02-26T17:57:03.026+0800","caller":"server/main.go:68","message":"server starting","service":"nsp-demo","addr":":8097"}
{"level":"info","timestamp":"2026-02-26T17:57:05.033+0800","caller":"middleware/logger.go:43","message":"request started","service":"nsp-demo","trace_id":"1bad8708c82f1e9ddfea435e3df714a3","span_id":"40facefced8cb97d","method":"GET","path":"/health","peer_addr":"127.0.0.1:52488"}
{"level":"info","timestamp":"2026-02-26T17:57:05.033+0800","caller":"handler/handler.go:28","message":"health check","service":"nsp-demo","trace_id":"1bad8708c82f1e9ddfea435e3df714a3","span_id":"40facefced8cb97d"}
```

### 多路输出模式

启用 `-log-file` 参数后：
- **控制台**: Console 格式，方便实时查看
- **文件**: JSON 格式，方便 ELK/Loki 等日志系统采集

## 日志配置详解

### 基础配置 (DefaultConfig)

```go
cfg := logger.DefaultConfig("nsp-demo")
// Level: info
// Format: json
// OutputPaths: ["stdout"]
// EnableCaller: true
// Sampling: enabled (100 initial, 10 thereafter)
```

### 开发配置 (DevelopmentConfig)

```go
cfg := logger.DevelopmentConfig("nsp-demo")
// Level: debug
// Format: console (colorized)
// OutputPaths: ["stdout"]
// Sampling: disabled
```

### 多路输出配置 (MultiOutputConfig)

```go
cfg := logger.MultiOutputConfig("nsp-demo", "/var/log/app.log")
// Outputs:
//   - stdout: console format
//   - file: json format with rotation
```

### 自定义多路输出

```go
cfg := &logger.Config{
    Level:       logger.LevelInfo,
    ServiceName: "my-service",
    Outputs: []logger.OutputConfig{
        {
            Type:   logger.OutputTypeStdout,
            Format: logger.FormatConsole,
            Level:  logger.LevelDebug,  // 控制台显示所有级别
        },
        {
            Type:   logger.OutputTypeFile,
            Path:   "/var/log/app.log",
            Format: logger.FormatJSON,
            Level:  logger.LevelInfo,   // 文件只记录 Info 及以上
            Rotation: &logger.RotationConfig{
                MaxSize:    100,  // 100MB 切割
                MaxBackups: 7,    // 保留 7 个备份
                MaxAge:     30,   // 保留 30 天
                Compress:   true, // gzip 压缩
                LocalTime:  true, // 使用本地时间
            },
        },
        {
            Type:   logger.OutputTypeFile,
            Path:   "/var/log/error.log",
            Format: logger.FormatJSON,
            Level:  logger.LevelError,  // 错误单独记录
        },
    },
    EnableCaller:     true,
    EnableStackTrace: true,
}
```

## 核心代码说明

### 1. Trace 中间件

```go
// middleware/trace.go
func Trace(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()

        // 从请求头获取或生成 trace_id
        traceID := r.Header.Get(HeaderTraceID)
        if traceID == "" {
            traceID = GenerateTraceID()
        }
        ctx = logger.ContextWithTraceID(ctx, traceID)

        // 生成新的 span_id
        spanID := GenerateSpanID()
        ctx = logger.ContextWithSpanID(ctx, spanID)

        // 设置响应头
        w.Header().Set(HeaderTraceID, traceID)
        w.Header().Set(HeaderSpanID, spanID)

        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

### 2. 在 Handler 中使用日志

```go
// handler/handler.go
func User(w http.ResponseWriter, r *http.Request) {
    userID := r.URL.Query().Get("id")
    
    // 方式1: 使用全局函数，自动从 context 提取 trace_id/span_id
    logger.InfoContext(r.Context(), "processing request", "user_id", userID)
    
    // 方式2: 创建带有固定字段的子 logger
    log := logger.GetLogger().WithContext(r.Context()).With(
        logger.FieldUserID, userID,
        logger.FieldModule, "user-handler",
    )
    log.Info("fetching user from database")
    log.Info("user fetched successfully")
}
```

### 3. 中间件链配置

```go
// cmd/server/main.go
// 中间件顺序: Recovery -> Trace -> Logger -> Handler
var h http.Handler = mux
h = middleware.Logger(h)   // 记录请求日志
h = middleware.Trace(h)    // 注入追踪 ID
h = middleware.Recovery(h) // 捕获 panic
```

## 运行测试

```bash
cd nsp-demo
go test -v ./...
```

测试覆盖：
- Trace ID / Span ID 生成
- 中间件功能验证
- Handler 响应验证
- Panic 恢复验证

## 与上游服务集成

当调用下游服务时，传递 trace_id 以保持追踪链路：

```go
func callDownstream(ctx context.Context, url string) (*http.Response, error) {
    req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
    
    // 传递 trace_id 到下游服务
    if traceID := logger.TraceIDFromContext(ctx); traceID != "" {
        req.Header.Set("X-Trace-ID", traceID)
    }
    
    return http.DefaultClient.Do(req)
}
```

## 标准字段常量

`nsp-common/pkg/logger` 提供了标准字段常量，确保日志字段命名一致：

```go
logger.FieldService    // "service"
logger.FieldTraceID    // "trace_id"
logger.FieldSpanID     // "span_id"
logger.FieldUserID     // "user_id"
logger.FieldRequestID  // "request_id"
logger.FieldModule     // "module"
logger.FieldMethod     // "method"
logger.FieldPath       // "path"
logger.FieldCode       // "code"
logger.FieldLatencyMS  // "latency_ms"
logger.FieldError      // "error"
logger.FieldPeerAddr   // "peer_addr"
```

## 日志文件轮转配置

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `MaxSize` | 100 | 单个日志文件最大大小 (MB) |
| `MaxBackups` | 7 | 保留的历史文件数量 |
| `MaxAge` | 30 | 历史文件保留天数 |
| `Compress` | true | 是否 gzip 压缩历史文件 |
| `LocalTime` | true | 文件名使用本地时间 |

轮转后的文件命名格式：`app-2026-02-26T15-04-05.000.log.gz`

---

## AK/SK 认证模块使用指南

### 快速开始

#### 1. 服务端配置（Gin 中间件）

```go
package main

import (
    "github.com/gin-gonic/gin"
    "nsp-common/pkg/auth"
)

func main() {
    // 创建凭证存储
    store := auth.NewMemoryStore([]*auth.Credential{
        {
            AccessKey: "AK1234567890",
            SecretKey: "SK1234567890abcdef",
            Label:     "demo-client",
            Enabled:   true,
        },
    })
    
    // 创建 Nonce 存储
    nonces := auth.NewMemoryNonceStore()
    
    // 创建验证器
    verifier := auth.NewVerifier(store, nonces, nil)
    
    // 配置中间件
    r := gin.Default()
    
    // 全局认证（可配置 Skipper 跳过特定路径）
    r.Use(auth.AKSKAuthMiddleware(verifier, &auth.MiddlewareOption{
        Skipper: auth.NewSkipperByPath("/health", "/public/*"),
    }))
    
    // 获取认证凭证
    r.GET("/api/user", func(c *gin.Context) {
        cred, ok := auth.CredentialFromGin(c)
        if ok {
            // 使用 cred.AccessKey 进行权限控制
        }
    })
}
```

#### 2. 客户端签名（Go）

```go
import (
    "net/http"
    "nsp-common/pkg/auth"
)

func callAPI() error {
    signer := auth.NewSigner("AK1234567890", "SK1234567890abcdef")
    
    req, _ := http.NewRequest("POST", "https://api.example.com/user", body)
    req.Header.Set("Content-Type", "application/json")
    
    // 自动填充所有认证头并签名
    if err := signer.Sign(req); err != nil {
        return err
    }
    
    resp, err := http.DefaultClient.Do(req)
    // ...
}
```

#### 3. 客户端签名（Python 示例）

```python
import hmac
import hashlib
import time
import secrets

def sign_request(method, uri, query, headers, body, ak, sk):
    timestamp = str(int(time.time()))
    nonce = secrets.token_hex(16)
    
    # 构造 StringToSign
    canonical_headers = f"content-type:{headers.get('Content-Type', '')}\n"
    canonical_headers += f"x-nsp-nonce:{nonce}\n"
    canonical_headers += f"x-nsp-timestamp:{timestamp}\n"
    
    body_hash = hashlib.sha256(body.encode()).hexdigest()
    signed_headers = "content-type;x-nsp-nonce;x-nsp-timestamp"
    
    string_to_sign = f"{method.upper()}\n{uri}\n{query}\n{canonical_headers}\n{signed_headers}\n{body_hash}"
    
    signature = hmac.new(
        sk.encode(),
        string_to_sign.encode(),
        hashlib.sha256
    ).hexdigest()
    
    return {
        "Authorization": f"NSP-HMAC-SHA256 AK={ak}, Signature={signature}",
        "X-NSP-Timestamp": timestamp,
        "X-NSP-Nonce": nonce,
        "X-NSP-SignedHeaders": signed_headers,
    }
```

### 请求头规范

| Header | 说明 | 示例 |
|--------|------|------|
| `Authorization` | 认证头 | `NSP-HMAC-SHA256 AK=xxx, Signature=xxx` |
| `X-NSP-Timestamp` | Unix 秒级时间戳 | `1709049600` |
| `X-NSP-Nonce` | 16字节随机 hex | `a1b2c3d4e5f67890` |
| `X-NSP-SignedHeaders` | 参与签名的请求头 | `content-type;x-nsp-nonce;x-nsp-timestamp` |

### 签名字符串构造（StringToSign）

```
POST
/api/v1/users
page=1&size=10
content-type:application/json
x-nsp-nonce:a1b2c3d4e5f67890
x-nsp-timestamp:1709049600

content-type;x-nsp-nonce;x-nsp-timestamp
<hex(SHA256(body))>
```

### 错误码映射

| 错误 | HTTP 状态码 | 说明 |
|------|-------------|------|
| `ErrMissingAuthHeader` | 400 | Authorization 头缺失 |
| `ErrInvalidAuthFormat` | 400 | Authorization 格式错误 |
| `ErrMissingTimestamp` | 400 | 时间戳缺失或格式错误 |
| `ErrMissingNonce` | 400 | Nonce 缺失 |
| `ErrTimestampExpired` | 401 | 时间戳超出容忍窗口 |
| `ErrNonceReused` | 401 | Nonce 已被使用 |
| `ErrAKNotFound` | 401 | AK 不存在或已禁用 |
| `ErrSignatureMismatch` | 401 | 签名不匹配 |

### 高级配置

#### 自定义时间戳容忍窗口

```go
cfg := &auth.VerifierConfig{
    TimestampTolerance: 10 * time.Minute,  // 10分钟容忍
    NonceTTL:           30 * time.Minute,  // Nonce 30分钟有效期
}
verifier := auth.NewVerifier(store, nonces, cfg)
```

#### 自定义认证失败响应

```go
r.Use(auth.AKSKAuthMiddleware(verifier, &auth.MiddlewareOption{
    OnAuthFailed: func(c *gin.Context, err error) {
        c.JSON(401, gin.H{
            "code": "AUTH_FAILED",
            "message": err.Error(),
        })
        c.Abort()
    },
}))
```

#### 路径前缀豁免

```go
// 跳过 /public/ 和 /health 路径
r.Use(auth.AKSKAuthMiddleware(verifier, &auth.MiddlewareOption{
    Skipper: auth.NewSkipperByPathPrefix("/public/", "/health"),
}))
```

### 生产环境建议

1. **凭证存储**: 使用数据库或 Redis 实现 `CredentialStore` 接口
2. **Nonce 存储**: 使用 Redis 实现 `NonceStore` 接口，支持分布式部署
3. **密钥轮换**: 定期更新 SecretKey，支持新旧密钥同时生效的过渡期
4. **日志审计**: 记录所有认证失败请求，用于安全分析
