## Why

Redis 6.0 引入了 ACL（Access Control List）功能，允许通过用户名 + 密码进行认证（`AUTH username password`），并为不同用户配置精细化的命令和 Key 权限。当前 `lock` 模块的 `RedisOption` 和 `StandaloneRedisOption` 只支持传统的无用户名密码认证（`AUTH password`），无法与启用 ACL 的 Redis 实例对接，导致在安全要求较高的生产环境中无法使用该模块。

## What Changes

- 在 `RedisOption` 中新增 `Username string` 字段，用于 Redis Cluster ACL 认证
- 在 `StandaloneRedisOption` 中新增 `Username string` 字段，用于单节点 Redis ACL 认证
- `NewRedisClient` 构建 `redis.ClusterOptions` 时传入 `Username`
- `NewStandaloneRedisClient` 构建 `redis.Options` 时传入 `Username`
- 更新使用示例注释，展示带 ACL 的配置方式
- 新增测试：验证带 `Username` 字段的选项能正常传递（借助 miniredis ACL 支持或单独的验证测试）

所有变更均向后兼容，`Username` 为空时行为与当前一致。

## Capabilities

### New Capabilities

- `redis-acl-auth`: 为 Redis Cluster 客户端和单节点客户端提供 Redis ACL 用户名 + 密码认证支持

### Modified Capabilities

<!-- 无现有 spec 需要调整 -->

## Impact

- **修改文件**：`lock/redis.go`（`RedisOption`、`StandaloneRedisOption`、`NewRedisClient`、`NewStandaloneRedisClient`）
- **测试文件**：`lock/lock_test.go`（新增 ACL 相关配置的测试用例）
- **外部依赖**：无新增依赖，`github.com/redis/go-redis/v9` 的 `Options.Username` 和 `ClusterOptions.Username` 字段已支持 ACL
- **API 兼容性**：纯增量变更，现有调用方无需修改
