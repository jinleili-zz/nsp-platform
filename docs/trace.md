# Trace 模块

> 包路径：`github.com/paic/nsp-common/pkg/trace`

## 功能说明

Trace 模块解决以下问题：

- **全链路追踪**：跨微服务请求的 TraceID 透传，串联完整调用链
- **调用树还原**：通过 SpanID / ParentSpanID 关系构建服务调用树
- **日志关联**：自动将 trace_id、span_id 注入 Context，日志自动携带
- **兼容 B3 标准**：采用 B3 Multi Header 命名规范，便于与其他系统互操作
- **实例定位**：记录处理请求的 Pod 实例，快速定位问题节点

---

## 核心概念

| 概念 | 格式 | 说明 |
|------|------|------|
| `TraceID` | 32 位 hex（16 字节） | 全链路唯一标识，一次请求从入口到结束保持不变 |
| `SpanID` | 16 位 hex（8 字节） | 每个服务独立生成，标识当前服务的一次处理 |
| `ParentSpanID` | 16 位 hex | 上游服务的 SpanID，用于构建调用树（root span 时为空） |
| `InstanceID` | 字符串 | 当前服务实例标识，通常为 K8s Pod 名称 |
| `Sampled` | boolean | 是否采样（预留，当前默认全量采样） |

---

## 核心接口

```go
// TraceContext 链路追踪上下文
type TraceContext struct {
    TraceID      string   // 全链路唯一标识
    SpanId       string   // 当前服务的 SpanID
    ParentSpanId string   // 上游服务的 SpanID（root span 时为空）
    InstanceId   string   // 当前服务实例标识
    Sampled      bool     // 是否采样
}

// Context 操作
func ContextWithTrace(ctx context.Context, tc *TraceContext) context.Context
func TraceFromContext(ctx context.Context) (*TraceContext, bool)
func MustTraceFromContext(ctx context.Context) *TraceContext  // 不存在返回空结构体

// ID 生成
func NewTraceID() string      // 生成 32 位 hex TraceID
func NewSpanId() string       // 生成 16 位 hex SpanID
func GetInstanceId() string   // 获取实例 ID（HOSTNAME 环境变量）

// HTTP 传播
func Extract(r *http.Request, instanceId string) *TraceContext
func Inject(req *http.Request, tc *TraceContext)
func InjectResponse(w http.ResponseWriter, tc *TraceContext)

// Gin 中间件
func TraceMiddleware(instanceId string) gin.HandlerFunc
func TraceFromGin(c *gin.Context) (*TraceContext, bool)

// HTTP 客户端
func NewTracedClient(inner *http.Client) *TracedClient
```

---

## HTTP 请求头规范

| Header | 方向 | 说明 |
|--------|------|------|
| `X-B3-TraceId` | 入站/出站 | TraceID（32 位 hex） |
| `X-B3-SpanId` | 入站/出站 | 发送方的 SpanID（接收方存为 ParentSpanID） |
| `X-B3-Sampled` | 入站/出站 | 采样标志（"1" 或 "0"） |
| `X-Request-Id` | 入站/响应 | 兼容网关，等于 TraceID |

**注意**：不透传 `X-B3-ParentSpanId`，采用现代独立 Span 模型。

---

## 快速使用

### 服务端中间件配置

```go
package main

import (
    "github.com/gin-gonic/gin"
    "github.com/paic/nsp-common/pkg/logger"
    "github.com/paic/nsp-common/pkg/trace"
)

func main() {
    // 初始化日志
    logger.Init(logger.DefaultConfig("order-service"))
    defer logger.Sync()

    // 获取实例 ID（启动时调用一次）
    instanceID := trace.GetInstanceId()

    router := gin.New()

    // 注册 Trace 中间件（必须在 Logger 中间件之前）
    router.Use(trace.TraceMiddleware(instanceID))

    router.GET("/api/orders", func(c *gin.Context) {
        ctx := c.Request.Context()

        // 方式一：从标准 context 获取
        tc, ok := trace.TraceFromContext(ctx)
        if ok {
            logger.InfoContext(ctx, "处理请求",
                "trace_id", tc.TraceID,
                "span_id", tc.SpanId,
            )
        }

        // 方式二：从 gin.Context 获取
        tc2, _ := trace.TraceFromGin(c)
        logger.InfoContext(ctx, "追踪信息",
            "is_root", tc2.IsRoot(),
            "parent_span_id", tc2.ParentSpanId,
        )

        c.JSON(200, gin.H{"status": "ok"})
    })

    router.Run(":8080")
}
```

