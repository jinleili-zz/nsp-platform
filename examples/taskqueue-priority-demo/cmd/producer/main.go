package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jinleili-zz/nsp-platform/examples/taskqueue-priority-demo/internal/config"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
)

// TaskProducer sends tasks to the configured broker.
type TaskProducer struct {
	broker    taskqueue.Broker
	config    *config.Config
	inspector taskqueue.Inspector
}

// NewTaskProducer creates a new producer.
func NewTaskProducer(cfg *config.Config) (*TaskProducer, error) {
	redisOpt := cfg.RedisConnOpt()
	return &TaskProducer{
		broker:    asynqbroker.NewBroker(redisOpt),
		config:    cfg,
		inspector: asynqbroker.NewInspector(redisOpt),
	}, nil
}

// Close closes producer resources.
func (p *TaskProducer) Close() error {
	if p.broker != nil {
		_ = p.broker.Close()
	}
	if p.inspector != nil {
		_ = p.inspector.Close()
	}
	return nil
}

// SendTask sends a task to the queue matching the given priority.
func (p *TaskProducer) SendTask(ctx context.Context, taskType string, params map[string]interface{}, priority string) (*taskqueue.TaskInfo, error) {
	return p.send(ctx, taskType, params, priority, "")
}

// SendTaskWithTimeout sends a task and records a timeout hint in metadata.
func (p *TaskProducer) SendTaskWithTimeout(ctx context.Context, taskType string, params map[string]interface{}, priority string, timeout time.Duration) (*taskqueue.TaskInfo, error) {
	return p.send(ctx, taskType, params, priority, timeout.String())
}

func (p *TaskProducer) send(ctx context.Context, taskType string, params map[string]interface{}, priority string, timeout string) (*taskqueue.TaskInfo, error) {
	payloadBytes, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	metadata := map[string]string{
		"producer_id": p.config.InstanceID,
		"priority":    priority,
		"send_time":   time.Now().Format(time.RFC3339),
	}
	if timeout != "" {
		metadata["timeout"] = timeout
	}

	task := &taskqueue.Task{
		Type:     taskType,
		Payload:  payloadBytes,
		Queue:    config.GetQueueByPriority(priority),
		Priority: getPriorityValue(priority),
		Metadata: metadata,
	}

	info, err := p.broker.Publish(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("failed to publish task: %w", err)
	}

	log.Printf("[Producer] Task sent: type=%s queue=%s priority=%s task_id=%s",
		taskType, task.Queue, priority, info.BrokerTaskID)
	return info, nil
}

// GetQueueStats prints queue statistics.
func (p *TaskProducer) GetQueueStats() error {
	queues := []string{config.QueueTaskHigh, config.QueueTaskMedium, config.QueueTaskLow, config.QueueResultCallback}
	ctx := context.Background()

	fmt.Println("\n========== Queue Statistics ==========")
	for _, queue := range queues {
		stats, err := p.inspector.GetQueueStats(ctx, queue)
		if err != nil {
			fmt.Printf("Queue: %-40s | Error: %v\n", queue, err)
			continue
		}
		fmt.Printf("Queue: %-40s | Pending: %3d | Active: %3d | Completed: %3d | Failed: %3d\n",
			queue, stats.Pending, stats.Active, stats.Completed, stats.Failed)
	}
	fmt.Println("======================================")
	return nil
}

func getPriorityValue(priority string) taskqueue.Priority {
	switch priority {
	case "high":
		return taskqueue.PriorityHigh
	case "medium":
		return taskqueue.PriorityNormal
	case "low":
		return taskqueue.PriorityLow
	default:
		return taskqueue.PriorityNormal
	}
}

func simulateTaskSending(producer *TaskProducer) {
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		_, err := producer.SendTask(ctx, config.TaskTypeEmailSend, map[string]interface{}{
			"to":      fmt.Sprintf("user%d@example.com", i),
			"subject": fmt.Sprintf("High Priority Email #%d", i),
			"body":    "This is a high priority email",
		}, "high")
		if err != nil {
			log.Printf("Failed to send high priority task: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	for i := 1; i <= 5; i++ {
		_, err := producer.SendTask(ctx, config.TaskTypeImageProcess, map[string]interface{}{
			"image_url": fmt.Sprintf("https://example.com/images/img%d.jpg", i),
			"operation": "resize",
			"width":     800,
			"height":    600,
		}, "medium")
		if err != nil {
			log.Printf("Failed to send medium priority task: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	for i := 1; i <= 3; i++ {
		_, err := producer.SendTask(ctx, config.TaskTypeDataExport, map[string]interface{}{
			"format":    "csv",
			"date_from": "2024-01-01",
			"date_to":   "2024-12-31",
			"user_id":   fmt.Sprintf("user_%d", i),
		}, "low")
		if err != nil {
			log.Printf("Failed to send low priority task: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	_, err := producer.SendTaskWithTimeout(ctx, config.TaskTypeReportGenerate, map[string]interface{}{
		"report_type": "monthly",
		"month":       "2024-12",
		"department":  "sales",
	}, "high", 60*time.Second)
	if err != nil {
		log.Printf("Failed to send timeout task: %v", err)
	}
}

func main() {
	log.Println("[Producer] Starting TaskQueue Priority Demo Producer...")

	cfg := config.DefaultConfig()
	log.Printf("[Producer] Config: redis=%s instance_id=%s", strings.Join(cfg.RedisAddrs, ","), cfg.InstanceID)

	producer, err := NewTaskProducer(cfg)
	if err != nil {
		log.Fatalf("Failed to create producer: %v", err)
	}
	defer producer.Close()

	simulateTaskSending(producer)
	time.Sleep(500 * time.Millisecond)
	_ = producer.GetQueueStats()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("[Producer] Running... Press Ctrl+C to exit")
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = producer.GetQueueStats()
		case sig := <-sigChan:
			log.Printf("[Producer] Received signal: %v, shutting down...", sig)
			return
		}
	}
}
