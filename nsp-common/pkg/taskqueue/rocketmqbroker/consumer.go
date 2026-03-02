// Package rocketmqbroker provides RocketMQ consumer implementation
package rocketmqbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/apache/rocketmq-client-go/v2"
	"github.com/apache/rocketmq-client-go/v2/consumer"
	"github.com/apache/rocketmq-client-go/v2/primitive"
	"github.com/yourorg/nsp-common/pkg/taskqueue"
)

// ConsumerConfig holds configuration for the RocketMQ consumer.
type ConsumerConfig struct {
	// NameServer is the RocketMQ name server address
	NameServer string
	// GroupName is the consumer group name
	GroupName string
	// Queues maps topic names to their subscription expressions (tags)
	// Example: {"tasks": "*", "tasks_high": "*", "callbacks": "*"}
	// "*" means subscribe to all tags, or use specific tags like "task_type_1||task_type_2"
	Queues map[string]string
	// Concurrency is the number of concurrent consumption goroutines
	Concurrency int
	// MaxReconsumeTimes is the max retry times for failed messages
	MaxReconsumeTimes int32
}

// Consumer implements taskqueue.Consumer using Apache RocketMQ.
type Consumer struct {
	pushConsumer rocketmq.PushConsumer
	config       *ConsumerConfig
	handlers     map[string]taskqueue.HandlerFunc
	mu           sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewConsumer creates a RocketMQ-backed Consumer.
func NewConsumer(cfg *ConsumerConfig) (*Consumer, error) {
	if cfg.NameServer == "" {
		return nil, fmt.Errorf("NameServer is required")
	}
	if cfg.GroupName == "" {
		cfg.GroupName = "taskqueue_consumer_group"
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 10
	}
	if cfg.MaxReconsumeTimes == 0 {
		cfg.MaxReconsumeTimes = 3
	}

	ctx, cancel := context.WithCancel(context.Background())

	c := &Consumer{
		config:   cfg,
		handlers: make(map[string]taskqueue.HandlerFunc),
		ctx:      ctx,
		cancel:   cancel,
	}

	return c, nil
}

// Handle registers a handler for the given task type.
// In RocketMQ, task type is mapped to message tag.
func (c *Consumer) Handle(taskType string, handler taskqueue.HandlerFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[taskType] = handler
}

// Start begins consuming messages from subscribed topics.
// This method blocks until Stop is called or context is cancelled.
func (c *Consumer) Start(ctx context.Context) error {
	c.mu.RLock()
	if len(c.handlers) == 0 {
		c.mu.RUnlock()
		return fmt.Errorf("no handlers registered")
	}
	c.mu.RUnlock()

	// Create push consumer
	pushConsumer, err := rocketmq.NewPushConsumer(
		consumer.WithNameServer([]string{c.config.NameServer}),
		consumer.WithGroupName(c.config.GroupName),
		consumer.WithConsumeFromWhere(consumer.ConsumeFromLastOffset),
		consumer.WithConsumerModel(consumer.Clustering),
		consumer.WithConsumeGoroutineNums(c.config.Concurrency),
		consumer.WithMaxReconsumeTimes(c.config.MaxReconsumeTimes),
	)
	if err != nil {
		return fmt.Errorf("failed to create push consumer: %w", err)
	}

	c.pushConsumer = pushConsumer

	// Subscribe to all configured topics
	for topic, selector := range c.config.Queues {
		err := pushConsumer.Subscribe(topic, consumer.MessageSelector{
			Type:       consumer.TAG,
			Expression: selector,
		}, func(ctx context.Context, msgs ...*primitive.MessageExt) (consumer.ConsumeResult, error) {
			return c.handleMessages(ctx, msgs...)
		})
		if err != nil {
			return fmt.Errorf("failed to subscribe topic %s: %w", topic, err)
		}
		log.Printf("[rocketmqbroker] subscribed to topic: %s, selector: %s", topic, selector)
	}

	// Start consuming
	if err := pushConsumer.Start(); err != nil {
		return fmt.Errorf("failed to start consumer: %w", err)
	}

	log.Printf("[rocketmqbroker] consumer started, group: %s", c.config.GroupName)

	// Block until context is cancelled or Stop is called
	select {
	case <-ctx.Done():
		log.Println("[rocketmqbroker] context cancelled, stopping consumer")
	case <-c.ctx.Done():
		log.Println("[rocketmqbroker] consumer stopped")
	}

	return nil
}

// Stop gracefully shuts down the consumer.
func (c *Consumer) Stop() error {
	c.cancel()
	if c.pushConsumer != nil {
		return c.pushConsumer.Shutdown()
	}
	return nil
}

// handleMessages processes consumed messages.
func (c *Consumer) handleMessages(ctx context.Context, msgs ...*primitive.MessageExt) (consumer.ConsumeResult, error) {
	for _, msg := range msgs {
		// Task type is stored in message tag
		taskType := msg.GetTags()
		
		c.mu.RLock()
		handler, exists := c.handlers[taskType]
		c.mu.RUnlock()

		if !exists {
			log.Printf("[rocketmqbroker] no handler for task type: %s, topic: %s", taskType, msg.Topic)
			// Return success to avoid infinite retry for unhandled types
			continue
		}

		// Unmarshal payload
		var raw struct {
			TaskID     string `json:"task_id"`
			ResourceID string `json:"resource_id"`
			TaskParams string `json:"task_params"`
		}
		if err := json.Unmarshal(msg.Body, &raw); err != nil {
			log.Printf("[rocketmqbroker] failed to unmarshal payload: %v, msgId: %s", err, msg.MsgId)
			// Return failure to trigger retry
			return consumer.ConsumeRetryLater, err
		}

		payload := &taskqueue.TaskPayload{
			TaskID:     raw.TaskID,
			TaskType:   taskType,
			ResourceID: raw.ResourceID,
			Params:     []byte(raw.TaskParams),
		}

		// Invoke handler
		result, err := handler(ctx, payload)
		if err != nil {
			log.Printf("[rocketmqbroker] handler error: type=%s, task_id=%s, msgId=%s, err=%v",
				taskType, raw.TaskID, msg.MsgId, err)
			// Return retry to let RocketMQ handle retry logic
			return consumer.ConsumeRetryLater, err
		}

		_ = result
		log.Printf("[rocketmqbroker] message consumed: type=%s, task_id=%s, msgId=%s",
			taskType, raw.TaskID, msg.MsgId)
	}

	return consumer.ConsumeSuccess, nil
}
