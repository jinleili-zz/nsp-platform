## 背景

我在开发 NSP 项目，这是一个基于 Go 的微服务平台。
我需要在公共基础库（nsp-common）中封装一个统一的配置管理模块，
以 SDK 形式嵌入每个业务服务。

---

## 技术选型（已确定）

- 底层库：github.com/spf13/viper
- Go 版本：>= 1.21
- 部署环境：Kubernetes
- 封装目标：业务代码只依赖接口，不直接依赖 viper
  后续如有必要，可以只替换实现层，业务代码不需要改动

---

## 目录结构

nsp-common/
└── pkg/
    └── config/
        ├── config.go        # 对外接口定义（Loader 接口、Option、UnmarshalFunc）
        ├── viper.go         # viper 实现（viperLoader 结构体）
        └── config_test.go   # 单元测试

---

## 接口设计（config.go）

### Loader 接口

// Loader 是配置加载器的核心接口，业务代码只依赖此接口
type Loader interface {
    // Load 加载配置文件并反序列化到 target（必须是指针）
    // 每次调用都会重新读取文件内容
    Load(target any) error

    // Unmarshal 将当前内存中的配置反序列化到 target（必须是指针）
    // 用于热更新回调中获取最新配置，不重新读取文件
    Unmarshal(target any) error

    // OnChange 注册配置变更回调
    // 文件发生变化且成功重新加载后，按注册顺序依次触发所有回调
    // 回调参数 UnmarshalFunc 可用于获取最新配置
    // 回调在独立 goroutine 中执行，封装层保证并发安全
    OnChange(fn func(unmarshal UnmarshalFunc))

    // Close 停止文件监听，释放文件描述符等资源
    // 服务优雅关闭时调用，测试用例结束时也需调用
    Close()
}

// UnmarshalFunc 热更新回调中用于获取最新配置的函数签名
type UnmarshalFunc func(target any) error

### Option 结构体

type Option struct {
    // ConfigFile 配置文件完整路径，如 "./config/config.yaml"
    // 与 ConfigName+ConfigPaths 二选一，ConfigFile 优先级更高
    ConfigFile string

    // ConfigName 配置文件名（不含扩展名），如 "config"
    // 需要配合 ConfigPaths 使用
    ConfigName string

    // ConfigPaths 配置文件搜索路径列表
    // 按顺序搜索，找到第一个匹配的文件为止
    ConfigPaths []string

    // ConfigType 配置文件格式，如 "yaml" "json" "toml"
    // 留空时根据文件扩展名自动判断
    ConfigType string

    // Defaults 配置项默认值
    // key 支持点号路径，如 "server.port"
    // 在配置文件和环境变量都未提供该项时生效
    Defaults map[string]any

    // Watch 是否开启热更新
    // true 时监听配置文件变化，变化后自动重新加载并触发 OnChange 回调
    Watch bool

    // EnvPrefix 环境变量前缀
    // 非空时自动绑定环境变量
    // 如前缀 "NSP"，环境变量 NSP_SERVER_PORT 覆盖配置项 server.port
    // 环境变量匹配不区分大小写
    EnvPrefix string
}

### New 函数

// New 创建 Loader 实例，当前返回 viper 实现
// 是唯一依赖具体实现的地方，换库时只需修改此函数
func New(opt Option) (Loader, error)

---

## 实现层要求（viper.go）

### viperLoader 结构体

内部字段：
  v         *viper.Viper          // viper 实例
  mu        sync.RWMutex          // 保护 callbacks 列表和热更新并发
  callbacks []func(UnmarshalFunc) // 已注册的变更回调列表
  done      chan struct{}          // 用于 Close 信号

### Load 实现

1. 调用 viper.ReadInConfig() 读取文件
2. 调用 viper.Unmarshal(target) 反序列化
3. 使用 mapstructure 的 DecoderConfig：
   - TagName: "mapstructure"
   - WeaklyTypedInput: false   // 不允许类型隐式转换，严格模式
   - ErrorUnused: true         // 配置文件中出现未定义字段时报错
                               // 防止拼写错误被静默忽略

### Unmarshal 实现

直接调用 viper.Unmarshal(target)，不重新读取文件
加读锁保护（与热更新的写锁互斥）

