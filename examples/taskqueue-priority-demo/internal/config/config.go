// config.go - 共享配置和队列常量定义
// Package config provides shared configuration and queue constants for the TaskQueue priority demo.
package config

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/hibiken/asynq"
)

// 队列命名规范（符合 Redis 命名习惯：小写、冒号分隔、语义清晰）
const (
	// 任务发送队列 - 按优先级分层
	QueueTaskHigh   = "taskqueue:business:task:send:priority:high"   // 高优先级
	QueueTaskMedium = "taskqueue:business:task:send:priority:medium" // 中优先级
	QueueTaskLow    = "taskqueue:business:task:send:priority:low"    // 低优先级

	// 结果回调队列
	QueueResultCallback = "taskqueue:business:result:callback"
)

// 任务类型常量
const (
	TaskTypeEmailSend     = "email:send"      // 发送邮件
	TaskTypeImageProcess  = "image:process"   // 图片处理
	TaskTypeDataExport    = "data:export"     // 数据导出
	TaskTypeReportGenerate = "report:generate" // 报表生成
	TaskTypeNotification  = "notification:send" // 发送通知
)

// 回调任务类型
const (
	CallbackTaskType = "task:result:callback"
)

// 优先级权重配置（用于消费者队列权重）
// 权重越高，消费者从该队列获取任务的概率越大
var QueueWeights = map[string]int{
	QueueTaskHigh:   30, // 高优先级权重最高
	QueueTaskMedium: 20, // 中优先级
	QueueTaskLow:    10, // 低优先级
}

// 默认配置
const (
	DefaultConcurrency   = 5
	DefaultCallbackConcurrency = 2
	DefaultTaskTimeout   = 30 * time.Second
	DefaultMaxRetry      = 3
)

// Config 应用配置
type Config struct {
	// Redis 配置
	// RedisAddrs 包含一个或多个节点地址；多个地址时使用 Redis Cluster 模式
	RedisAddrs []string
	RedisDB    int

	// 消费者配置
	Concurrency       int           // 任务消费者并发数
	CallbackConcurrency int         // 回调消费者并发数
	QueueWeights      map[string]int // 队列权重配置

	// 任务配置
	TaskTimeout time.Duration // 任务执行超时时间
	MaxRetry    int           // 最大重试次数

	// 实例标识
	InstanceID string
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		log.Fatal("REDIS_ADDR environment variable is required")
	}
	// 支持逗号分隔的多节点地址（Cluster 模式），单个地址则为单节点模式
	addrs := strings.Split(redisAddr, ",")
	for i := range addrs {
		addrs[i] = strings.TrimSpace(addrs[i])
	}
	return &Config{
		RedisAddrs:          addrs,
		RedisDB:             0,
		Concurrency:         DefaultConcurrency,
		CallbackConcurrency: DefaultCallbackConcurrency,
		QueueWeights:        QueueWeights,
		TaskTimeout:         DefaultTaskTimeout,
		MaxRetry:            DefaultMaxRetry,
		InstanceID:          getInstanceID(),
	}
}

// RedisConnOpt 返回适合当前配置的 asynq Redis 连接选项。
// 单节点返回 RedisClientOpt，多节点（Cluster）返回 RedisClusterClientOpt。
func (c *Config) RedisConnOpt() asynq.RedisConnOpt {
	if len(c.RedisAddrs) == 1 {
		return asynq.RedisClientOpt{Addr: c.RedisAddrs[0], DB: c.RedisDB}
	}
	return asynq.RedisClusterClientOpt{Addrs: c.RedisAddrs}
}

// GetQueueByPriority 根据优先级获取对应的队列名称
func GetQueueByPriority(priority string) string {
	switch priority {
	case "high":
		return QueueTaskHigh
	case "medium":
		return QueueTaskMedium
	case "low":
		return QueueTaskLow
	default:
		return QueueTaskMedium // 默认中优先级
	}
}

// GetPriorityByQueue 根据队列名称获取优先级
func GetPriorityByQueue(queue string) string {
	switch queue {
	case QueueTaskHigh:
		return "high"
	case QueueTaskMedium:
		return "medium"
	case QueueTaskLow:
		return "low"
	default:
		return "medium"
	}
}

// getInstanceID 获取实例标识
func getInstanceID() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "unknown-instance"
}
