# redis-acl-auth Specification

## Purpose
TBD - created by archiving change lock-redis-acl-support. Update Purpose after archive.
## Requirements
### Requirement: RedisOption 支持 ACL 用户名
`RedisOption` SHALL 包含 `Username string` 字段，用于 Redis Cluster ACL 认证。
当 `Username` 不为空时，`NewRedisClient` SHALL 将其传递给底层 `redis.ClusterOptions.Username`。
当 `Username` 为空时，行为 SHALL 与当前版本一致（仅使用 `Password` 进行传统认证）。

#### Scenario: 携带 Username 创建 Cluster 客户端
- **WHEN** 调用 `NewRedisClient` 时 `RedisOption.Username` 和 `RedisOption.Password` 均已设置
- **THEN** 底层 `redis.ClusterOptions.Username` 和 `redis.ClusterOptions.Password` 均 SHALL 被设置为对应值

#### Scenario: 不设置 Username 时向后兼容
- **WHEN** 调用 `NewRedisClient` 时 `RedisOption.Username` 为空字符串
- **THEN** 底层连接行为 SHALL 与未引入 `Username` 字段之前一致

### Requirement: StandaloneRedisOption 支持 ACL 用户名
`StandaloneRedisOption` SHALL 包含 `Username string` 字段，用于单节点 Redis ACL 认证。
当 `Username` 不为空时，`NewStandaloneRedisClient` SHALL 将其传递给底层 `redis.Options.Username`。
当 `Username` 为空时，行为 SHALL 与当前版本一致。

#### Scenario: 携带 Username 创建单节点客户端
- **WHEN** 调用 `NewStandaloneRedisClient` 时 `StandaloneRedisOption.Username` 和 `StandaloneRedisOption.Password` 均已设置
- **THEN** 底层 `redis.Options.Username` 和 `redis.Options.Password` 均 SHALL 被设置为对应值

#### Scenario: 不设置 Username 时向后兼容
- **WHEN** 调用 `NewStandaloneRedisClient` 时 `StandaloneRedisOption.Username` 为空字符串
- **THEN** 底层连接行为 SHALL 与未引入 `Username` 字段之前一致

### Requirement: ACL 认证失败时返回明确错误
当 Redis 服务端因 ACL 鉴权失败拒绝连接时，`NewRedisClient` 和 `NewStandaloneRedisClient` SHALL 返回包含原始错误信息的非 nil error，而不是 panic 或静默失败。

#### Scenario: ACL 认证失败时初始化报错
- **WHEN** 提供了错误的 `Username` 或 `Password` 导致 Redis 连通性检测失败
- **THEN** 工厂函数 SHALL 返回封装了底层错误的 error，且该 error 通过 `errors.Unwrap` 可溯源

