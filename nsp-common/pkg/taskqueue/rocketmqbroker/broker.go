// Package rocketmqbroker provides a RocketMQ implementation of taskqueue.Broker and taskqueue.Consumer
package rocketmqbroker

import (
	"context"
	"fmt"

	"github.com/apache/rocketmq-client-go/v2"
	"github.com/apache/rocketmq-client-go/v2/primitive"
	"github.com/apache/rocketmq-client-go/v2/producer"
	"github.com/paic/nsp-common/pkg/taskqueue"
)

// BrokerConfig holds configuration for the RocketMQ broker.
type BrokerConfig struct {
	// NameServer is the RocketMQ name server address (e.g., "127.0.0.1:9876")
	NameServer string
	// GroupName is the producer group name
	GroupName string
	// Retry times when send failed
	RetryTimes int
}

// Broker implements taskqueue.Broker using Apache RocketMQ.
type Broker struct {
	producer rocketmq.Producer
	config   *BrokerConfig
}

// NewBroker creates a RocketMQ-backed Broker.
func NewBroker(cfg *BrokerConfig) (*Broker, error) {
	if cfg.NameServer == "" {
		return nil, fmt.Errorf("NameServer is required")
	}
	if cfg.GroupName == "" {
		cfg.GroupName = "taskqueue_producer_group"
	}
	if cfg.RetryTimes == 0 {
		cfg.RetryTimes = 2
	}

	p, err := rocketmq.NewProducer(
		producer.WithNameServer([]string{cfg.NameServer}),
		producer.WithGroupName(cfg.GroupName),
		producer.WithRetry(cfg.RetryTimes),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create producer: %w", err)
	}

	if err := p.Start(); err != nil {
		return nil, fmt.Errorf("failed to start producer: %w", err)
	}

	return &Broker{
		producer: p,
		config:   cfg,
	}, nil
}

// Publish sends a task to the RocketMQ topic.
// Queue name is mapped to RocketMQ topic name.
func (b *Broker) Publish(ctx context.Context, task *taskqueue.Task) (*taskqueue.TaskInfo, error) {
	topic := task.Queue
	if topic == "" {
		topic = "taskqueue_default"
	}

	msg := primitive.NewMessage(topic, task.Payload)
	
	// Set task type as message tag for filtering
	msg = msg.WithTag(task.Type)
	
	// Set priority if specified
	// RocketMQ doesn't have built-in priority, but we can use message properties
	if task.Priority > 0 {
		msg.WithProperty("priority", fmt.Sprintf("%d", task.Priority))
	}
	
	// Add metadata as message properties
	for k, v := range task.Metadata {
		msg.WithProperty(k, v)
	}

	result, err := b.producer.SendSync(ctx, msg)
	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	return &taskqueue.TaskInfo{
		BrokerTaskID: result.MsgID,
		Queue:        topic,
	}, nil
}

// Close gracefully shuts down the producer.
func (b *Broker) Close() error {
	if b.producer != nil {
		return b.producer.Shutdown()
	}
	return nil
}
