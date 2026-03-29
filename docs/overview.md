# NSP Platform 开发者总览

> 模块路径：`github.com/paic/nsp-common`
> Go 版本：1.24+

本文档覆盖跨模块的核心内容：架构总览、快速接入、中间件链与请求生命周期、FAQ 与故障排查。
各模块的详细 API 和配置，请参阅同目录下对应的专题文档。

---

## 目录

- [一、平台架构总览](#一平台架构总览)
- [二、快速开始](#二快速开始)
- [三、中间件链与请求生命周期](#三中间件链与请求生命周期)
- [四、FAQ 与故障排查](#四faq-与故障排查)

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
| **taskqueue** | `pkg/taskqueue` | 异步任务投递与队列巡检抽象，当前提供 Asynq 适配与 Reply/Trace/Metadata 透传 |

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

## 三、中间件链与请求生命周期

### 推荐中间件注册顺序

```go
r := gin.New()
r.Use(middleware.GinRecovery())               // 1. 捕获 panic，防止服务崩溃
r.Use(trace.TraceMiddleware(instanceId))      // 2. 注入 trace_id / span_id
r.Use(logger.AccessLogMiddleware())           // 3. 记录访问日志（需 trace 先注入）
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
// 全局带追踪的 HTTP 客户端（只需初始化一次）
var httpClient = trace.NewTracedClient(nil)

func (h *Handler) CreateOrder(c *gin.Context) {
    ctx := c.Request.Context() // 已包含 trace + credential

    // 调用下游服务，自动传播 X-B3-TraceId / X-B3-SpanId
    resp, err := httpClient.Post(ctx, "http://stock/deduct", "application/json", body)

    // 日志自动携带 trace_id / span_id
    logger.Business().InfoContext(ctx, "order created", "order_id", "ORD-001")

    // 传给 service 层保持 trace 上下文
    result, err := h.orderService.Create(ctx, req)
}

func (s *OrderService) Create(ctx context.Context, req *CreateOrderReq) (*Order, error) {
    // service 层同样可以用 ctx 获取认证信息
    cred, _ := auth.CredentialFromContext(ctx)
    logger.Business().InfoContext(ctx, "processing", "operator", cred.Label)
    return nil, nil
}
```

---

## 四、FAQ 与故障排查

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
3. `saga_transactions` 表是否存在——参考 [`saga.md`](./saga.md) 中的数据库表结构初始化

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
