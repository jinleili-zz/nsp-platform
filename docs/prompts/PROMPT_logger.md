## 背景

我在开发 NSP 项目，这是一个基于 Go 的微服务平台，包含多个独立的微服务。
我需要在公共基础库（nsp-common）中封装一个统一的日志模块，供所有微服务使用。

---

## 技术选型（已确定）

采用「方案B」：
- 对外接口：基于 log/slog 风格设计（面向 Go 官方标准方向）
- 底层实现：使用 zap 作为高性能驱动引擎
- 桥接方式：使用 go.uber.org/zap/exp/zapslog 将 zap Core 注册为 slog 的 Handler

---

## 目录结构

代码放在如下目录：

nsp-common/
└── pkg/
    └── logger/
        ├── logger.go        # 对外接口 + 全局方法
        ├── config.go        # 配置结构体
        ├── zap_logger.go    # zap 底层实现，zapslog 桥接
        ├── context.go       # Context 集成（TraceID 注入与提取）
        ├── fields.go        # 标准字段 Key 常量定义
        └── logger_test.go   # 单元测试

---

## 核心需求

### 1. 配置（config.go）

定义 Config 结构体，包含以下字段：

- Level：日志级别，枚举值 debug / info / warn / error，默认 info
- Format：输出格式，枚举值 json / console，默认 json
- ServiceName：微服务名称，字符串，必填
- OutputPaths：输出目标列表，字符串数组，支持 "stdout" / "stderr" / 文件路径，默认 ["stdout"]
- Rotation：日志文件轮转配置（子结构体），字段包括：
  - Filename：日志文件路径
  - MaxSize：单文件最大体积，单位 MB，默认 100
  - MaxBackups：最大备份文件数，默认 7
  - MaxAge：日志最大保留天数，默认 30
  - Compress：是否 gzip 压缩归档，默认 true
  - LocalTime：归档文件名是否使用本地时间，默认 true
- EnableCaller：是否在日志中打印调用方文件名和行号，默认 true
- EnableStackTrace：是否在 error 级别自动附加堆栈信息，默认 true
- Development：是否开发模式（开发模式下 console 彩色输出，panic 时打印堆栈），默认 false
- SamplingConfig：采样配置（子结构体），字段包括：
  - Initial：每秒前 N 条日志全量输出，默认 100
  - Thereafter：超出后每 M 条输出 1 条，默认 10

提供两个预设构造函数：
- DefaultConfig(serviceName string) *Config：生产环境默认值
- DevelopmentConfig(serviceName string) *Config：开发环境默认值

---

### 2. 标准字段（fields.go）

用常量统一定义所有日志字段的 Key 名称，包含以下字段：

service / trace_id / span_id / user_id / request_id /
module / method / path / code / latency_ms / error / peer_addr

---

### 3. 对外接口（logger.go）

定义 Logger 接口，方法分为三组：

第一组：基础日志方法
- Debug / Info / Warn / Error / Fatal
- 签名：(msg string, args ...any)
- args 遵循 slog 风格，支持两种写法：
  - 交替 KV：logger.Info("msg", "key", value)
  - slog.Attr：logger.Info("msg", slog.String("key", value))

第二组：Context 感知方法（自动从 ctx 提取 trace_id、span_id）
- DebugContext / InfoContext / WarnContext / ErrorContext
- 签名：(ctx context.Context, msg string, args ...any)

第三组：派生方法
- With(args ...any) Logger：携带固定字段，返回新 Logger
- WithGroup(name string) Logger：字段分组，返回新 Logger
- WithContext(ctx context.Context) Logger：绑定 ctx 中的链路字段，返回新 Logger

管理方法：
- Sync() error：刷新缓冲，程序退出前调用
- SetLevel(level string) error：运行时动态修改日志级别
- GetLevel() string：查询当前日志级别
- Handler() slog.Handler：返回底层 slog.Handler（供需要直接操作 slog 的场景使用）

全局快捷函数：
- 提供包级别的 Init、GetLogger、Debug、Info、Warn、Error、
  DebugContext、InfoContext、WarnContext、ErrorContext、
  With、WithGroup、Sync 函数，
  内部委托给全局 Logger 实例

---

### 4. Context 集成（context.go）

实现以下函数：

// 写入
ContextWithTraceID(ctx context.Context, traceID string) context.Context
ContextWithSpanID(ctx context.Context, spanID string) context.Context
ContextWithLogger(ctx context.Context, l Logger) context.Context

// 读取
TraceIDFromContext(ctx context.Context) string
SpanIDFromContext(ctx context.Context) string
FromContext(ctx context.Context) Logger  // 取不到时返回全局 Logger

// 内部工具函数（不导出）
extractContextFields(ctx context.Context) []any  // 提取 ctx 中的 slog Attr 列表

Context Key 使用包内私有 int 类型（type contextKey int），
避免与其他包的 Key 冲突。

---

### 5. 底层实现（zap_logger.go）

构建流程：

1. 根据 Config.Level 创建 zap.AtomicLevel
2. 根据 Config.Format 创建对应 Encoder（JSON 或 Console）
3. 根据 Config.OutputPaths 构建 WriteSyncer：
   - "stdout" → os.Stdout
   - "stderr" → os.Stderr
   - 文件路径 → lumberjack.Logger（日志轮转）
   - 多个路径 → zapcore.NewMultiWriteSyncer
4. 根据 SamplingConfig 用 zapcore.NewSamplerWithOptions 包装 Core，
   Error 级别不参与采样（全量保留）
5. 使用 zapslog.NewHandler 将 zap Core 转换为 slog.Handler
6. 注入全局固定字段 service = Config.ServiceName
7. 将 slog.Handler 包装为实现 Logger 接口的结构体

SetLevel 通过 zap.AtomicLevel.SetLevel 实现，无需重启服务。

---

### 6. 日志输出格式规范

JSON 格式字段顺序（生产环境）：

{
  "timestamp": "2006-01-02T15:04:05.000Z0700",
  "level":     "info",
  "caller":    "order/service.go:42",
  "service":   "nsp-order",
  "module":    "order-service",
  "trace_id":  "abc-123",
  "message":   "...",
  ... 其他业务字段
}

Console 格式（开发环境）：
2006-01-02T15:04:05.000Z0700  INFO  order/service.go:42  message  {fields}

---

### 7. 依赖版本

go.uber.org/zap                  v1.27.0
go.uber.org/zap/exp/zapslog      跟随 zap 版本
gopkg.in/natefinch/lumberjack.v2  v2.2.1
Go 标准库 log/slog               需要 Go >= 1.21

---

### 8. 测试（logger_test.go）

覆盖以下测试场景：

1. 基础日志输出：各级别日志能正常写入，低于当前级别的日志不输出
2. JSON 格式验证：输出内容可被解析为合法 JSON，且包含 timestamp / level / message / service 字段
3. Context 集成：trace_id 和 span_id 能从 ctx 自动附加到日志输出
4. 动态级别：SetLevel 后日志级别立即生效，无需重新初始化
5. WithFields 派生：With 返回的新 Logger 携带固定字段，不影响原 Logger
6. 采样验证：高频同类日志触发采样后，输出条数少于输入条数
7. Sync：Sync() 调用不返回错误

测试工具：使用 zaptest.NewLogger 或将输出重定向到 bytes.Buffer 以便断言内容。

---

## 输出要求

1. 按文件分别输出完整代码，每个文件用注释标注文件名和包名
2. 所有导出的类型、函数、方法都要有 godoc 注释
3. 不要省略任何实现细节，不要用注释代替代码
4. 在所有代码输出完毕后，提供 go.mod 中需要添加的依赖声明