# NSP Platform 技术文档

## 项目概述

NSP (Network Service Platform) 是一个基于 Go 语言构建的微服务平台，提供了一套完整的公共基础设施组件。项目采用模块化设计，主要包含以下核心模块：

- **auth** - AK/SK 认证模块
- **trace** - 分布式链路追踪模块
- **saga** - SAGA 分布式事务模块
- **logger** - 统一日志模块
- **taskqueue** - 任务队列编排模块

## 目录结构

```
nsp_platform/
├── nsp-common/                 # 公共基础库
│   └── pkg/
│       ├── auth/               # AK/SK 认证
│       ├── trace/              # 分布式链路追踪
│       ├── saga/               # SAGA 分布式事务
│       ├── logger/             # 统一日志
│       └── taskqueue/          # 任务队列编排
└── nsp-demo/                   # 示例服务
    ├── cmd/server/             # HTTP 服务入口
    └── internal/               # 内部实现
```

---

## 架构总览

### 系统架构图

```
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                              NSP Platform 架构总览                                       │
└─────────────────────────────────────────────────────────────────────────────────────────┘

                                    ┌─────────────┐
                                    │   Client    │
                                    │  (HTTP/S)   │
                                    └──────┬──────┘
                                           │
                              X-B3-TraceId │ Authorization
                              X-B3-SpanId  │ X-NSP-Timestamp
                                           ▼
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                                   nsp-demo (示例服务)                                    │
│  ┌────────────────────────────────────────────────────────────────────────────────────┐ │
│  │                              Gin HTTP Server                                       │ │
│  │  ┌──────────────────────────────────────────────────────────────────────────────┐  │ │
│  │  │                           Middleware Chain                                   │  │ │
│  │  │                                                                              │  │ │
│  │  │   ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────────────────────┐  │  │ │
│  │  │   │ Recovery │ → │  Trace   │ → │  Logger  │ → │     AK/SK Auth           │  │  │ │
│  │  │   │          │   │Middleware│   │Middleware│   │     Middleware           │  │  │ │
│  │  │   └──────────┘   └────┬─────┘   └────┬─────┘   └───────────┬──────────────┘  │  │ │
│  │  │                       │              │                     │                 │  │ │
│  │  └───────────────────────┼──────────────┼─────────────────────┼─────────────────┘  │ │
│  │                          │              │                     │                    │ │
│  │                          ▼              ▼                     ▼                    │ │
│  │                    ┌──────────┐   ┌──────────┐          ┌──────────┐               │ │
│  │                    │ context  │   │ context  │          │ context  │               │ │
│  │                    │ TraceCtx │   │ TraceID  │          │Credential│               │ │
│  │                    │          │   │ SpanID   │          │          │               │ │
│  │                    └──────────┘   └──────────┘          └──────────┘               │ │
│  │                          │              │                     │                    │ │
│  │                          └──────────────┴─────────────────────┘                    │ │
│  │                                         │                                          │ │
│  │                                         ▼                                          │ │
│  │                               ┌──────────────────┐                                 │ │
│  │                               │     Handlers     │                                 │ │
│  │                               └──────────────────┘                                 │ │
│  └────────────────────────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────────────────┘
                                           │
                                           │ 依赖
                                           ▼
┌─────────────────────────────────────────────────────────────────────────────────────────┐
│                                nsp-common (公共基础库)                                   │
│                                                                                         │
│  ┌─────────────────────┐  ┌─────────────────────┐  ┌─────────────────────────────────┐  │
│  │      auth           │  │       trace         │  │           logger                │  │
│  │  ┌───────────────┐  │  │  ┌───────────────┐  │  │  ┌─────────────────────────┐    │  │
│  │  │ CredentialStore│  │  │  │ TraceContext  │  │  │  │      ZapLogger          │    │  │
│  │  │  ├─MemoryStore│  │  │  │  ├─ TraceID    │  │  │  │  ├─ Debug/Info/Warn/Err│    │  │
│  │  │  └─(扩展Redis) │  │  │  │  ├─ SpanId    │  │  │  │  ├─ DebugContext/...   │    │  │
│  │  ├───────────────┤  │  │  │  ├─ ParentSpan │  │  │  │  └─ With/WithGroup     │    │  │
│  │  │  NonceStore   │  │  │  │  └─ InstanceId │  │  │  ├─────────────────────────┤    │  │
│  │  │  ├─MemoryNonce│  │  │  ├───────────────┤  │  │  │     WriterAdapter       │    │  │
│  │  │  └─(扩展Redis) │  │  │  │  Propagator   │  │  │  │  ├─ io.Writer 适配     │    │  │
│  │  ├───────────────┤  │  │  │  ├─ Extract    │  │  │  │  ├─ WithLevel          │    │  │
│  │  │    Signer     │  │  │  │  ├─ Inject     │  │  │  │  ├─ WithPrefix         │    │  │
│  │  │  (客户端签名)  │  │  │  │  └─ InjectResp│  │  │  │  └─ WithContext        │    │  │
│  │  ├───────────────┤  │  │  ├───────────────┤  │  │  ├─────────────────────────┤    │  │
│  │  │   Verifier    │  │  │  │ TracedClient  │  │  │  │       Config            │    │  │
│  │  │  (服务端验证)  │  │  │  │  ├─ Do        │  │  │  │  ├─ Level/Format       │    │  │
│  │  ├───────────────┤  │  │  │  ├─ Get       │  │  │  │  ├─ Rotation           │    │  │
│  │  │  Middleware   │  │  │  │  └─ Post      │  │  │  │  └─ Sampling           │    │  │
│  │  │  (Gin适配)    │  │  │  ├───────────────┤  │  │  └─────────────────────────┘    │  │
│  │  └───────────────┘  │  │  │  Middleware   │  │  └─────────────────────────────────┘  │
│  │                     │  │  │  (Gin适配)    │  │                                       │
│  │  HMAC-SHA256 签名   │  │  └───────────────┘  │           Zap + Lumberjack            │
│  │  ±5min 时间戳窗口   │  │                     │           结构化日志                   │
│  │  Nonce 防重放       │  │   B3 Multi Header   │           日志轮转                     │
│  └─────────────────────┘  │   全链路透传        │                                       │
│                           └─────────────────────┘                                       │
│                                                                                         │
│  ┌──────────────────────────────────────────┐  ┌──────────────────────────────────────┐ │
│  │                 saga                      │  │              taskqueue               │ │
│  │  ┌──────────────────────────────────┐    │  │  ┌──────────────────────────────┐    │ │
│  │  │           Engine                 │    │  │  │           Engine             │    │ │
│  │  │  ├─ Submit(ctx, def) → txID      │    │  │  │  ├─ SubmitWorkflow(def)      │    │ │
│  │  │  ├─ Query(ctx, txID) → status    │    │  │  │  ├─ HandleCallback(cb)       │    │ │
│  │  │  ├─ Start(ctx) / Stop()          │    │  │  │  ├─ QueryWorkflow(id)        │    │ │
│  │  │  └─ trace 自动注入               │    │  │  │  └─ RetryStep(stepID)        │    │ │
│  │  ├──────────────────────────────────┤    │  │  ├──────────────────────────────┤    │ │
│  │  │         Coordinator              │    │  │  │         Broker               │    │ │
│  │  │  ├─ 状态机驱动                   │    │  │  │  ├─ Publish(task)            │    │ │
│  │  │  ├─ 崩溃恢复                     │    │  │  │  └─ (asynq/kafka/...)        │    │ │
│  │  │  └─ 超时扫描                     │    │  │  ├──────────────────────────────┤    │ │
│  │  ├──────────────────────────────────┤    │  │  │      CallbackSender          │    │ │
│  │  │          Executor                │    │  │  │  ├─ Success(taskID, result)  │    │ │
│  │  │  ├─ ExecuteStep (同步)           │    │  │  │  └─ Fail(taskID, error)      │    │ │
│  │  │  ├─ ExecuteAsyncStep (异步)      │    │  │  ├──────────────────────────────┤    │ │
│  │  │  ├─ CompensateStep (补偿)        │    │  │  │      QueueRouter             │    │ │
│  │  │  └─ Poll (轮询)                  │    │  │  │  tasks_{tag}_{priority}      │    │ │
│  │  ├──────────────────────────────────┤    │  │  └──────────────────────────────┘    │ │
│  │  │           Poller                 │    │  │                                      │ │
│  │  │  ├─ 异步步骤状态轮询             │    │  │  优先级: Low(1) Normal(3)            │ │
│  │  │  └─ JSONPath 结果匹配            │    │  │          High(6) Critical(9)         │ │
│  │  ├──────────────────────────────────┤    │  │                                      │ │
│  │  │         Template                 │    │  │  状态流转:                            │ │
│  │  │  {action_response.field}         │    │  │  pending → queued → running          │ │
│  │  │  {step[0].action_response.x}     │    │  │      ↓                                │ │
│  │  │  {transaction.payload.field}     │    │  │  completed / failed                   │ │
│  │  └──────────────────────────────────┘    │  └──────────────────────────────────────┘ │
│  │                                          │                                           │
│  │  状态流转:                                │                                           │
│  │  pending → running → succeeded           │                                           │
│  │      ↓         ↓                         │                                           │
│  │  (失败) → compensating → failed          │                                           │
│  └──────────────────────────────────────────┘                                           │
└─────────────────────────────────────────────────────────────────────────────────────────┘
           │                │                              │               │
           │                │                              │               │
           ▼                ▼                              ▼               ▼
┌─────────────────────────────────────────────┐  ┌─────────────────────────────────────────┐
│              PostgreSQL                      │  │           Message Queue                 │
│  ┌────────────────┐  ┌────────────────────┐  │  │  ┌─────────────────────────────────┐    │
│  │saga_transactions│  │ taskqueue_workflows│  │  │  │  Asynq (Redis) / Kafka / ...   │    │
│  ├────────────────┤  ├────────────────────┤  │  │  │                                 │    │
│  │  saga_steps    │  │ taskqueue_steps    │  │  │  │  ┌───────┐  ┌───────┐  ┌─────┐ │    │
│  ├────────────────┤  └────────────────────┘  │  │  │  │tasks  │  │tasks_ │  │task_│ │    │
│  │saga_poll_tasks │                          │  │  │  │       │  │high   │  │cb   │ │    │
│  └────────────────┘                          │  │  │  └───────┘  └───────┘  └─────┘ │    │
└─────────────────────────────────────────────┘  │  └─────────────────────────────────┘    │
                                                 └─────────────────────────────────────────┘
```

