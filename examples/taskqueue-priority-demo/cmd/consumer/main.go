package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jinleili-zz/nsp-platform/examples/taskqueue-priority-demo/internal/config"
	"github.com/jinleili-zz/nsp-platform/examples/taskqueue-priority-demo/internal/handler"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
	"github.com/jinleili-zz/nsp-platform/trace"
)

// TaskProcessor routes tasks to the configured handlers.
type TaskProcessor struct {
	handlers map[string]handler.TaskHandler
}

// NewTaskProcessor creates a task processor.
func NewTaskProcessor() *TaskProcessor {
	return &TaskProcessor{
		handlers: map[string]handler.TaskHandler{
			config.TaskTypeEmailSend:      handler.HandleEmailSend,
			config.TaskTypeImageProcess:   handler.HandleImageProcess,
			config.TaskTypeDataExport:     handler.HandleDataExport,
			config.TaskTypeReportGenerate: handler.HandleReportGenerate,
			config.TaskTypeNotification:   handler.HandleNotification,
		},
	}
}

// Process handles a single task.
func (p *TaskProcessor) Process(ctx context.Context, task *taskqueue.Task) error {
	tc := trace.MustTraceFromContext(ctx)

	taskHandler, exists := p.handlers[task.Type]
	if !exists {
		return fmt.Errorf("unknown task type: %s", task.Type)
	}

	startTime := time.Now()
	log.Printf("[Consumer] Processing task: type=%s queue=%s trace_id=%s",
		task.Type, task.Queue, tc.TraceID)

	if err := taskHandler(ctx, task); err != nil {
		log.Printf("[Consumer] Task failed: type=%s err=%v trace_id=%s", task.Type, err, tc.TraceID)
		return err
	}

	log.Printf("[Consumer] Task completed: type=%s duration=%v trace_id=%s",
		task.Type, time.Since(startTime), tc.TraceID)
	return nil
}

func createConsumer(cfg *config.Config, processor *TaskProcessor) taskqueue.Consumer {
	consumer := asynqbroker.NewConsumer(cfg.RedisConnOpt(), asynqbroker.ConsumerConfig{
		Concurrency: cfg.Concurrency,
		Queues:      cfg.QueueWeights,
	})

	for taskType := range processor.handlers {
		consumer.Handle(taskType, processor.Process)
		log.Printf("[Consumer] Registered handler for task type: %s", taskType)
	}
	return consumer
}

func printConsumerInfo(cfg *config.Config) {
	fmt.Println("\n========== Consumer Configuration ==========")
	fmt.Printf("Instance ID:     %s\n", cfg.InstanceID)
	fmt.Printf("Redis Address:   %s\n", strings.Join(cfg.RedisAddrs, ","))
	fmt.Printf("Concurrency:     %d\n", cfg.Concurrency)
	fmt.Println("\nQueue Weights (Priority Order):")
	fmt.Printf("  %-40s weight=%d\n", config.QueueTaskHigh, cfg.QueueWeights[config.QueueTaskHigh])
	fmt.Printf("  %-40s weight=%d\n", config.QueueTaskMedium, cfg.QueueWeights[config.QueueTaskMedium])
	fmt.Printf("  %-40s weight=%d\n", config.QueueTaskLow, cfg.QueueWeights[config.QueueTaskLow])
	fmt.Println("============================================")
}

func main() {
	log.Println("[Consumer] Starting TaskQueue Priority Demo Consumer...")

	cfg := config.DefaultConfig()
	printConsumerInfo(cfg)

	processor := NewTaskProcessor()
	consumer := createConsumer(cfg, processor)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("[Consumer] Starting with concurrency=%d...", cfg.Concurrency)
		if err := consumer.Start(ctx); err != nil {
			log.Printf("[Consumer] Error: %v", err)
		}
	}()

	log.Println("[Consumer] Running... Press Ctrl+C to exit")
	<-sigChan

	log.Println("[Consumer] Shutting down gracefully...")
	_ = consumer.Stop()
}
