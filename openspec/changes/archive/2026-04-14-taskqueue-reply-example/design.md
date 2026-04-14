## Context

当前 taskqueue 模块提供了 Broker、Consumer、Inspector 等核心接口，但缺少一个完整的示例来展示以下场景：
1. Producer 和 Consumer 分离部署
2. 多队列命名规范
3. 请求-回复模式（ReplySpec）
4. 通过 TaskID 匹配异步结果
5. Inspector 监控队列状态

本示例基于现有的 asynqbroker 实现，使用 Redis 作为后端。

## Goals / Non-Goals

**Goals:**
- 提供清晰的生产者-消费者分离示例
- 展示队列命名最佳实践（使用 `:` 分隔符）
- 演示 ReplySpec 机制实现双向通信
- 展示 Inspector 接口的使用方法
- 提供可运行的完整测试（基于 Docker Redis）

**Non-Goals:**
- 不修改 taskqueue 模块的核心接口
- 不实现新的 broker 后端
- 不处理分布式事务或复杂错误恢复

## Decisions

### 1. 队列命名规范
使用 `service:action:direction` 格式，例如：
- `calc:request:incoming` - 计算服务的请求队列
- `calc:response:outgoing` - 计算服务的响应队列

**Rationale**: 冒号分隔符符合 Redis/Asynq 的惯例，层次清晰，便于 Inspector 按前缀查询。

### 2. 消息格式设计
```json
{
  "task_id": "uuid-string",
  "operation": "add",
  "operands": [1, 2],
  "reply_to": "calc:response:outgoing"
}
```

**Rationale**: `task_id` 作为业务级唯一标识，与 Broker 分配的 Task ID 分离，便于端到端追踪。

### 3. 回复机制
Consumer 处理完成后，使用 Broker.Publish 向 `ReplySpec.Queue` 发送结果消息，Payload 中包含原始 `task_id`。

**Rationale**: 复用现有的 Broker 接口，无需新增回复专用接口。

### 4. 测试策略
使用 Docker Compose 启动 Redis，Producer 和 Consumer 作为独立进程运行，通过共享 Redis 通信。

**Rationale**: 接近真实部署场景，避免使用内存 broker 导致的测试偏差。

## Risks / Trade-offs

| Risk | Mitigation |
|------|------------|
| 示例代码与核心模块接口不同步 | 示例中使用接口类型而非具体实现，接口变更时编译会报错 |
| 测试依赖 Docker 环境 | 提供 `docker-compose.yml` 和清晰的启动说明 |
| 异步回复可能丢失 | 示例中展示重试逻辑，生产环境应配合持久化 |