### 模块依赖关系

```
                        ┌────────────────────────┐
                        │        logger          │
                        │   (基础日志服务)        │
                        └───────────┬────────────┘
                                    │
                 ┌──────────────────┼──────────────────┐
                 │                  │                  │
                 ▼                  ▼                  ▼
          ┌────────────┐     ┌────────────┐     ┌────────────┐
          │   trace    │     │    auth    │     │    saga    │
          │ (链路追踪)  │     │  (认证)    │     │ (分布式事务)│
          └──────┬─────┘     └────────────┘     └──────┬─────┘
                 │                                     │
                 │           ┌─────────────────────────┘
                 │           │
                 ▼           ▼
          ┌─────────────────────────┐
          │   saga (Executor)        │
          │   trace.Inject() 注入    │
          │   X-B3-TraceId/SpanId   │
          └─────────────────────────┘
```

### 典型请求处理流程

```
Client Request
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│ 1. TraceMiddleware                                                              │
│    Extract(req) → TraceContext{TraceID, SpanId, ParentSpanId}                   │
│    ctx = ContextWithTrace(ctx, tc)                                              │
│    InjectResponse(w, tc)  → X-B3-TraceId, X-Request-Id                          │
└─────────────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│ 2. LoggerMiddleware                                                             │
│    logger.InfoContext(ctx, "request", "method", method, "path", path)           │
│    自动附加 trace_id, span_id                                                    │
└─────────────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│ 3. AKSKAuthMiddleware                                                           │
│    verifier.Verify(req) → 检查签名/时间戳/Nonce                                  │
│    ctx = ContextWithCredential(ctx, cred)                                       │
└─────────────────────────────────────────────────────────────────────────────────┘
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│ 4. Handler                                                                      │
│    cred, _ := auth.CredentialFromGin(c)                                         │
│    tc, _ := trace.TraceFromGin(c)                                               │
│                                                                                 │
│    // 提交 SAGA 事务 (自动传播 trace)                                            │
│    engine.Submit(ctx, sagaDef)                                                  │
│                                                                                 │
│    // 调用下游服务 (自动注入 trace headers)                                      │
│    tracedClient.Post(ctx, url, body)                                            │
└─────────────────────────────────────────────────────────────────────────────────┘
     │
     ▼
Response with X-B3-TraceId, X-Request-Id
```

