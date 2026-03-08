# Auth 模块

> 包路径：`github.com/paic/nsp-common/pkg/auth`

## 功能说明

Auth 模块解决以下问题：

- **服务间认证**：基于 AK/SK 的 HMAC-SHA256 签名认证
- **防重放攻击**：时间戳校验（±5分钟）+ Nonce 一次性校验
- **安全传输**：SK 永不传输，只参与签名计算
- **Gin 集成**：开箱即用的中间件，支持路径豁免

---

## 核心接口

### 凭证存储接口

```go
// Credential 凭证结构体
type Credential struct {
    AccessKey string   // AK，公开标识，随请求传输
    SecretKey string   // SK，私钥，仅服务端存储，永不传输
    Label     string   // 描述，如 "nsp-order-service"
    Enabled   bool     // 是否启用，false 时拒绝认证
}

// CredentialStore 凭证存储接口
type CredentialStore interface {
    // GetByAK 根据 AK 查询凭证
    // 找不到返回 (nil, nil)；出错返回 (nil, err)
    GetByAK(ctx context.Context, ak string) (*Credential, error)
}
```

### Nonce 存储接口

```go
// NonceStore Nonce 防重放接口
type NonceStore interface {
    // CheckAndStore 检查并存储 Nonce
    // 未使用：存储并返回 (false, nil)
    // 已使用：返回 (true, nil)
    CheckAndStore(ctx context.Context, nonce string, ttl time.Duration) (used bool, err error)
}
```

### 签名器（客户端）

```go
// Signer 客户端签名器
type Signer struct { /* ... */ }

// NewSigner 创建签名器
func NewSigner(ak, sk string) *Signer

// Sign 对 HTTP 请求签名
// 自动填充 X-NSP-Timestamp、X-NSP-Nonce、X-NSP-SignedHeaders、Authorization
func (s *Signer) Sign(req *http.Request) error
```

### 验证器（服务端）

```go
// VerifierConfig 验证器配置
type VerifierConfig struct {
    TimestampTolerance time.Duration   // 时间戳容忍偏差，默认 5 分钟
    NonceTTL           time.Duration   // Nonce 存储有效期，默认 15 分钟
}

// Verifier 服务端验证器
type Verifier struct { /* ... */ }

// NewVerifier 创建验证器
func NewVerifier(store CredentialStore, nonces NonceStore, cfg *VerifierConfig) *Verifier

// Verify 验证 HTTP 请求
// 成功返回凭证，失败返回对应错误
func (v *Verifier) Verify(req *http.Request) (*Credential, error)
```

---

## 配置项

### VerifierConfig

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `TimestampTolerance` | `time.Duration` | `5 * time.Minute` | 时间戳最大偏差（客户端与服务端时钟差） |
| `NonceTTL` | `time.Duration` | `15 * time.Minute` | Nonce 缓存时间（防重放窗口） |

### MiddlewareOption

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `Skipper` | `func(*gin.Context) bool` | `nil` | 返回 true 则跳过认证 |
| `OnAuthFailed` | `func(*gin.Context, error)` | 默认 JSON 响应 | 自定义认证失败处理 |

---

## 快速使用

### 服务端配置

