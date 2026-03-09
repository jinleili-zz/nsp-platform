# Config 模块

> 包路径：`github.com/paic/nsp-common/pkg/config`

## 功能说明

Config 模块解决以下问题：

- **统一配置加载**：支持 YAML、JSON、TOML 等多种格式
- **环境变量覆盖**：通过前缀自动绑定环境变量，便于容器化部署
- **默认值设置**：减少配置文件冗余，提供合理默认值
- **热更新支持**：监听配置文件变化，自动重载并触发回调
- **实现解耦**：业务代码仅依赖 Loader 接口，底层可替换

---

## 核心接口

```go
// Loader 配置加载器接口
type Loader interface {
    // Load 加载配置文件并反序列化到 target（必须是指针）
    // 每次调用都会重新读取文件
    // 使用严格模式：配置文件中的未知字段会导致错误
    Load(target any) error

    // OnChange 注册配置变更回调
    // 配置文件变化并成功重载后，按注册顺序触发所有回调
    // apply 函数用于反序列化最新配置：apply(&newCfg)
    // 如果 Watch=false，回调永远不会触发
    OnChange(fn func(apply func(any) error))

    // Close 停止配置监听，释放资源
    // 可多次调用，后续调用为空操作
    Close()
}
```

---

## 配置项

### Option

| 字段名 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| `ConfigFile` | `string` | `""` | 配置文件完整路径（与 ConfigName+ConfigPaths 互斥） |
| `ConfigName` | `string` | `""` | 配置文件名（不含扩展名），需配合 ConfigPaths 使用 |
| `ConfigPaths` | `[]string` | `nil` | 配置文件搜索路径列表 |
| `ConfigType` | `string` | `""` | 配置文件格式（yaml/json/toml），为空时按扩展名自动识别 |
| `Defaults` | `map[string]any` | `nil` | 默认值，键支持点分隔，如 `"server.port"` |
| `Watch` | `bool` | `false` | 是否启用热更新监听 |
| `EnvPrefix` | `string` | `""` | 环境变量前缀，如 `"NSP"` 会绑定 `NSP_SERVER_PORT` 到 `server.port` |

---

## 快速使用

### 基础配置加载

配置文件 `config/config.yaml`：

```yaml
server:
  host: "0.0.0.0"
  port: 8080
  read_timeout: 15s
  write_timeout: 15s

database:
  host: "localhost"
  port: 5432
  name: "nsp_db"
  user: "postgres"
  password: "secret"

redis:
  addrs:
    - "redis-0:6379"
    - "redis-1:6379"
  password: ""
  pool_size: 10

log:
  level: "info"
  format: "json"

debug: false
```

Go 代码：

```go
package main

import (
    "fmt"
    "time"

    "github.com/paic/nsp-common/pkg/config"
)

// 配置结构体（字段标签使用 mapstructure）
type ServerConfig struct {
    Host         string        `mapstructure:"host"`
    Port         int           `mapstructure:"port"`
    ReadTimeout  time.Duration `mapstructure:"read_timeout"`
    WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

type DatabaseConfig struct {
    Host     string `mapstructure:"host"`
    Port     int    `mapstructure:"port"`
    Name     string `mapstructure:"name"`
    User     string `mapstructure:"user"`
    Password string `mapstructure:"password"`
}

type RedisConfig struct {
    Addrs    []string `mapstructure:"addrs"`
    Password string   `mapstructure:"password"`
    PoolSize int      `mapstructure:"pool_size"`
}

type LogConfig struct {
    Level  string `mapstructure:"level"`
    Format string `mapstructure:"format"`
}

type AppConfig struct {
    Server   ServerConfig   `mapstructure:"server"`
    Database DatabaseConfig `mapstructure:"database"`
    Redis    RedisConfig    `mapstructure:"redis"`
    Log      LogConfig      `mapstructure:"log"`
    Debug    bool           `mapstructure:"debug"`
}

func main() {
    // 1. 创建配置加载器
    loader, err := config.New(config.Option{
        ConfigFile: "./config/config.yaml",
    })
    if err != nil {
        panic(err)
    }
    defer loader.Close()

    // 2. 加载配置
    var cfg AppConfig
    if err := loader.Load(&cfg); err != nil {
        panic(err)
    }

    // 3. 使用配置
    fmt.Printf("服务地址: %s:%d\n", cfg.Server.Host, cfg.Server.Port)
    fmt.Printf("数据库: %s@%s:%d/%s\n",
        cfg.Database.User, cfg.Database.Host, cfg.Database.Port, cfg.Database.Name)
}
```

