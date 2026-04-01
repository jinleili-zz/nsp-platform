## 1. 修改 RedisOption 和 StandaloneRedisOption

- [x] 1.1 在 `lock/redis.go` 的 `RedisOption` 结构体中新增 `Username string` 字段，并补充注释说明其用途（Redis ACL 用户名）
- [x] 1.2 在 `lock/redis.go` 的 `StandaloneRedisOption` 结构体中新增 `Username string` 字段，并补充注释

## 2. 透传 Username 到底层 go-redis 客户端

- [x] 2.1 在 `NewRedisClient` 中将 `opt.Username` 赋值给 `redis.ClusterOptions.Username`
- [x] 2.2 在 `NewStandaloneRedisClient` 中将 `opt.Username` 赋值给 `redis.Options.Username`

## 3. 更新使用示例注释

- [x] 3.1 更新 `lock/lock.go` 中的 Usage example 注释，增加携带 `Username` 的 ACL 配置示例

## 4. 新增测试

- [x] 4.1 在 `lock/lock_test.go` 中新增 `TestNewRedisClient_UsernameField` 测试：构造带 `Username` 的 `RedisOption` 并验证该字段被正确设置（通过直接构造 `redis.ClusterOptions` 对比或检查字段赋值路径）
- [x] 4.2 在 `lock/lock_test.go` 中新增 `TestNewStandaloneRedisClient_UsernameField` 测试：构造带 `Username` 的 `StandaloneRedisOption`，结合 miniredis 验证选项被正确透传

## 5. 更新文档

- [x] 5.1 更新 `docs/lock.md` 中 `RedisOption` 配置表，新增 `Username` 行（类型 `string`，默认值 `""`，说明 Redis ACL 用户名，空值时退回传统密码认证）
- [x] 5.2 更新 `docs/lock.md` 中 `StandaloneRedisOption` 配置表，同样新增 `Username` 行
- [x] 5.3 在 `docs/lock.md` 快速使用章节新增"Redis ACL 认证"示例，展示同时设置 `Username` 和 `Password` 的用法
- [x] 5.4 同步更新 `docs/modules/lock.md`（内容与 `docs/lock.md` 一致，重复以上三步变更）
- [x] 5.5 更新 `docs/prompts/PROMPT_lock.md` 中 `RedisOption` 结构体描述，新增 `Username` 字段及说明，与代码注释保持一致