```go
package main

import (
    "github.com/gin-gonic/gin"
    "github.com/paic/nsp-common/pkg/auth"
)

func main() {
    // 1. 初始化凭证存储（生产环境建议使用数据库）
    credStore := auth.NewMemoryStore([]*auth.Credential{
        {
            AccessKey: "order-service-ak",
            SecretKey: "order-service-sk-32-characters!",
            Label:     "order-service",
            Enabled:   true,
        },
        {
            AccessKey: "user-service-ak",
            SecretKey: "user-service-sk-32-characters!!",
            Label:     "user-service",
            Enabled:   true,
        },
    })

    // 2. 初始化 Nonce 存储（生产环境建议使用 Redis）
    nonceStore := auth.NewMemoryNonceStore()

    // 3. 创建验证器（使用默认配置）
    verifier := auth.NewVerifier(credStore, nonceStore, nil)

    // 4. 配置 Gin 中间件
    router := gin.New()
    router.Use(auth.AKSKAuthMiddleware(verifier, &auth.MiddlewareOption{
        // 豁免健康检查和 Prometheus 指标接口
        Skipper: auth.NewSkipperByPathPrefix("/health", "/metrics"),
    }))

    // 5. 注册业务路由
    router.GET("/api/v1/orders", func(c *gin.Context) {
        // 从 gin.Context 获取已验证的凭证
        cred, ok := auth.CredentialFromGin(c)
        if !ok {
            c.JSON(401, gin.H{"error": "unauthorized"})
            return
        }

        c.JSON(200, gin.H{
            "message": "Hello " + cred.Label,
        })
    })

    router.Run(":8080")
}
```

### 客户端调用（net/http）

```go
package main

import (
    "fmt"
    "io"
    "net/http"
    "strings"

    "github.com/paic/nsp-common/pkg/auth"
)

func main() {
    // 创建签名器
    signer := auth.NewSigner("order-service-ak", "order-service-sk-32-characters!")

    // GET 请求
    getReq, err := http.NewRequest("GET", "http://localhost:8080/api/v1/orders?page=1", nil)
    if err != nil {
        panic(err)
    }
    if err := signer.Sign(getReq); err != nil {
        panic(err)
    }

    resp, err := http.DefaultClient.Do(getReq)
    if err != nil {
        panic(err)
    }
    defer resp.Body.Close()

    body, _ := io.ReadAll(resp.Body)
    fmt.Printf("GET 响应: %d - %s\n", resp.StatusCode, body)

    // POST 请求（带 Body）
    payload := `{"item_id": "SKU-001", "quantity": 2}`
    postReq, err := http.NewRequest("POST", "http://localhost:8080/api/v1/orders", strings.NewReader(payload))
    if err != nil {
        panic(err)
    }
    postReq.Header.Set("Content-Type", "application/json")

    if err := signer.Sign(postReq); err != nil {
        panic(err)
    }

    resp2, err := http.DefaultClient.Do(postReq)
    if err != nil {
        panic(err)
    }
    defer resp2.Body.Close()

    body2, _ := io.ReadAll(resp2.Body)
    fmt.Printf("POST 响应: %d - %s\n", resp2.StatusCode, body2)
}
```

### 客户端调用（go-resty）

```go
package main

import (
    "fmt"
    "net/http"

    "github.com/go-resty/resty/v2"
    "github.com/paic/nsp-common/pkg/auth"
)

func main() {
    signer := auth.NewSigner("order-service-ak", "order-service-sk-32-characters!")

    // 创建带签名钩子的 Resty 客户端
    client := resty.New()
    client.SetPreRequestHook(func(c *resty.Client, req *http.Request) error {
        return signer.Sign(req)
    })

    // GET 请求
    resp, err := client.R().
        SetHeader("Content-Type", "application/json").
        SetQueryParam("page", "1").
        Get("http://localhost:8080/api/v1/orders")

    if err != nil {
        panic(err)
    }
    fmt.Printf("响应: %d - %s\n", resp.StatusCode(), resp.String())
}
```

---

## AK/SK 签名算法

### 请求头规范

| Header | 格式 | 说明 |
|--------|------|------|
| `Authorization` | `NSP-HMAC-SHA256 AK=<ak>, Signature=<signature>` | 认证方案 + AK + 签名 |
| `X-NSP-Timestamp` | Unix 秒级时间戳字符串 | 防重放 - 时间窗口校验 |
| `X-NSP-Nonce` | 32 位十六进制字符串（16 字节） | 防重放 - 唯一性校验 |
| `X-NSP-SignedHeaders` | 小写、分号分隔、已排序 | 参与签名的 Header 列表 |

默认签名 Headers：`content-type;x-nsp-nonce;x-nsp-timestamp`

### 签名字符串构造