### SAGA 事务执行流程

```
engine.Submit(ctx, def)
     │
     ├──→ 提取 TraceContext, 存入 payload._trace_id / _span_id
     │
     ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           Coordinator                                           │
│                                                                                 │
│   ┌─────────┐     ┌─────────┐     ┌─────────┐                                   │
│   │ Step 1  │ ──→ │ Step 2  │ ──→ │ Step 3  │ ──→ succeeded                     │
│   │ (sync)  │     │ (async) │     │ (sync)  │                                   │
│   └────┬────┘     └────┬────┘     └─────────┘                                   │
│        │               │                                                        │
│        ▼               ▼                                                        │
│   ┌─────────┐     ┌─────────┐                                                   │
│   │Executor │     │ Poller  │                                                   │
│   │ExecuteStep│   │  Poll   │                                                   │
│   └────┬────┘     └────┬────┘                                                   │
│        │               │                                                        │
│        ▼               ▼                                                        │
│   HTTP Request    HTTP GET                                                      │
│   + X-B3-TraceId  + X-B3-TraceId                                                │
│   + X-B3-SpanId   + X-B3-SpanId                                                 │
│   + X-Saga-Transaction-Id                                                       │
│   + X-Idempotency-Key                                                           │
│                                                                                 │
│   ════════════════════════════════════════════════════════════════════════════  │
│   失败时触发补偿:                                                                │
│                                                                                 │
│   Step 3 failed ──→ compensating ──→ CompensateStep(2) ──→ CompensateStep(1)    │
│                                              │                    │             │
│                                              ▼                    ▼             │
│                                         HTTP POST            HTTP POST          │
│                                         (补偿URL)            (补偿URL)          │
│                                              │                    │             │
│                                              └────────┬───────────┘             │
│                                                       ▼                         │
│                                                    failed                       │
└─────────────────────────────────────────────────────────────────────────────────┘
```

### 图例说明

| 符号 | 含义 |
|-----|------|
| `→` | 数据/控制流向 |
| `▼` | 调用/依赖方向 |
| `├─` | 包含的子组件 |
| `───` | 连接线 |

### 核心模块职责

| 模块 | 职责 |
|-----|------|
| **auth** | AK/SK 签名认证、防重放 |
| **trace** | B3 格式链路追踪、跨服务透传 |
| **logger** | 结构化日志、trace 关联、第三方框架适配 |
| **saga** | 分布式事务编排、补偿回滚、异步轮询 |
| **taskqueue** | 工作流编排、优先级队列、回调驱动 |

---

## 模块一：AK/SK 认证模块 (auth)

### 功能概述

提供基于 HMAC-SHA256 的 AK/SK 认证机制，支持：
- 请求签名与验证
- 时间戳防重放 (±5分钟容忍窗口)
- Nonce 一次性校验
- Gin 中间件集成

### 核心数据结构

#### Credential - 凭证结构体
```go
type Credential struct {
    AccessKey string  // AK，公开标识
    SecretKey string  // SK，私钥，永不传输
    Label     string  // 描述标签
    Enabled   bool    // 是否启用
}
```

### API 列表

#### 凭证存储 (store.go)

| 接口/函数 | 说明 |
|----------|------|
| `CredentialStore` | 凭证存储接口 |
| `CredentialStore.GetByAK(ctx, ak) (*Credential, error)` | 根据 AK 获取凭证 |
| `NewMemoryStore(creds []*Credential) *MemoryStore` | 创建内存凭证存储 |
| `MemoryStore.Add(cred *Credential)` | 运行时添加凭证 |

#### Nonce 防重放 (nonce.go)

| 接口/函数 | 说明 |
|----------|------|
| `NonceStore` | Nonce 存储接口 |
| `NonceStore.CheckAndStore(ctx, nonce, ttl) (used bool, err error)` | 检查并存储 Nonce |
| `NewMemoryNonceStore() *MemoryNonceStore` | 创建内存 Nonce 存储 |
| `MemoryNonceStore.Stop()` | 停止清理 goroutine |

#### 签名与验证 (aksk.go)

| 函数/类型 | 说明 |
|----------|------|
| `NewSigner(ak, sk string) *Signer` | 创建客户端签名器 |
| `Signer.Sign(req *http.Request) error` | 对请求进行签名 |
| `NewVerifier(store, nonces, cfg) *Verifier` | 创建服务端验证器 |
| `Verifier.Verify(req *http.Request) (*Credential, error)` | 验证请求签名 |
| `ErrorToHTTPStatus(err error) int` | 错误转 HTTP 状态码 |

#### 错误定义

| 错误常量 | HTTP 状态码 | 说明 |
|---------|------------|------|
| `ErrMissingAuthHeader` | 400 | Authorization 头缺失 |
| `ErrInvalidAuthFormat` | 400 | Authorization 格式错误 |
| `ErrMissingTimestamp` | 400 | 时间戳缺失或格式错误 |
| `ErrMissingNonce` | 400 | Nonce 缺失 |
| `ErrTimestampExpired` | 401 | 时间戳过期 |
| `ErrNonceReused` | 401 | Nonce 重放 |
| `ErrAKNotFound` | 401 | AK 不存在或禁用 |
| `ErrSignatureMismatch` | 401 | 签名不匹配 |

#### Gin 中间件 (middleware.go)

| 函数 | 说明 |
|-----|------|
| `AKSKAuthMiddleware(verifier, opt) gin.HandlerFunc` | 创建认证中间件 |
| `CredentialFromGin(c *gin.Context) (*Credential, bool)` | 从 Gin Context 获取凭证 |
| `ContextWithCredential(ctx, cred) context.Context` | 凭证写入 Context |
| `CredentialFromContext(ctx) (*Credential, bool)` | 从 Context 获取凭证 |
| `NewSkipperByPath(paths ...string) func(*gin.Context) bool` | 创建路径跳过器 |
| `NewSkipperByPathPrefix(prefixes ...string) func(*gin.Context) bool` | 创建前缀跳过器 |

