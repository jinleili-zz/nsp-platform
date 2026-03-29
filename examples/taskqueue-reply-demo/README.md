# Taskqueue Reply Demo

演示 taskqueue 模块的生产者-消费者双向通信模式。

## 架构

```
Producer ──[calc:request:incoming]──> Consumer
Producer <──[calc:response:outgoing]── Consumer
```

- **Producer**: 发送计算任务（加法、减法、乘法），并通过回复队列接收结果。
- **Consumer**: 监听请求队列，执行计算，将结果发送到 ReplySpec 指定的回复队列。

## 队列命名规范

使用 `service:action:direction` 格式：
- `calc:request:incoming` — 计算服务请求队列
- `calc:response:outgoing` — 计算服务响应队列

## 快速开始

### 1. 启动 Redis

```bash
docker-compose up -d
```

### 2. 启动 Consumer

```bash
go run ./cmd/consumer
```

### 3. 启动 Producer（另开终端）

```bash
go run ./cmd/producer
```

### 4. 清理

```bash
docker-compose down
```

## 关键特性演示

- **ReplySpec**: Producer 指定回复队列，Consumer 自动将结果发送到该队列
- **task_id 匹配**: 使用 UUID 作为业务级任务 ID，实现端到端的请求-回复匹配
- **Inspector**: 查询队列列表、队列统计、任务详情
- **队列命名规范**: 使用冒号分隔的多层次队列命名