### 使用默认值和环境变量

```go
func loadConfig() (*AppConfig, error) {
    loader, err := config.New(config.Option{
        ConfigFile: "./config/config.yaml",
        EnvPrefix:  "NSP",  // 环境变量前缀
        Defaults: map[string]any{
            "server.port":          8080,
            "server.read_timeout":  "15s",
            "server.write_timeout": "15s",
            "redis.pool_size":      10,
            "log.level":            "info",
            "log.format":           "json",
            "debug":                false,
        },
    })
    if err != nil {
        return nil, err
    }
    defer loader.Close()

    var cfg AppConfig
    if err := loader.Load(&cfg); err != nil {
        return nil, err
    }

    return &cfg, nil
}

// 环境变量覆盖示例：
// NSP_SERVER_PORT=9090        → cfg.Server.Port = 9090
// NSP_DATABASE_PASSWORD=xxx   → cfg.Database.Password = "xxx"
// NSP_DEBUG=true              → cfg.Debug = true
```

### 启用热更新

```go
package main

import (
    "sync"

    "github.com/paic/nsp-common/pkg/config"
    "github.com/paic/nsp-common/pkg/logger"
)

var (
    appConfig AppConfig
    configMu  sync.RWMutex
)

func main() {
    // 创建带热更新的加载器
    loader, err := config.New(config.Option{
        ConfigFile: "./config/config.yaml",
        Watch:      true,  // 启用热更新
        EnvPrefix:  "NSP",
    })
    if err != nil {
        panic(err)
    }
    defer loader.Close()

    // 初始加载
    if err := loader.Load(&appConfig); err != nil {
        panic(err)
    }

    // 注册热更新回调
    loader.OnChange(func(apply func(any) error) {
        var newCfg AppConfig
        if err := apply(&newCfg); err != nil {
            // 新配置解析失败，记录日志，继续使用旧配置
            logger.Error("配置热更新失败", logger.FieldError, err)
            return
        }

        // 原子更新配置
        configMu.Lock()
        appConfig = newCfg
        configMu.Unlock()

        logger.Info("配置已热更新",
            "server_port", newCfg.Server.Port,
            "log_level", newCfg.Log.Level,
        )

        // 可在此处触发其他组件重新初始化
        if err := logger.SetLevel(newCfg.Log.Level); err != nil {
            logger.Warn("设置日志级别失败", logger.FieldError, err)
        }
    })

    // 获取配置的安全方式
    getConfig := func() AppConfig {
        configMu.RLock()
        defer configMu.RUnlock()
        return appConfig
    }

    // 使用配置
    cfg := getConfig()
    fmt.Printf("当前端口: %d\n", cfg.Server.Port)

    // 保持运行，等待配置变更
    select {}
}
```

### 多路径搜索配置

```go
func loadWithPaths() (*AppConfig, error) {
    loader, err := config.New(config.Option{
        ConfigName: "config",  // 不含扩展名
        ConfigPaths: []string{
            "./config",           // 当前目录下的 config 文件夹
            "/etc/nsp",           // 系统配置目录
            "$HOME/.nsp",         // 用户配置目录
        },
        ConfigType: "yaml",    // 明确指定格式（可选）
    })
    if err != nil {
        return nil, err
    }
    defer loader.Close()

    var cfg AppConfig
    if err := loader.Load(&cfg); err != nil {
        return nil, err
    }

    return &cfg, nil
}
```