### HTTP 请求头规范

| 请求头 | 说明 |
|-------|------|
| `Authorization` | `NSP-HMAC-SHA256 AK=<ak>, Signature=<signature>` |
| `X-NSP-Timestamp` | Unix 秒级时间戳 |
| `X-NSP-Nonce` | 16 字节随机 hex 字符串 |
| `X-NSP-SignedHeaders` | 参与签名的请求头列表 |

### 签名算法

StringToSign 构造：
```
Line 1: HTTP Method (大写)
Line 2: Canonical URI (Path)
Line 3: Canonical Query String (排序)
Line 4: Canonical Headers
Line 5: SignedHeaders
Line 6: hex(SHA256(body))
```

签名计算：`signature = hex(HMAC-SHA256(SK, StringToSign))`

---

## 模块二：分布式链路追踪模块 (trace)

### 功能概述

实现 B3 标准的分布式链路追踪，支持：
- TraceID/SpanID 生成与传播
- 服务间调用链追踪
- 与日志模块集成
- Gin 中间件集成

### 核心数据结构

#### TraceContext - 追踪上下文
```go
type TraceContext struct {
    TraceID      string  // 全链路唯一标识 (32位hex)
    SpanId       string  // 当前服务处理标识 (16位hex)
    ParentSpanId string  // 上游 SpanId
    InstanceId   string  // 实例标识 (k8s Pod 名称)
    Sampled      bool    // 是否采样
}
```

### API 列表

#### Context 集成 (context.go)

| 函数 | 说明 |
|-----|------|
| `ContextWithTrace(ctx, tc) context.Context` | 注入 TraceContext |
| `TraceFromContext(ctx) (*TraceContext, bool)` | 从 Context 提取 |
| `MustTraceFromContext(ctx) *TraceContext` | 强制提取，不存在返回空结构体 |
| `TraceContext.IsRoot() bool` | 判断是否为根 Span |
| `TraceContext.LogFields() map[string]string` | 获取日志字段 |

#### ID 生成 (generator.go)

| 函数 | 说明 |
|-----|------|
| `NewTraceID() string` | 生成 32 位 TraceID |
| `NewSpanId() string` | 生成 16 位 SpanId |
| `GetInstanceId() string` | 获取实例标识 |

#### HTTP 传播 (propagator.go)

| 函数 | 说明 |
|-----|------|
| `Extract(r *http.Request, instanceId string) *TraceContext` | 从请求提取追踪信息 |
| `Inject(req *http.Request, tc *TraceContext)` | 向请求注入追踪信息 |
| `InjectResponse(w http.ResponseWriter, tc *TraceContext)` | 向响应注入追踪信息 |

#### HTTP 请求头常量

| 常量 | 值 | 说明 |
|-----|---|------|
| `HeaderTraceID` | `X-B3-TraceId` | TraceID 头 |
| `HeaderSpanId` | `X-B3-SpanId` | SpanId 头 |
| `HeaderSampled` | `X-B3-Sampled` | 采样标志头 |
| `HeaderRequestID` | `X-Request-Id` | 兼容头 |

#### Gin 中间件 (middleware.go)

| 函数 | 说明 |
|-----|------|
| `TraceMiddleware(instanceId string) gin.HandlerFunc` | 创建追踪中间件 |
| `TraceFromGin(c *gin.Context) (*TraceContext, bool)` | 从 Gin Context 获取 |

#### HTTP 客户端 (client.go)

| 类型/函数 | 说明 |
|----------|------|
| `TracedClient` | 带追踪能力的 HTTP 客户端 |
| `NewTracedClient(inner *http.Client) *TracedClient` | 创建客户端 |
| `TracedClient.Do(req) (*http.Response, error)` | 发送请求 (自动注入追踪头) |
| `TracedClient.Get(ctx, url) (*http.Response, error)` | GET 请求 |
| `TracedClient.Post(ctx, url, contentType, body) (*http.Response, error)` | POST 请求 |

### 调用链传播示例

```
gateway (入口) → order (中间) → stock (末端)

gateway:
  TraceID = T1 (新生成)
  SpanId = S1 (新生成)
  ParentSpanId = ""
  出站头: X-B3-TraceId=T1, X-B3-SpanId=S1

order:
  TraceID = T1 (继承)
  SpanId = S2 (新生成)
  ParentSpanId = S1 (来自入站 X-B3-SpanId)
  出站头: X-B3-TraceId=T1, X-B3-SpanId=S2

stock:
  TraceID = T1 (继承)
  SpanId = S3 (新生成)
  ParentSpanId = S2 (来自入站 X-B3-SpanId)
```

---

## 模块三：SAGA 分布式事务模块 (saga)

### 功能概述

实现 SAGA 模式的分布式事务，支持：
- 同步/异步步骤执行
- 基于补偿的回滚
- 异步步骤轮询
- 崩溃恢复
- 超时处理
- **分布式链路追踪集成**

### 核心数据结构

#### 状态枚举

**事务状态 (TxStatus)**
| 状态 | 说明 |
|-----|------|
| `pending` | 待执行 |
| `running` | 执行中 |
| `compensating` | 补偿中 |
| `succeeded` | 成功 |
| `failed` | 失败 |

**步骤状态 (StepStatus)**
| 状态 | 说明 |
|-----|------|
| `pending` | 待执行 |
| `running` | 执行中 |
| `polling` | 轮询中 (异步步骤) |
| `succeeded` | 成功 |
| `failed` | 失败 |
| `compensating` | 补偿中 |
| `compensated` | 已补偿 |
| `skipped` | 已跳过 |

**步骤类型 (StepType)**
| 类型 | 说明 |
|-----|------|
| `sync` | 同步步骤 |
| `async` | 异步步骤 (需轮询) |

### API 列表

#### 引擎入口 (engine.go)

