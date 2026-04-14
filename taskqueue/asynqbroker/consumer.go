package asynqbroker

import (
	"context"
	"sync"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jinleili-zz/nsp-platform/logger"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
)

const (
	// MinTaskCheckInterval is the minimum supported polling interval for empty queues.
	MinTaskCheckInterval = 200 * time.Millisecond
	// MaxTaskCheckInterval is the maximum supported polling interval for empty queues.
	MaxTaskCheckInterval = 2 * time.Second
)

// ConsumerConfig holds configuration for the asynq consumer.
type ConsumerConfig struct {
	// Concurrency is the number of concurrent worker goroutines.
	Concurrency int
	// Queues maps queue names to their priority weights.
	Queues map[string]int
	// StrictPriority enables strict priority ordering.
	StrictPriority bool
	// TaskCheckInterval controls empty-queue polling.
	// Zero and negative values keep asynq's default. Positive values are clamped to [MinTaskCheckInterval, MaxTaskCheckInterval].
	TaskCheckInterval time.Duration
	// Logger is the asynq logger. If nil, asynq's default logger is used.
	Logger asynq.Logger
	// RuntimeLogger is the optional repository logger for wrapper runtime logs.
	RuntimeLogger logger.Logger
}

// Consumer implements taskqueue.Consumer using asynq.
type Consumer struct {
	server   *asynq.Server
	mux      *asynq.ServeMux
	stopCh   chan struct{}
	stopOnce sync.Once
	log      logger.Logger
}

// NewConsumer creates an asynq-backed Consumer.
func NewConsumer(opt asynq.RedisConnOpt, cfg ConsumerConfig) *Consumer {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}

	runtimeLog := resolveRuntimeLogger(cfg.RuntimeLogger)
	taskCheckInterval, clamped := normalizeTaskCheckInterval(cfg.TaskCheckInterval)
	if clamped {
		runtimeLog.Warn("consumer task check interval clamped",
			"original", cfg.TaskCheckInterval.String(),
			"clamped", taskCheckInterval.String(),
		)
	}

	asynqCfg := asynq.Config{
		Concurrency:    cfg.Concurrency,
		Queues:         cfg.Queues,
		StrictPriority: cfg.StrictPriority,
	}
	if taskCheckInterval > 0 {
		asynqCfg.TaskCheckInterval = taskCheckInterval
	}
	asynqCfg.Logger = resolveFrameworkLogger(runtimeLog, cfg.Logger)

	server := asynq.NewServer(opt, asynqCfg)

	return &Consumer{
		server: server,
		mux:    asynq.NewServeMux(),
		stopCh: make(chan struct{}),
		log:    runtimeLog,
	}
}

func normalizeTaskCheckInterval(interval time.Duration) (time.Duration, bool) {
	if interval <= 0 {
		return 0, false
	}
	if interval < MinTaskCheckInterval {
		return MinTaskCheckInterval, true
	}
	if interval > MaxTaskCheckInterval {
		return MaxTaskCheckInterval, true
	}
	return interval, false
}

// Handle registers a handler for the given task type.
// The handler receives the broker-level Task reconstructed from the asynq message.
func (c *Consumer) Handle(taskType string, handler taskqueue.HandlerFunc) {
	c.mux.HandleFunc(taskType, func(ctx context.Context, t *asynq.Task) error {
		payload, traceMeta, reply, metadata := unwrapEnvelope(t.Payload())
		ctx = injectTraceFromMetadata(ctx, traceMeta)

		queueName, _ := asynq.GetQueueName(ctx)
		task := &taskqueue.Task{
			Type:     t.Type(),
			Payload:  payload,
			Queue:    queueName,
			Reply:    reply,
			Metadata: metadata,
		}

		if err := handler(ctx, task); err != nil {
			taskID, _ := asynq.GetTaskID(ctx)
			c.log.ErrorContext(ctx, "task handler returned error",
				logger.FieldTaskType, taskType,
				logger.FieldTaskID, taskID,
				logger.FieldQueue, queueName,
				logger.FieldError, err,
			)
			return err
		}
		return nil
	})
}

// Start begins consuming messages. This method blocks until Stop is called
// or the context is cancelled.
func (c *Consumer) Start(ctx context.Context) error {
	if err := c.server.Start(c.mux); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
	case <-c.stopCh:
	}

	c.server.Shutdown()
	return nil
}

// Stop gracefully shuts down the consumer.
func (c *Consumer) Stop() error {
	c.stopOnce.Do(func() {
		close(c.stopCh)
		c.server.Shutdown()
	})
	return nil
}
