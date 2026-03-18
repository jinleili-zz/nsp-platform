# NSP Platform

NSP (Network Service Platform) 是一个基于 Go 语言构建的微服务基础组件库，提供分布式系统开发所需的核心功能。

## 安装

```bash
go get github.com/jinleili-zz/nsp-platform
```

## 模块

| 模块 | 导入路径 | 说明 |
|------|----------|------|
| auth | `github.com/jinleili-zz/nsp-platform/auth` | AK/SK 认证 |
| saga | `github.com/jinleili-zz/nsp-platform/saga` | SAGA 分布式事务 |
| taskqueue | `github.com/jinleili-zz/nsp-platform/taskqueue` | 任务队列编排 |
| logger | `github.com/jinleili-zz/nsp-platform/logger` | 统一日志 |
| trace | `github.com/jinleili-zz/nsp-platform/trace` | 分布式链路追踪 |
| lock | `github.com/jinleili-zz/nsp-platform/lock` | 分布式锁 |
| config | `github.com/jinleili-zz/nsp-platform/config` | 配置管理 |

## 快速开始

### 按需导入

只需导入你需要的模块：

```go
import "github.com/jinleili-zz/nsp-platform/saga"
import "github.com/jinleili-zz/nsp-platform/auth"
import "github.com/jinleili-zz/nsp-platform/logger"
```

### 示例

```go
package main

import (
    "github.com/jinleili-zz/nsp-platform/logger"
    "github.com/jinleili-zz/nsp-platform/saga"
)

func main() {
    // 初始化日志
    log := logger.NewZapLogger(&logger.Config{
        Level:  "info",
        Format: "json",
    })

    // 使用 SAGA 分布式事务
    engine, err := saga.NewEngine(store)
    if err != nil {
        log.Error("failed to create saga engine", "error", err)
        return
    }

    // ...
}
```

## 目录结构

```
nsp-platform/
├── auth/               # AK/SK 认证
├── saga/               # SAGA 分布式事务
├── taskqueue/          # 任务队列编排
│   ├── asynqbroker/    # Asynq 实现
│   └── rocketmqbroker/ # RocketMQ 实现
├── logger/             # 统一日志
├── trace/              # 分布式链路追踪
├── lock/               # 分布式锁
├── config/             # 配置管理
├── docs/               # 详细文档
└── examples/           # 示例代码
```

## 文档

- [完整文档](DOCUMENTATION.md)
- [开发指南](DEVELOPER_GUIDE.md)
- [docs/](docs/) - 各模块详细文档

## License

Apache 2.0