| 类型/函数 | 说明 |
|----------|------|
| `Config` | 引擎配置 |
| `NewEngine(cfg *Config) (*Engine, error)` | 创建引擎 |
| `Engine.Start(ctx) error` | 启动后台任务 |
| `Engine.Stop() error` | 停止引擎 |
| `Engine.Submit(ctx, def) (string, error)` | 提交事务 |
| `Engine.Query(ctx, txID) (*TransactionStatus, error)` | 查询状态 |

#### 配置参数

```go
type Config struct {
    DSN               string        // PostgreSQL 连接串
    WorkerCount       int           // 协调器 Worker 数 (默认 4)
    PollBatchSize     int           // 轮询批次大小 (默认 20)
    PollScanInterval  time.Duration // 轮询扫描间隔 (默认 3s)
    CoordScanInterval time.Duration // 协调器扫描间隔 (默认 5s)
    HTTPTimeout       time.Duration // HTTP 超时 (默认 30s)
    InstanceID        string        // 实例标识 (自动生成)
}
```

#### 事务定义 (definition.go)

| 类型/函数 | 说明 |
|----------|------|
| `SagaDefinition` | 事务定义结构体 |
| `Step` | 步骤定义结构体 |
| `NewSaga(name string) *SagaBuilder` | 创建 Builder |
| `SagaBuilder.AddStep(step Step) *SagaBuilder` | 添加步骤 |
| `SagaBuilder.WithTimeout(seconds int) *SagaBuilder` | 设置超时 |
| `SagaBuilder.WithPayload(payload map[string]any) *SagaBuilder` | 设置全局 Payload |
| `SagaBuilder.Build() (*SagaDefinition, error)` | 构建定义 |

#### Step 结构体字段

```go
type Step struct {
    Name             string         // 步骤名称
    Type             StepType       // sync / async
    ActionMethod     string         // 正向 HTTP 方法
    ActionURL        string         // 正向 URL (支持模板)
    ActionPayload    map[string]any // 正向请求体 (支持模板)
    CompensateMethod string         // 补偿 HTTP 方法
    CompensateURL    string         // 补偿 URL (支持模板)
    CompensatePayload map[string]any // 补偿请求体 (支持模板)
    MaxRetry         int            // 最大重试次数 (默认 3)
    
    // 异步步骤轮询配置
    PollURL          string         // 轮询 URL
    PollMethod       string         // 轮询方法 (默认 GET)
    PollIntervalSec  int            // 轮询间隔 (默认 5s)
    PollMaxTimes     int            // 最大轮询次数 (默认 60)
    PollSuccessPath  string         // 成功 JSONPath
    PollSuccessValue string         // 成功值
    PollFailurePath  string         // 失败 JSONPath
    PollFailureValue string         // 失败值
}
```

#### 存储层 (store.go)

| 接口方法 | 说明 |
|---------|------|
| `CreateTransaction(ctx, tx) error` | 创建事务 |
| `GetTransaction(ctx, id) (*Transaction, error)` | 获取事务 |
| `UpdateTransactionStatus(ctx, id, status, error) error` | 更新状态 |
| `CreateSteps(ctx, steps) error` | 批量创建步骤 |
| `GetSteps(ctx, txID) ([]*Step, error)` | 获取所有步骤 |
| `GetStep(ctx, stepID) (*Step, error)` | 获取单个步骤 |
| `UpdateStepStatus(ctx, stepID, status, error) error` | 更新步骤状态 |
| `UpdateStepResponse(ctx, stepID, response) error` | 更新响应 |
| `CreatePollTask(ctx, task) error` | 创建轮询任务 |
| `AcquirePollTasks(ctx, instanceID, batchSize) ([]*PollTask, error)` | 获取轮询任务 |
| `ListRecoverableTransactions(ctx) ([]*Transaction, error)` | 崩溃恢复查询 |
| `ListTimedOutTransactions(ctx) ([]*Transaction, error)` | 超时查询 |

#### 模板渲染 (template.go)

| 函数 | 说明 |
|-----|------|
| `RenderTemplate(tpl, data) (string, error)` | 渲染模板字符串 |
| `RenderPayload(payload, data) (map[string]any, error)` | 渲染 Payload |
| `BuildTemplateData(tx, steps, currentStep) map[string]any` | 构建模板数据 |

**模板语法**
```
{action_response.field}              # 当前步骤响应字段
{step[0].action_response.field}      # 指定步骤响应字段
{transaction.payload.field}          # 全局 Payload 字段
```

#### JSONPath 解析 (jsonpath.go)

| 函数 | 说明 |
|-----|------|
| `ExtractByPath(data, path) (string, error)` | JSONPath 提取 |
| `MatchPollResult(response, step) (success, failure bool, err error)` | 匹配轮询结果 |

**JSONPath 语法**
```
$.status                 # 顶层字段
$.result.code            # 嵌套字段
$.items[0].status        # 数组索引
```

#### 执行器 (executor.go)

| 类型/函数 | 说明 |
|----------|------|
| `ExecutorConfig` | 执行器配置 |
| `NewExecutor(store Store, cfg *ExecutorConfig) *Executor` | 创建执行器 |
| `Executor.ExecuteStep(ctx, tx, step, allSteps) error` | 执行同步步骤 |
| `Executor.ExecuteAsyncStep(ctx, tx, step, allSteps) error` | 执行异步步骤 |
| `Executor.CompensateStep(ctx, tx, step, allSteps) error` | 执行补偿 |
| `Executor.Poll(ctx, tx, step, allSteps) (map[string]any, error)` | 执行轮询请求 |

**执行器错误常量**

| 错误 | 说明 |
|-----|------|
| `ErrStepRetryable` | 步骤失败但可重试 |
| `ErrStepFatal` | 步骤失败且不可重试 |
| `ErrCompensationFailed` | 补偿执行失败 |

### 分布式链路追踪集成

SAGA 模块与 trace 模块深度集成，实现全链路追踪：

**工作原理**

1. **提交事务时**：`Engine.Submit()` 自动从 context 中提取 `TraceContext`，将 `trace_id` 和 `span_id` 存储到事务 payload 中
2. **执行步骤时**：执行器自动从 context 或 payload 中恢复 trace 上下文，并注入到所有出站 HTTP 请求中
3. **后台任务**：Poller 和 Coordinator 在执行时会从 payload 中恢复 trace 上下文，确保崩溃恢复后仍能关联日志