### OnChange 实现

加锁将 fn 追加到 callbacks 列表
如果 Watch=false，注册回调不报错，但永远不会被触发
（让业务代码不需要关心是否开启了 Watch）

### 热更新实现

Watch=true 时：
  调用 viper.WatchConfig()
  调用 viper.OnConfigChange(handler)

handler 的执行逻辑：
  1. 加写锁
  2. 构造 unmarshalFn（对 viper.Unmarshal 的封装，内部持有读锁）
  3. 释放写锁
  4. 按注册顺序依次调用所有 callbacks(unmarshalFn)
  5. 单个 callback panic 时用 recover 捕获，记录日志后继续执行下一个
     不能因为一个回调 panic 导致后续回调不执行

注意：
  K8s Secret 卷更新使用软链接原子替换，触发 fsnotify 的 Create 事件
  viper 内部的 fsnotify 已处理此场景，不需要额外适配
  但需要在注释中说明这一点

### Close 实现

关闭 done channel
viper 没有提供停止 WatchConfig 的方法
通过在 OnConfigChange 回调中检查 done channel 状态来跳过执行
（Close 后触发的文件事件不再执行回调）

---

## 使用示例（以注释形式写在 config.go 末尾）

type ServerConfig struct {
    Host string `mapstructure:"host"`
    Port int    `mapstructure:"port"`
}

type AppConfig struct {
    Server ServerConfig `mapstructure:"server"`
    Debug  bool         `mapstructure:"debug"`
}

func main() {
    loader, err := config.New(config.Option{
        ConfigFile: "./config/config.yaml",
        Watch:      true,
        EnvPrefix:  "NSP",
        Defaults: map[string]any{
            "server.port": 8080,
            "debug":       false,
        },
    })
    if err != nil {
        panic(err)
    }
    defer loader.Close()

    var cfg AppConfig
    if err := loader.Load(&cfg); err != nil {
        panic(err)
    }

    // 注册热更新回调
    // 回调内部决定如何使用新配置，封装层不介入
    loader.OnChange(func(unmarshal config.UnmarshalFunc) {
        var newCfg AppConfig
        if err := unmarshal(&newCfg); err != nil {
            // 新配置解析失败，记录日志，继续使用旧配置
            return
        }
        cfg = newCfg
    })

    // 业务代码使用 cfg，不直接调用任何 viper API
}

---

## 测试要求（config_test.go）

使用 os.CreateTemp 或 t.TempDir() 创建临时配置文件，不依赖外部文件

覆盖以下场景：

1. Load_YAML：加载 yaml 格式配置文件，验证字段正确反序列化
2. Load_JSON：加载 json 格式配置文件，验证字段正确反序列化
3. Load_DefaultValue：配置文件中缺少的字段，使用 Defaults 中的默认值
4. Load_EnvOverride：设置 EnvPrefix 后，环境变量值覆盖配置文件中的值
5. Load_UnknownField：配置文件中存在结构体未定义的字段时，Load 返回错误
6. Load_FileNotFound：配置文件不存在时，Load 返回明确的错误信息
7. Unmarshal_AfterLoad：Load 之后调用 Unmarshal，结果与 Load 一致
8. OnChange_Triggered：
   Watch=true 时，修改配置文件后，OnChange 回调被触发
   回调内通过 unmarshal 获取到的是新配置的值
   使用 time.Sleep 或 channel 等待回调触发，超时 3s 视为失败
9. OnChange_MultipleCallbacks：
   注册多个回调，文件变化后所有回调按注册顺序依次触发
10. OnChange_WatchFalse：
    Watch=false 时，注册 OnChange 回调不报错，修改文件后回调不触发
11. Close_StopsWatch：
    Close 后修改配置文件，OnChange 回调不再触发
12. OnChange_PanicRecovery：
    第一个回调 panic，第二个回调仍然正常执行

---

## 输出要求

1. 按文件分别输出完整代码，每个文件顶部注释标注文件名和包名
2. 所有导出的类型、函数、方法均需有 godoc 注释
3. 不得省略任何实现细节，不得用注释代替代码
4. 接口层（config.go）不出现任何 viper 类型
5. 代码输出完毕后，提供需要在 go.mod 中添加的依赖声明