## Context

`lock` 模块通过 `go-redis/v9` 连接 Redis，当前 `RedisOption` 和 `StandaloneRedisOption` 只暴露了 `Password` 字段，对应 Redis 传统的单密码认证（`AUTH password`）。

Redis 6.0 引入了 ACL 机制，认证方式变为 `AUTH username password`。`go-redis/v9` 的 `redis.Options` 和 `redis.ClusterOptions` 均已通过 `Username` 字段支持 ACL，但 `lock` 模块从未将该字段透传给底层客户端。

**当前代码路径：**
- `redis.go:105` — `redis.NewClusterClient(&redis.ClusterOptions{...})` 无 `Username`
- `redis.go:386` — `redis.NewClient(&redis.Options{...})` 无 `Username`

## Goals / Non-Goals

**Goals:**
- 在 `RedisOption` 和 `StandaloneRedisOption` 中增加 `Username` 字段
- 将 `Username` 透传至 `redis.ClusterOptions` 和 `redis.Options`
- 保持 100% 向后兼容（`Username` 为空时行为不变）

**Non-Goals:**
- 不验证 ACL 权限是否足够（例如是否有 SET/GET/DEL 权限）
- 不支持 TLS/mTLS 配置（属于独立 concern）
- 不修改 `Lock` 或 `Client` 接口

## Decisions

### 决策 1：字段命名为 `Username`（而非 `User` 或 `ACLUser`）

与 `go-redis/v9` 的字段名保持一致（`redis.Options.Username`），降低使用者的认知负荷。`User` 过于宽泛，`ACLUser` 过于实现细节化。

**备选方案：** 使用 `ACLUser` — 拒绝，暴露了不必要的实现细节，且 go-redis 本身不使用该命名。

### 决策 2：纯透传，不在模块层做 ACL 验证

ACL 权限校验由 Redis 服务端负责。`NewRedisClient` 已有连通性检测（`ClusterInfo` ping），若 ACL 用户名/密码不正确，ping 会立即失败并将错误透传给调用方，无需额外处理。

**备选方案：** 在初始化时发送 `AUTH` 命令做提前验证 — 拒绝，`ClusterInfo` 本身就需要认证，已起到等效的早期失败效果。

### 决策 3：`Username` 为空时不做特殊处理

`go-redis/v9` 内部：当 `Username` 为空且 `Password` 非空时，发送 `AUTH password`；当两者均设置时，发送 `AUTH username password`；均为空时不发送 AUTH。无需在 `lock` 模块层添加条件判断。

## Risks / Trade-offs

- **误配置风险**：用户同时设置 `Username` 和使用旧式单密码 Redis 时，连通性检测会失败并返回明确错误，风险可控。
- **miniredis ACL 支持有限**：miniredis 对 ACL 命令的支持不完整，`Username` 字段的测试需通过检查选项结构体字段被正确赋值来间接验证，而非通过真实认证流程。

## Migration Plan

仅增量变更，无迁移步骤。现有调用方不受影响；需要 ACL 的调用方新增 `Username` 字段即可。