**HTTP 请求头注入**

所有 SAGA 步骤执行的 HTTP 请求（正向执行、异步执行、补偿、轮询）都会自动注入以下请求头：

| 请求头 | 说明 |
|-------|------|
| `X-B3-TraceId` | 全链路追踪 ID |
| `X-B3-SpanId` | 当前操作的 Span ID |
| `X-B3-Sampled` | 采样标志 |
| `X-Saga-Transaction-Id` | SAGA 事务 ID |
| `X-Idempotency-Key` | 幂等 Key（步骤 ID） |

**使用示例**

```go
// 带 trace 上下文提交事务
ctx := trace.ContextWithTrace(context.Background(), &trace.TraceContext{
    TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
    SpanId:  "00f067aa0ba902b7",
    Sampled: true,
})

txID, err := engine.Submit(ctx, def)
// 后续所有步骤执行都会携带此 trace 信息
```

### 数据库表结构

```sql
-- 全局事务表
CREATE TABLE saga_transactions (
    id              VARCHAR(64) PRIMARY KEY,
    status          VARCHAR(20) NOT NULL,
    payload         JSONB,
    current_step    INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at     TIMESTAMPTZ,
    timeout_at      TIMESTAMPTZ,
    retry_count     INT NOT NULL DEFAULT 0,
    last_error      TEXT
);

-- 步骤表
CREATE TABLE saga_steps (
    id                  VARCHAR(64) PRIMARY KEY,
    transaction_id      VARCHAR(64) NOT NULL REFERENCES saga_transactions(id),
    step_index          INT NOT NULL,
    name                VARCHAR(128) NOT NULL,
    step_type           VARCHAR(20) NOT NULL,
    status              VARCHAR(20) NOT NULL,
    action_method       VARCHAR(10) NOT NULL,
    action_url          TEXT NOT NULL,
    action_payload      JSONB,
    action_response     JSONB,
    compensate_method   VARCHAR(10) NOT NULL,
    compensate_url      TEXT NOT NULL,
    compensate_payload  JSONB,
    poll_url            TEXT,
    poll_method         VARCHAR(10) DEFAULT 'GET',
    poll_interval_sec   INT DEFAULT 5,
    poll_max_times      INT DEFAULT 60,
    poll_count          INT NOT NULL DEFAULT 0,
    poll_success_path   TEXT,
    poll_success_value  TEXT,
    poll_failure_path   TEXT,
    poll_failure_value  TEXT,
    next_poll_at        TIMESTAMPTZ,
    retry_count         INT NOT NULL DEFAULT 0,
    max_retry           INT NOT NULL DEFAULT 3,
    last_error          TEXT,
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,
    UNIQUE (transaction_id, step_index)
);

-- 轮询任务表
CREATE TABLE saga_poll_tasks (
    id              BIGSERIAL PRIMARY KEY,
    step_id         VARCHAR(64) NOT NULL REFERENCES saga_steps(id),
    transaction_id  VARCHAR(64) NOT NULL,
    next_poll_at    TIMESTAMPTZ NOT NULL,
    locked_until    TIMESTAMPTZ,
    locked_by       VARCHAR(64),
    UNIQUE (step_id)
);
```

### 使用示例

```go
// 创建引擎
engine, _ := saga.NewEngine(&saga.Config{
    DSN: "postgres://user:pass@localhost:5432/db?sslmode=disable",
})
engine.Start(ctx)
defer engine.Stop()

// 定义事务
def, _ := saga.NewSaga("order-checkout").
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

// 提交事务
txID, _ := engine.Submit(ctx, def)

// 查询状态
status, _ := engine.Query(ctx, txID)
```

---

## 模块四：统一日志模块 (logger)

### 功能概述

基于 Zap 构建的高性能日志模块，支持：
- 结构化日志
- 多输出目标 (stdout/stderr/file)
- 日志轮转
- 动态日志级别
- Context 集成 (trace_id/span_id)
- 采样控制
- **第三方框架集成 (WriterAdapter)**

### API 列表

#### 全局函数 (logger.go)

| 函数 | 说明 |
|-----|------|
| `Init(cfg *Config) error` | 初始化全局 Logger |
| `GetLogger() Logger` | 获取全局 Logger |
| `Debug(msg string, args ...any)` | Debug 级别日志 |
| `Info(msg string, args ...any)` | Info 级别日志 |
| `Warn(msg string, args ...any)` | Warn 级别日志 |
| `Error(msg string, args ...any)` | Error 级别日志 |
| `Fatal(msg string, args ...any)` | Fatal 日志并退出 |
| `DebugContext(ctx, msg, args...)` | 带 Context 的 Debug |
| `InfoContext(ctx, msg, args...)` | 带 Context 的 Info |
| `WarnContext(ctx, msg, args...)` | 带 Context 的 Warn |
| `ErrorContext(ctx, msg, args...)` | 带 Context 的 Error |
| `With(args ...any) Logger` | 创建带字段的子 Logger |
| `WithGroup(name string) Logger` | 创建带分组的子 Logger |
| `Sync() error` | 刷新日志缓冲 |
| `SetLevel(level string) error` | 动态设置日志级别 |
| `GetLevel() string` | 获取当前日志级别 |

#### Logger 接口

```go
type Logger interface {
    Debug(msg string, args ...any)
    Info(msg string, args ...any)
    Warn(msg string, args ...any)
    Error(msg string, args ...any)
    Fatal(msg string, args ...any)
    DebugContext(ctx context.Context, msg string, args ...any)
    InfoContext(ctx context.Context, msg string, args ...any)
    WarnContext(ctx context.Context, msg string, args ...any)
    ErrorContext(ctx context.Context, msg string, args ...any)
    With(args ...any) Logger
    WithGroup(name string) Logger
    WithContext(ctx context.Context) Logger
    Sync() error
    SetLevel(level string) error
    GetLevel() string
    Handler() slog.Handler
}
```

#### Context 集成 (context.go)

