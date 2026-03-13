# TaskQueue Demo 使用说明

本目录包含 TaskQueue 模块的演示程序，分为两种模式：

## 目录结构

```
nsp-demo/cmd/
├── taskqueue-workflow-demo/   # 使用 Engine 工作流编排（推荐）
├── taskqueue-broker-demo/     # 仅使用 Broker（自定义实现）
└── ... (legacy demos)
```

## 两种模式对比

| 特性 | Workflow Demo | Broker Demo |
|------|---------------|-------------|
| 工作流编排 | Engine 自动管理 | 自定义实现 |
| 步骤状态 | Engine 自动推进 | 需自行管理 |
| 任务存储 | Engine 负责 | 自定义 TaskStore |
| 失败重试 | Engine 自动处理 | 需自行实现 |
| 适用场景 | 复杂多步骤流程 | 简单独立任务 |

---

## 1. taskqueue-workflow-demo - Engine 工作流模式

**特点**：
- 使用 Engine 的完整工作流编排能力
- 自动管理多步骤顺序执行
- 自动处理失败重试和状态推进
- PostgreSQL 持久化 + asynq 消息投递

**运行前准备**：
```bash
# 启动 Redis
docker run -d --name redis -p 6379:6379 redis:6-alpine

# 启动 PostgreSQL
docker run -d --name postgres \
  -e POSTGRES_PASSWORD=postgres \
  -p 5432:5432 postgres:13

# 创建数据库
psql -h localhost -U postgres -c "CREATE DATABASE taskqueue_workflow;"
```

**运行**：
```bash
cd nsp-demo/cmd/taskqueue-workflow-demo
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

## 2. taskqueue-broker-demo - 自定义 Broker 模式

**特点**：
- 不使用 Engine 的工作流编排
- 自定义任务存储层（TaskStore）
- 自己实现任务提交、状态管理、回调处理
- 只用 asynq broker 做消息投递
- 展示如何基于 broker 构建自己的任务队列系统

**与 Workflow Demo 的区别**：
- Workflow Demo：适合复杂的多步骤业务流程
- Broker Demo：适合简单的独立任务，或者需要完全自定义任务生命周期的场景

**运行前准备**：
```bash
# 同上，启动 Redis 和 PostgreSQL
docker run -d --name redis -p 6379:6379 redis:6-alpine
docker run -d --name postgres -e POSTGRES_PASSWORD=postgres -p 5432:5432 postgres:13

# 创建数据库
psql -h localhost -U postgres -c "CREATE DATABASE taskqueue_broker;"
```

**运行**：
```bash
cd nsp-demo/cmd/taskqueue-broker-demo
go run main.go
```

**预期输出**：
```
[Setup] Broker created
[Setup] Database migrated
[Demo] Task 1 submitted: id=xxx
[Demo] Task 2 submitted: id=xxx
[Worker] Creating record: user_registration
[Worker] Sending email to: user@example.com
[Demo] ✅ All Tasks SUCCEEDED!
[Demo] Testing retry logic
[Demo] Failing task final status: failed (retries=2/2)
```

**代码结构解析**：

```go
// 1. 自定义任务存储层
type TaskStore struct {
    db *sql.DB
}

// 2. 任务管理器（核心逻辑）
type TaskManager struct {
    store  *TaskStore
    broker *asynqbroker.Broker
}

// 3. 提交任务：存储到 DB + 发布到 Broker
func (m *TaskManager) SubmitTask(ctx context.Context, ...) (string, error) {
    // 1. 写入 PostgreSQL
    // 2. 发布到 asynq
    // 3. 更新状态
}

// 4. 处理回调：更新任务状态 + 决定是否重试
func (m *TaskManager) HandleCallback(ctx context.Context, cb *CallbackPayload) error {
    // 1. 查询任务
    // 2. 根据状态处理（成功/失败/重试）
    // 3. 需要重试时重新发布到 broker
}
```

---

## 环境变量配置

如果需要修改连接参数，可以在代码中修改以下常量：

### taskqueue-workflow-demo
```go
const (
    redisAddr = "127.0.0.1:6379"
    pgDSN     = "postgres://postgres:postgres@127.0.0.1:5432/taskqueue_workflow?sslmode=disable"
)
```

### taskqueue-broker-demo
```go
const (
    redisAddr = "127.0.0.1:6379"
    pgDSN     = "postgres://postgres:postgres@127.0.0.1:5432/taskqueue_broker?sslmode=disable"
)
```

---

## 故障排查

### Redis 连接失败
```bash
redis-cli ping  # 应返回 PONG
```

### PostgreSQL 连接失败
```bash
psql -h localhost -U postgres -c "SELECT 1;"
```

### 任务卡住不动
```bash
# 检查队列积压
redis-cli LLEN workflow_tasks
redis-cli LLEN broker_tasks

# 检查数据库状态
psql -d taskqueue_workflow -c "SELECT * FROM tq_workflows ORDER BY created_at DESC LIMIT 5;"
psql -d taskqueue_broker -c "SELECT * FROM broker_tasks ORDER BY created_at DESC LIMIT 5;"
```

---

## 更多文档

- **完整使用指南**：`nsp-common/pkg/taskqueue/GUIDE.md`
- **代码分析报告**：`nsp-common/pkg/taskqueue/ANALYSIS.md`
- **API 文档**：参考 GUIDE.md 中的 "API 参考" 章节

---

## 选择哪种模式？

| 场景 | 推荐模式 |
|------|----------|
| 多步骤业务流程，需要自动推进状态 | Workflow Demo |
| 简单独立任务，无需步骤串联 | Broker Demo |
| 需要完全自定义任务生命周期 | Broker Demo |
| 快速开发，最小化代码量 | Workflow Demo |
