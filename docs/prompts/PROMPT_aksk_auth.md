# 背景

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
