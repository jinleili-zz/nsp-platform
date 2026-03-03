package asynqbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/hibiken/asynq"
	"github.com/yourorg/nsp-common/pkg/taskqueue"
)

// ConsumerConfig holds configuration for the asynq consumer.
type ConsumerConfig struct {
	// Concurrency is the number of concurrent worker goroutines.
	Concurrency int
	// Queues maps queue names to their priority weights.
	Queues map[string]int
	// StrictPriority enables strict priority ordering.
	StrictPriority bool
	// Logger is the asynq logger. If nil, asynq's default logger is used.
	Logger asynq.Logger
}

// Consumer implements taskqueue.Consumer using asynq.
type Consumer struct {
	server *asynq.Server
	mux    *asynq.ServeMux
}

// NewConsumer creates an asynq-backed Consumer.
func NewConsumer(opt asynq.RedisConnOpt, cfg ConsumerConfig) *Consumer {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}

	asynqCfg := asynq.Config{
		Concurrency:    cfg.Concurrency,
		Queues:         cfg.Queues,
		StrictPriority: cfg.StrictPriority,
	}
	if cfg.Logger != nil {
		asynqCfg.Logger = cfg.Logger
	}

	server := asynq.NewServer(opt, asynqCfg)

	return &Consumer{
		server: server,
		mux:    asynq.NewServeMux(),
	}
}

// Handle registers a handler for the given task type.
// The handler receives a decoded TaskPayload (not the raw asynq.Task).
func (c *Consumer) Handle(taskType string, handler taskqueue.HandlerFunc) {
	c.mux.HandleFunc(taskType, func(ctx context.Context, t *asynq.Task) error {
		var raw struct {
			TaskID     string `json:"task_id"`
			ResourceID string `json:"resource_id"`
			TaskParams string `json:"task_params"`
		}
		if err := json.Unmarshal(t.Payload(), &raw); err != nil {
			return fmt.Errorf("failed to unmarshal task payload: %w", err)
		}

		payload := &taskqueue.TaskPayload{
			TaskID:     raw.TaskID,
			TaskType:   t.Type(),
			ResourceID: raw.ResourceID,
			Params:     []byte(raw.TaskParams),
		}

		result, err := handler(ctx, payload)
		if err != nil {
			log.Printf("[asynqbroker] handler error: type=%s, task_id=%s, err=%v", taskType, raw.TaskID, err)
			return err
		}

		_ = result // result is used by the caller via CallbackSender
		return nil
	})
}

// HandleRaw registers a raw asynq handler. This is useful for special task types
// like "task_callback" where the payload format differs.
func (c *Consumer) HandleRaw(taskType string, handler func(context.Context, *asynq.Task) error) {
	c.mux.HandleFunc(taskType, handler)
}

// Start begins consuming messages. This method blocks until Stop is called
// or the context is cancelled.
func (c *Consumer) Start(ctx context.Context) error {
	// Start a goroutine to watch for context cancellation
	go func() {
		<-ctx.Done()
		c.server.Shutdown()
	}()
	return c.server.Run(c.mux)
}

// Stop gracefully shuts down the consumer.
func (c *Consumer) Stop() error {
	c.server.Shutdown()
	return nil
}
