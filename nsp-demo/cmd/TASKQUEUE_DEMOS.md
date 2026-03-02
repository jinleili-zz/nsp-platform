# TaskQueue Demo 使用说明

本目录包含 TaskQueue 模块的三个演示程序。

## 目录结构

```
nsp-demo/cmd/
├── taskqueue-simple/       # 入门级示例（推荐新手）
├── taskqueue-demo/         # 完整功能演示
└── taskqueue-demo-fail/    # 失败重试演示
```

## 1. taskqueue-simple - 入门示例

**适合人群**：初次使用 TaskQueue

**特点**：
- 单机 Redis（无需集群）
- 简单两步工作流（创建记录 → 发送邮件）
- 代码简洁，易于理解

**运行前准备**：
```bash
# 启动 Redis
docker run -d --name redis -p 6379:6379 redis:6-alpine

# 启动 PostgreSQL
docker run -d --name postgres \
  -e POSTGRES_PASSWORD=postgres \
  -p 5432:5432 postgres:13

# 创建数据库
psql -h localhost -U postgres -c "CREATE DATABASE taskqueue_simple;"
```

**运行**：
```bash
cd nsp-demo/cmd/taskqueue-simple
go run main.go
```

**预期输出**：
```
[Setup] Broker created
[Setup] Database migrated
[Demo] Workflow submitted: id=xxx
[Worker] Creating record: user_registration
[Worker] Sending email to: user@example.com
[Demo] ✅ Workflow SUCCEEDED!
```

---

## 2. taskqueue-demo - 完整功能演示

**适合人群**：了解基本概念，需要学习高级功能

**特点**：
- Redis Cluster 集群模式
- 自定义队列路由器
- 多队列（switch / firewall）
- 完整的 VPC 创建工作流

**运行前准备**：
```bash
# 启动 Redis Cluster（3节点）
# 方法1：使用 docker-compose（推荐）
# 参考：https://github.com/bitnami/containers/tree/main/bitnami/redis-cluster

# 方法2：手动启动（端口映射到 7001-7003）
docker run -d --name redis-node-1 -p 7001:6379 redis:6-alpine redis-server --cluster-enabled yes
docker run -d --name redis-node-2 -p 7002:6379 redis:6-alpine redis-server --cluster-enabled yes
docker run -d --name redis-node-3 -p 7003:6379 redis:6-alpine redis-server --cluster-enabled yes

# 创建集群
redis-cli --cluster create 127.0.0.1:7001 127.0.0.1:7002 127.0.0.1:7003 --cluster-replicas 0

# 创建数据库
psql -h localhost -U saga -c "CREATE DATABASE taskqueue_test;"
# 注意：需要修改 main.go 中的 DSN 连接字符串
```

**运行**：
```bash
cd nsp-demo/cmd/taskqueue-demo
go run main.go
```

**预期输出**：
```
[Demo] Workflow submitted: id=xxx
[Worker] Creating VRF: VRF-DEMO-1
[Worker] Creating VLAN subinterface: vlan_id=100
[Worker] Creating firewall zone: zone-demo-1
[Demo] ✅ Workflow SUCCEEDED!
```

---

## 3. taskqueue-demo-fail - 失败重试演示

**适合人群**：需要了解错误处理和重试机制

**特点**：
- 模拟步骤失败场景
- 演示 `RetryStep()` 手动重试
- 验证工作流状态流转

**流程**：
1. Step 1 成功
2. Step 2 首次执行失败
3. Workflow 进入 `failed` 状态
4. 手动重试 Step 2
5. Step 2 重试成功
6. Step 3 继续执行
7. Workflow 最终 `succeeded`

**运行前准备**：
同 `taskqueue-demo`（Redis Cluster + PostgreSQL）

**运行**：
```bash
cd nsp-demo/cmd/taskqueue-demo-fail
go run main.go
```

**预期输出**：
```
[Demo] Workflow submitted: xxx
[Worker] Creating VRF (成功)
[Worker] Creating VLAN (首次失败)
[Demo] Workflow correctly in FAILED state. Now retrying...
[Demo] Step retried: yyy
[Worker] Creating VLAN (重试成功)
[Worker] Creating firewall zone (成功)
[Demo] ✅ Workflow SUCCEEDED after retry!
```

---

## 环境变量配置

如果需要修改连接参数，可以在代码中修改以下常量：

### taskqueue-simple
```go
const (
    redisAddr = "127.0.0.1:6379"
    pgDSN     = "postgres://postgres:postgres@127.0.0.1:5432/taskqueue_simple?sslmode=disable"
)
```

### taskqueue-demo / taskqueue-demo-fail
```go
const (
    redisNode1 = "127.0.0.1:7001"
    redisNode2 = "127.0.0.1:7002"
    redisNode3 = "127.0.0.1:7003"
    pgDSN      = "postgres://saga:saga123@127.0.0.1:5432/taskqueue_test?sslmode=disable"
)
```

---

## 故障排查

### Redis 连接失败
```bash
# 检查 Redis 是否运行
redis-cli ping  # 应返回 PONG

# 检查 Redis Cluster 状态
redis-cli -p 7001 cluster info
```

### PostgreSQL 连接失败
```bash
# 检查 PostgreSQL 是否运行
psql -h localhost -U postgres -c "SELECT 1;"

# 检查数据库是否存在
psql -h localhost -U postgres -c "\l"
```

### 工作流卡住不动
```bash
# 检查队列积压
redis-cli LLEN simple_tasks
redis-cli LLEN demo_tasks_switch
redis-cli LLEN demo_fail_tasks_switch

# 检查数据库状态
psql -d taskqueue_simple -c "SELECT * FROM tq_workflows ORDER BY created_at DESC LIMIT 5;"
psql -d taskqueue_simple -c "SELECT * FROM tq_steps WHERE workflow_id='xxx';"
```

---

## 更多文档

- **完整使用指南**：`nsp-common/pkg/taskqueue/GUIDE.md`
- **代码分析报告**：`nsp-common/pkg/taskqueue/ANALYSIS.md`
- **API 文档**：参考 GUIDE.md 中的 "API 参考" 章节

---

## 下一步

1. 运行 `taskqueue-simple` 理解基本概念
2. 阅读 `GUIDE.md` 学习高级功能
3. 运行 `taskqueue-demo` 和 `taskqueue-demo-fail` 体验完整功能
4. 集成到自己的项目中

有问题？查看 `GUIDE.md` 的"故障排查"章节或提交 Issue。