---

## 与其他模块集成

### 初始化 Logger

```go
import (
    "github.com/paic/nsp-common/pkg/config"
    "github.com/paic/nsp-common/pkg/logger"
)

func initLogger(cfg LogConfig) error {
    logCfg := &logger.Config{
        Level:       logger.Level(cfg.Level),
        Format:      logger.Format(cfg.Format),
        ServiceName: cfg.ServiceName,
        OutputPaths: cfg.OutputPaths,
    }

    return logger.Init(logCfg)
}
```

### 初始化分布式锁

```go
import (
    "github.com/paic/nsp-common/pkg/config"
    "github.com/paic/nsp-common/pkg/lock"
)

func initLockClient(cfg RedisConfig) (lock.Client, error) {
    return lock.NewRedisClient(lock.RedisOption{
        Addrs:    cfg.Addrs,
        Password: cfg.Password,
        PoolSize: cfg.PoolSize,
    })
}
```

---

## 生产环境配置示例

### 完整 YAML 配置

```yaml
# config/config.yaml

server:
  host: "0.0.0.0"
  port: 8080
  read_timeout: 15s
  write_timeout: 15s

log:
  level: "info"
  format: "json"
  service_name: "order-service"

redis:
  addrs:
    - "redis-0.redis.default.svc.cluster.local:6379"
    - "redis-1.redis.default.svc.cluster.local:6379"
  password: ""  # 通过环境变量 NSP_REDIS_PASSWORD 注入
  pool_size: 10

database:
  host: "postgres.default.svc.cluster.local"
  port: 5432
  name: "nsp_order"
  user: "nsp_user"
  password: ""  # 通过环境变量 NSP_DATABASE_PASSWORD 注入

debug: false
```

### Kubernetes 部署

```yaml
# deployment.yaml
env:
  - name: NSP_REDIS_PASSWORD
    valueFrom:
      secretKeyRef:
        name: app-secrets
        key: redis-password
  - name: NSP_DATABASE_PASSWORD
    valueFrom:
      secretKeyRef:
        name: app-secrets
        key: db-password
```

---

## 注意事项

### 配置结构体标签

使用 `mapstructure` 标签（viper 底层使用 mapstructure 反序列化）：

```go
// 正确
type Config struct {
    ServerPort int `mapstructure:"server_port"`
}

// 错误：标签名与配置文件不匹配
type Config struct {
    ServerPort int `mapstructure:"serverPort"`  // 配置文件用 server_port
}
```

### 严格模式

Load 使用 `UnmarshalExact`，配置文件中的未知字段会导致错误，有助于发现拼写错误。

### 环境变量命名

- 前缀 + 下划线 + 键名（嵌套用下划线连接）
- 全部大写

```
EnvPrefix: "NSP"
server.port        → NSP_SERVER_PORT
database.password  → NSP_DATABASE_PASSWORD
```

### 常见错误

```go
// 错误：忘记调用 Close
func bad() {
    loader, _ := config.New(config.Option{Watch: true})
    var cfg AppConfig
    loader.Load(&cfg)
    // 缺少 defer loader.Close()，监听 goroutine 会泄漏
}

// 正确：始终 defer Close
func good() {
    loader, err := config.New(config.Option{Watch: true})
    if err != nil {
        return err
    }
    defer loader.Close()

    var cfg AppConfig
    return loader.Load(&cfg)
}
```

---

## 支持的配置格式

| 格式 | 扩展名 | 说明 |
|------|--------|------|
| YAML | `.yaml`, `.yml` | 推荐，可读性好 |
| JSON | `.json` | 通用格式 |
| TOML | `.toml` | 适合简单配置 |
| HCL | `.hcl` | HashiCorp 配置语言 |
| ENV | `.env` | 环境变量文件 |
| Properties | `.properties` | Java 风格配置 |