StringToSign 由以下内容按行拼接（每行以 `\n` 结尾，最后一行无 `\n`）：

```
Line 1: HTTP Method（大写）
Line 2: Canonical URI（仅 Path，空则填 /）
Line 3: Canonical Query String（参数名和值排序，格式 a=1&b=2）
Line 4: Canonical Headers（key:value\n 格式，按 SignedHeaders 顺序）
Line 5: SignedHeaders（分号分隔列表）
Line 6: hex(SHA256(body))
```

### 签名计算

```
signature = hex(HMAC-SHA256(SecretKey, StringToSign))
```

---

## 与其他模块集成

### 结合 Trace 模块获取完整上下文

```go
import (
    "github.com/gin-gonic/gin"
    "github.com/paic/nsp-common/pkg/auth"
    "github.com/paic/nsp-common/pkg/logger"
    "github.com/paic/nsp-common/pkg/trace"
)

func orderHandler(c *gin.Context) {
    ctx := c.Request.Context()

    // 获取追踪信息
    tc, _ := trace.TraceFromContext(ctx)

    // 获取认证信息
    cred, _ := auth.CredentialFromContext(ctx)

    // 记录带完整上下文的日志
    logger.InfoContext(ctx, "处理订单请求",
        "client", cred.Label,
        "trace_id", tc.TraceID,
        "span_id", tc.SpanId,
    )
}
```

### 在 Service 层使用凭证

```go
type OrderService struct {
    logger logger.Logger
}

func (s *OrderService) CreateOrder(ctx context.Context, req CreateOrderRequest) error {
    // 从 Context 获取凭证（由中间件注入）
    cred, ok := auth.CredentialFromContext(ctx)
    if !ok {
        return errors.New("未找到认证凭证")
    }

    s.logger.InfoContext(ctx, "创建订单",
        "client", cred.Label,
        "order_id", req.OrderID,
    )

    // 业务逻辑...
    return nil
}
```

---

## 注意事项

### 安全要点

- **SK 绝不传输**：SecretKey 只存储在服务端，永远不随请求发送
- **使用 HTTPS**：虽然签名可防篡改，但仍建议使用 HTTPS 防止窃听
- **定期轮换密钥**：建议每 90 天更换一次 AK/SK
- **最小权限**：每个服务使用独立的 AK/SK，便于审计和撤销

### 性能提示

- **Nonce 存储选型**：单实例用 MemoryNonceStore，多实例必须用 Redis
- **凭证缓存**：生产环境建议实现带缓存的 CredentialStore
- **Body 大小限制**：默认最大 10MB，超大请求考虑分片或异步

### 常见错误

```go
// 错误：直接比较签名（存在时序攻击风险）
if clientSig == expectedSig { ... }

// 正确：使用 hmac.Equal（常量时间比较）
if hmac.Equal([]byte(clientSig), []byte(expectedSig)) { ... }

// 错误：Sign 后修改请求
req.Header.Set("X-Custom", "value")  // 破坏签名
signer.Sign(req)

// 正确：先设置所有 Header，最后 Sign
req.Header.Set("X-Custom", "value")
req.Header.Set("Content-Type", "application/json")
signer.Sign(req)  // 最后调用
```

---

## 错误码映射

| 错误 | HTTP 状态码 | 说明 |
|------|-------------|------|
| `ErrMissingAuthHeader` | 400 | Authorization 头缺失 |
| `ErrInvalidAuthFormat` | 400 | Authorization 格式错误 |
| `ErrMissingTimestamp` | 400 | X-NSP-Timestamp 缺失或格式错误 |
| `ErrMissingNonce` | 400 | X-NSP-Nonce 缺失 |
| `ErrTimestampExpired` | 401 | 时间戳超出容忍窗口 |
| `ErrNonceReused` | 401 | Nonce 已被使用（重放攻击） |
| `ErrAKNotFound` | 401 | AK 不存在或已禁用 |
| `ErrSignatureMismatch` | 401 | 签名不匹配 |