### 出站请求自动注入

```go
package main

import (
    "context"
    "fmt"
    "io"

    "github.com/paic/nsp-common/pkg/trace"
)

func callDownstream(ctx context.Context) error {
    // 创建带追踪的 HTTP 客户端
    client := trace.NewTracedClient(nil)  // nil 使用默认配置（30s 超时）

    // GET 请求（自动注入 X-B3-TraceId, X-B3-SpanId, X-B3-Sampled）
    resp, err := client.Get(ctx, "http://stock-service/api/v1/stock")
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    body, _ := io.ReadAll(resp.Body)
    fmt.Printf("响应: %s\n", body)

    return nil
}
```

### 手动注入追踪信息

```go
import (
    "net/http"

    "github.com/paic/nsp-common/pkg/trace"
)

func manualInject(ctx context.Context) {
    tc, ok := trace.TraceFromContext(ctx)
    if !ok {
        return
    }

    req, _ := http.NewRequest("GET", "http://example.com/api", nil)

    // 手动注入追踪头
    trace.Inject(req, tc)

    // 此时 req.Header 包含：
    // X-B3-TraceId: <tc.TraceID>
    // X-B3-SpanId:  <tc.SpanId>
    // X-B3-Sampled: 1
}
```

---

## 调用链传播示例

```
gateway → order-service → stock-service

┌─────────────────────────────────────────────────────────────────────┐
│ gateway（入口节点）                                                  │
│   入站请求头：无                                                     │
│   TraceID      = T1（新生成）                                       │
│   SpanID       = S1（新生成）                                       │
│   ParentSpanID = ""（root span）                                    │
│   出站请求头：X-B3-TraceId=T1, X-B3-SpanId=S1                       │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│ order-service（中间节点）                                            │
│   入站请求头：X-B3-TraceId=T1, X-B3-SpanId=S1                       │
│   TraceID      = T1（继承）                                         │
│   SpanID       = S2（新生成）                                       │
│   ParentSpanID = S1（来自入站 X-B3-SpanId）                         │
│   出站请求头：X-B3-TraceId=T1, X-B3-SpanId=S2                       │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│ stock-service（末端节点）                                            │
│   入站请求头：X-B3-TraceId=T1, X-B3-SpanId=S2                       │
│   TraceID      = T1（继承）                                         │
│   SpanID       = S3（新生成）                                       │
│   ParentSpanID = S2（来自入站 X-B3-SpanId）                         │
└─────────────────────────────────────────────────────────────────────┘

日志还原调用树（WHERE trace_id = T1 ORDER BY timestamp）：
  S1 (parent="")  → gateway   [root]
  S2 (parent=S1)  → order     [gateway 的子节点]
  S3 (parent=S2)  → stock     [order 的子节点]
```

---

## 与 Logger 集成

```go
// TraceContext.LogFields() 返回结构化字段
tc := trace.MustTraceFromContext(ctx)
fields := tc.LogFields()
// {
//   "trace_id":      "4bf92f3577b34da6a3ce929d0e0e4736",
//   "span_id":       "00f067aa0ba902b7",
//   "parent_span_id":"a3f2b1c4d5e6f7a8",  // 有上游时才有
//   "instance_id":   "order-pod-7d9f2b"
// }

// 推荐：使用 logger.InfoContext，自动提取 trace_id/span_id
logger.InfoContext(ctx, "处理订单", "order_id", "ORD-001")
// 输出：{"trace_id":"...","span_id":"...","msg":"处理订单","order_id":"ORD-001"}
```

---

## 注意事项

### 中间件顺序

```go
// 正确顺序：Trace 在 Logger 之前
router.Use(trace.TraceMiddleware(instanceID))  // 1. 先注入 trace_id
router.Use(loggerMiddleware())                  // 2. 后记录日志

// 错误顺序：日志中无 trace_id
router.Use(loggerMiddleware())                  // 日志时 trace_id 还未注入
router.Use(trace.TraceMiddleware(instanceID))
```

### TraceID 格式校验

Extract 会校验 TraceID 和 SpanID 的格式（必须是有效 hex 字符串）：
- TraceID：32 位 hex
- SpanID：16 位 hex

格式不符时会生成新 ID，保证格式一致性。

---

## 请求头常量

```go
const (
    HeaderTraceID   = "X-B3-TraceId"    // TraceID
    HeaderSpanId    = "X-B3-SpanId"     // SpanID
    HeaderSampled   = "X-B3-Sampled"    // 采样标志
    HeaderRequestID = "X-Request-Id"    // 兼容网关
)
```
