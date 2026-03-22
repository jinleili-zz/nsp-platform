// inspector.go - RocketMQ 实现的 Inspector 接口（仅核心接口）
package rocketmqbroker

import (
	"context"
	"sync"
	"time"

	"github.com/apache/rocketmq-client-go/v2/admin"
	"github.com/apache/rocketmq-client-go/v2/primitive"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
)

// 编译期接口检查：仅实现核心 Inspector 接口
var _ taskqueue.Inspector = (*Inspector)(nil)

// InspectorConfig holds configuration for the RocketMQ Inspector.
type InspectorConfig struct {
	// NameServer is the RocketMQ name server address (e.g., "127.0.0.1:9876")
	NameServer string
	// ConsumerGroup is used to query consumer stats
	ConsumerGroup string
}

// Inspector 实现 taskqueue.Inspector 核心接口。
// RocketMQ 不支持 TaskReader/TaskController/QueueController 可选接口。
//
// 注意：RocketMQ Admin API 功能有限，部分方法返回的数据不完整。
// 这是 RocketMQ 的固有限制，不影响核心功能使用。
type Inspector struct {
	admin  admin.Admin
	config *InspectorConfig
	mu     sync.Mutex
	closed bool
}

// NewInspector 创建 RocketMQ 实现的 Inspector。
func NewInspector(cfg *InspectorConfig) (*Inspector, error) {
	if cfg.NameServer == "" {
		cfg.NameServer = "127.0.0.1:9876"
	}
	if cfg.ConsumerGroup == "" {
		cfg.ConsumerGroup = "taskqueue_consumer_group"
	}

	adm, err := admin.NewAdmin(
		admin.WithResolver(primitive.NewPassthroughResolver([]string{cfg.NameServer})),
	)
	if err != nil {
		return nil, err
	}

	return &Inspector{
		admin:  adm,
		config: cfg,
	}, nil
}

// -----------------------------------------------------------------------------
// Inspector 核心接口实现
// -----------------------------------------------------------------------------

// Queues 返回所有队列名称列表。
// RocketMQ 中队列对应 Topic。
func (i *Inspector) Queues(ctx context.Context) ([]string, error) {
	topics, err := i.admin.FetchAllTopicList(ctx)
	if err != nil {
		return nil, err
	}

	// 过滤系统 topic
	result := make([]string, 0, len(topics.TopicList))
	for _, topic := range topics.TopicList {
		// 跳过 RocketMQ 内部 topic
		if isSystemTopic(topic) {
			continue
		}
		result = append(result, topic)
	}
	return result, nil
}

// GetQueueStats 返回指定队列的实时统计快照。
// RocketMQ Admin API 功能有限，大部分统计字段返回零值。
//
// 支持的字段：
// - Queue: 队列名称
// - Timestamp: 统计时间点
// - Paused: 固定为 false（RocketMQ 不支持队列暂停）
//
// 不支持的字段（返回零值）：
// - Pending, Scheduled, Active, Retry, Failed, Completed
//
// 注意：完整的消费进度统计需要通过 RocketMQ Console 或 mqadmin 工具查看。
func (i *Inspector) GetQueueStats(ctx context.Context, queue string) (*taskqueue.QueueStats, error) {
	// 检查 topic 是否存在
	topics, err := i.admin.FetchAllTopicList(ctx)
	if err != nil {
		return nil, err
	}

	found := false
	for _, topic := range topics.TopicList {
		if topic == queue {
			found = true
			break
		}
	}
	if !found {
		return nil, taskqueue.ErrQueueNotFound
	}

	// RocketMQ Admin API 不提供详细的消费统计
	// 返回基础信息
	return &taskqueue.QueueStats{
		Queue:     queue,
		Timestamp: time.Now(),
		// RocketMQ 不支持暂停队列
		Paused: false,
		// 以下字段 RocketMQ Admin API 无法直接获取，返回零值
		Pending:   0,
		Scheduled: 0,
		Active:    0,
		Retry:     0,
		Failed:    0,
		Completed: 0,
	}, nil
}

// ListWorkers 返回当前在线的 worker 实例信息。
// RocketMQ Admin API 功能有限，返回空列表。
//
// 注意：Consumer 连接信息需要通过 RocketMQ Console 或 mqadmin 工具查看。
func (i *Inspector) ListWorkers(ctx context.Context) ([]*taskqueue.WorkerInfo, error) {
	// RocketMQ admin.Admin 接口不提供 Consumer 连接查询
	// 返回空列表
	return []*taskqueue.WorkerInfo{}, nil
}

// Close 释放 Inspector 持有的连接资源。
func (i *Inspector) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.closed {
		return nil
	}
	i.closed = true
	return i.admin.Close()
}

// -----------------------------------------------------------------------------
// 辅助函数
// -----------------------------------------------------------------------------

// isSystemTopic 检查是否为 RocketMQ 系统 topic。
func isSystemTopic(topic string) bool {
	systemPrefixes := []string{
		"RMQ_SYS_",
		"SCHEDULE_TOPIC_",
		"BenchmarkTest",
		"DefaultCluster",
		"SELF_TEST_TOPIC",
		"TBW102",
		"%RETRY%",
		"%DLQ%",
	}
	for _, prefix := range systemPrefixes {
		if len(topic) >= len(prefix) && topic[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
