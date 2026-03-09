# 背景

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