| 函数 | 说明 |
|-----|------|
| `ContextWithTraceID(ctx, traceID) context.Context` | 注入 TraceID |
| `ContextWithSpanID(ctx, spanID) context.Context` | 注入 SpanID |
| `ContextWithLogger(ctx, l) context.Context` | 注入 Logger |
| `TraceIDFromContext(ctx) string` | 提取 TraceID |
| `SpanIDFromContext(ctx) string` | 提取 SpanID |
| `FromContext(ctx) Logger` | 从 Context 获取 Logger |

#### 配置 (config.go)

```go
type Config struct {
    Level            Level             // 日志级别
    Format           Format            // 输出格式 (json/console)
    ServiceName      string            // 服务名称
    OutputPaths      []string          // 输出路径
    Development      bool              // 开发模式
    EnableCaller     bool              // 记录调用位置
    EnableStackTrace bool              // 错误堆栈追踪
    Sampling         *SamplingConfig   // 采样配置
    Rotation         *RotationConfig   // 日志轮转配置
    Outputs          []OutputConfig    // 多输出配置
}

type RotationConfig struct {
    MaxSize    int  // 单文件最大 MB
    MaxBackups int  // 保留文件数
    MaxAge     int  // 保留天数
    Compress   bool // 是否压缩
    LocalTime  bool // 使用本地时间
}
```

#### 预设配置函数

| 函数 | 说明 |
|-----|------|
| `DefaultConfig(serviceName) *Config` | 默认配置 (JSON 格式) |
| `DevelopmentConfig(serviceName) *Config` | 开发配置 (彩色控制台) |
| `MultiOutputConfig(serviceName, filePath) *Config` | 多输出配置 |

#### WriterAdapter - 第三方框架集成 (writer.go)

WriterAdapter 实现 `io.Writer` 接口，允许第三方框架（如 Gin、GORM、Asynq）通过标准 io.Writer 接口输出日志到 nsp-common logger。

| 类型/函数 | 说明 |
|----------|------|
| `WriterAdapter` | io.Writer 适配器结构体 |
| `NewWriterAdapter(logger Logger, opts ...WriterOption) *WriterAdapter` | 创建适配器，logger 为 nil 时使用全局 Logger |
| `WriterAdapter.Write(p []byte) (n int, err error)` | 实现 io.Writer 接口 |
| `WriterAdapter.UpdateContext(ctx context.Context)` | 更新 trace 上下文（用于长连接场景） |

**WriterOption 配置选项**

| 函数 | 说明 |
|-----|------|
| `WithLevel(level string) WriterOption` | 设置日志级别 ("debug"/"info"/"warn"/"error")，默认 "info" |
| `WithPrefix(prefix string) WriterOption` | 设置日志前缀，如 "[gin]"、"[asynq]"、"[gorm]" |
| `WithContext(ctx context.Context) WriterOption` | 启用 Context 关联，自动传播 trace_id/span_id |

**使用示例**

```go
// Gin 框架集成
gin.DefaultWriter = logger.NewWriterAdapter(nil, 
    logger.WithLevel("info"), 
    logger.WithPrefix("[gin]"))

// GORM 日志集成（带 trace 关联）
gormLogger := logger.NewWriterAdapter(myLogger, 
    logger.WithContext(ctx), 
    logger.WithLevel("debug"),
    logger.WithPrefix("[gorm]"))

// Asynq 集成
asynq.WithLogger(logger.NewWriterAdapter(nil, 
    logger.WithPrefix("[asynq]")))
```

### 日志输出格式

```json
{
    "timestamp": "2026-03-02T10:30:00.000Z",
    "level": "info",
    "service": "nsp-order",
    "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
    "span_id": "00f067aa0ba902b7",
    "caller": "handler/order.go:42",
    "message": "order created",
    "order_id": "ORD-001"
}
```

---

## 模块五：任务队列编排模块 (taskqueue)

### 功能概述

基于消息队列的工作流编排框架，支持：
- 多步骤工作流
- 优先级队列
- 步骤重试
- 回调机制
- 队列路由

### 核心数据结构

#### 优先级
| 常量 | 值 | 说明 |
|-----|---|------|
| `PriorityLow` | 1 | 低优先级 |
| `PriorityNormal` | 3 | 普通优先级 |
| `PriorityHigh` | 6 | 高优先级 |
| `PriorityCritical` | 9 | 紧急优先级 |

#### 工作流状态
| 状态 | 说明 |
|-----|------|
| `pending` | 待执行 |
| `running` | 执行中 |
| `succeeded` | 成功 |
| `failed` | 失败 |

#### 步骤状态
| 状态 | 说明 |
|-----|------|
| `pending` | 待执行 |
| `queued` | 已入队 |
| `running` | 执行中 |
| `completed` | 完成 |
| `failed` | 失败 |

### API 列表

#### 引擎 (engine.go)

| 类型/函数 | 说明 |
|----------|------|
| `Config` | 引擎配置 |
| `NewEngine(cfg, broker) (*Engine, error)` | 创建引擎 |
| `Engine.Migrate(ctx) error` | 执行数据库迁移 |
| `Engine.SubmitWorkflow(ctx, def) (string, error)` | 提交工作流 |
| `Engine.HandleCallback(ctx, cb) error` | 处理回调 |
| `Engine.QueryWorkflow(ctx, workflowID) (*WorkflowStatusResponse, error)` | 查询工作流 |
| `Engine.RetryStep(ctx, stepID) error` | 重试步骤 |
| `Engine.NewCallbackSender() *CallbackSender` | 创建回调发送器 |
| `Engine.Stop() error` | 停止引擎 |

#### 工作流定义

```go
type WorkflowDefinition struct {
    Name         string
    ResourceType string
    ResourceID   string
    Metadata     map[string]string
    Steps        []StepDefinition
}

type StepDefinition struct {
    TaskType   string   // 任务类型标识
    TaskName   string   // 任务名称
    Params     string   // JSON 参数
    QueueTag   string   // 队列路由标签
    Priority   Priority // 优先级
    MaxRetries int      // 最大重试次数
}
```

#### Broker 接口 (broker.go)

```go
type Broker interface {
    Publish(ctx context.Context, task *Task) (*TaskInfo, error)
    Close() error
}
```

#### Store 接口 (store.go)

| 方法 | 说明 |
|-----|------|
| `Migrate(ctx) error` | 数据库迁移 |
| `CreateWorkflow(ctx, wf) error` | 创建工作流 |
| `GetWorkflow(ctx, id) (*Workflow, error)` | 获取工作流 |
| `UpdateWorkflowStatus(ctx, id, status, error) error` | 更新状态 |
| `IncrementCompletedSteps(ctx, id) error` | 递增完成步骤计数 |
| `IncrementFailedSteps(ctx, id) error` | 递增失败步骤计数 |
| `BatchCreateSteps(ctx, steps) error` | 批量创建步骤 |
| `GetStep(ctx, id) (*StepTask, error)` | 获取步骤 |
| `GetStepsByWorkflow(ctx, workflowID) ([]*StepTask, error)` | 获取工作流所有步骤 |
| `GetNextPendingStep(ctx, workflowID) (*StepTask, error)` | 获取下一个待执行步骤 |
| `UpdateStepStatus(ctx, id, status) error` | 更新步骤状态 |
| `UpdateStepResult(ctx, id, status, result, error) error` | 更新步骤结果 |
| `UpdateStepBrokerID(ctx, id, brokerTaskID) error` | 更新 Broker 任务 ID |
| `GetStepStats(ctx, workflowID) (*StepStats, error)` | 获取统计信息 |

#### 回调发送器

| 类型/函数 | 说明 |
|----------|------|
| `CallbackSender` | 回调发送器 |
| `NewCallbackSenderFromBroker(broker, queue) *CallbackSender` | 创建发送器 |
| `CallbackSender.Success(ctx, taskID, result) error` | 发送成功回调 |
| `CallbackSender.Fail(ctx, taskID, errorMsg) error` | 发送失败回调 |

### 队列路由

默认路由器 `DefaultQueueRouter` 生成队列名称：
```
tasks              # 普通优先级，无标签
tasks_high         # 高优先级
tasks_critical     # 紧急优先级
tasks_{tag}        # 带标签
tasks_{tag}_high   # 带标签和优先级
```

### 使用示例

```go
// 编排端
engine, _ := taskqueue.NewEngine(&taskqueue.Config{
    DSN:           "postgres://...",
    CallbackQueue: "task_callbacks",
}, asynqBroker)

def := &taskqueue.WorkflowDefinition{
    Name:         "create-vrf",
    ResourceType: "vrf",
    ResourceID:   "VRF-001",
    Steps: []taskqueue.StepDefinition{
        {TaskType: "create_vrf_on_switch", TaskName: "Switch-A", Params: `{...}`},
        {TaskType: "create_vrf_on_switch", TaskName: "Switch-B", Params: `{...}`},
    },
}
workflowID, _ := engine.SubmitWorkflow(ctx, def)

// Worker 端
sender := taskqueue.NewCallbackSenderFromBroker(broker, "task_callbacks")
// 处理成功
sender.Success(ctx, taskID, result)
// 处理失败
sender.Fail(ctx, taskID, "error message")
```

---

## 模块六：示例服务 (nsp-demo)

### 功能概述

演示如何集成各公共模块的 HTTP 服务示例。

### 启动参数

| 参数 | 默认值 | 说明 |
|-----|-------|------|
| `-addr` | `:8080` | 监听地址 |
| `-dev` | `false` | 开发模式 |
| `-log-file` | `""` | 日志文件路径 |

### 中间件栈

1. **Recovery** - 捕获 panic
2. **Trace** - 注入 trace_id/span_id
3. **Logger** - 请求日志
4. **Auth** - AK/SK 认证

### API 端点

| 路径 | 方法 | 说明 | 需要认证 |
|-----|-----|------|---------|
| `/health` | GET | 健康检查 | 否 |
| `/hello` | GET | Hello 示例 | 是 |
| `/user` | GET | 用户信息示例 | 是 |
| `/error` | GET | 错误示例 | 是 |
| `/panic` | GET | Panic 示例 | 是 |

### 测试凭证

| AccessKey | SecretKey |
|-----------|-----------|
| `test-ak` | `test-sk-1234567890abcdef` |
| `demo-ak` | `demo-sk-abcdef1234567890` |

---

## 依赖项

```go
// go.mod 依赖
require (
    github.com/gin-gonic/gin v1.10.0
    github.com/google/uuid v1.6.0
    github.com/lib/pq v1.10.9
    go.uber.org/zap v1.27.0
    go.uber.org/zap/exp/zapslog v0.2.0
    gopkg.in/natefinch/lumberjack.v2 v2.2.1
)
```

---

## 最佳实践

### 1. 日志记录
```go
// 使用 Context 自动关联 trace_id
logger.InfoContext(ctx, "processing order", "order_id", orderID)
```

### 2. 服务间调用
```go
// 使用 TracedClient 自动传播追踪信息
client := trace.NewTracedClient(nil)
resp, _ := client.Post(ctx, url, "application/json", body)
```

### 3. 认证集成
```go
// Handler 中获取认证凭证
cred, ok := auth.CredentialFromGin(c)
if ok {
    logger.Info("request from", "client", cred.Label)
}
```

### 4. SAGA 事务
```go
// 使用模板变量引用前序步骤结果
CompensateURL: "http://service/api/cancel/{action_response.task_id}"
```

### 5. SAGA 与 Trace 集成
```go
// 提交事务时自动传播 trace 上下文
// 后续步骤执行的 HTTP 请求都会携带相同的 trace_id
func (h *Handler) CreateOrder(c *gin.Context) {
    ctx := c.Request.Context() // 已由 TraceMiddleware 注入 trace 上下文
    
    def, _ := saga.NewSaga("order-checkout").
        AddStep(...).
        Build()
    
    txID, _ := engine.Submit(ctx, def) // trace 信息自动传播
}
```

### 6. 第三方框架日志集成
```go
// Gin 框架日志集成
gin.DefaultWriter = logger.NewWriterAdapter(nil,
    logger.WithLevel("info"),
    logger.WithPrefix("[gin]"))

// 带 trace 关联的日志
writer := logger.NewWriterAdapter(nil,
    logger.WithContext(ctx),
    logger.WithPrefix("[worker]"))
```

---

## 版本信息

- Go 版本要求: >= 1.21
- 最后更新: 2026-03-02
