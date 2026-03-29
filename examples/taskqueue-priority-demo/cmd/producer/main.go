package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/jinleili-zz/nsp-platform/examples/taskqueue-priority-demo/store"
	"github.com/jinleili-zz/nsp-platform/logger"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
	"github.com/jinleili-zz/nsp-platform/trace"
)

type callbackMessage struct {
	TaskID       string          `json:"task_id"`
	Status       string          `json:"status"`
	Result       json.RawMessage `json:"result,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
}

// TaskManager manages task lifecycle using broker for message delivery.
type TaskManager struct {
	store  *store.TaskStore
	broker *asynqbroker.Broker
}

// NewTaskManager creates a new TaskManager.
func NewTaskManager(s *store.TaskStore, b *asynqbroker.Broker) *TaskManager {
	return &TaskManager{store: s, broker: b}
}

// SubmitTask submits a new task using the default queue.
func (m *TaskManager) SubmitTask(ctx context.Context, name, taskType, payload string, maxRetries int, callbackQueue string) (string, error) {
	return m.SubmitTaskWithPriority(ctx, name, taskType, payload, maxRetries, store.DefaultQueue, callbackQueue)
}

// SubmitTaskWithPriority submits a new task to the given queue with a specified callback queue.
func (m *TaskManager) SubmitTaskWithPriority(ctx context.Context, name, taskType, payload string, maxRetries int, queue string, callbackQueue string) (string, error) {
	taskID := uuid.New().String()
	now := time.Now()

	record := &store.Task{
		ID:         taskID,
		Name:       name,
		Type:       taskType,
		Queue:      queue,
		Payload:    payload,
		Status:     store.TaskStatusPending,
		MaxRetries: maxRetries,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := m.store.Create(ctx, record); err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	msg := &taskqueue.Task{
		Type:    taskType,
		Payload: []byte(payload),
		Queue:   queue,
		Reply:   &taskqueue.ReplySpec{Queue: callbackQueue},
		Metadata: map[string]string{
			"task_id": taskID,
		},
	}

	info, err := m.broker.Publish(ctx, msg)
	if err != nil {
		_ = m.store.UpdateStatus(ctx, taskID, store.TaskStatusFailed, err.Error())
		return "", fmt.Errorf("failed to publish task: %w", err)
	}

	_ = m.store.UpdateBrokerTaskID(ctx, taskID, info.BrokerTaskID)
	_ = m.store.UpdateStatus(ctx, taskID, store.TaskStatusRunning, "")

	log.Printf("[Producer] Task submitted: id=%s, type=%s, queue=%s, callback=%s, broker_id=%s | trace_id=%s",
		taskID, taskType, queue, callbackQueue, info.BrokerTaskID, getTraceID(ctx))
	return taskID, nil
}

// HandleCallback updates local task state from worker replies.
func (m *TaskManager) HandleCallback(ctx context.Context, cb *callbackMessage) error {
	tc := trace.MustTraceFromContext(ctx)
	log.Printf("[Producer] Callback received: task_id=%s, status=%s | trace_id=%s",
		cb.TaskID, cb.Status, tc.TraceID)

	task, err := m.store.GetByID(ctx, cb.TaskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}
	if task == nil {
		return fmt.Errorf("task not found: %s", cb.TaskID)
	}

	switch cb.Status {
	case "completed":
		return m.store.UpdateResult(ctx, cb.TaskID, store.TaskStatusCompleted, string(cb.Result))
	case "failed":
		if task.RetryCount < task.MaxRetries {
			_ = m.store.IncrementRetry(ctx, cb.TaskID)
			_ = m.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusPending, cb.ErrorMessage)

			// Determine callback queue from original task metadata
			callbackQueue := ""
			if task.Type == "send:payment:notification" || task.Type == "deduct:inventory" ||
				task.Type == "generate:report" || task.Type == "export:data" {
				callbackQueue = store.CallbackQueueOrder
			} else {
				callbackQueue = store.CallbackQueueNotify
			}

			info, err := m.broker.Publish(ctx, &taskqueue.Task{
				Type:    task.Type,
				Payload: []byte(task.Payload),
				Queue:   task.Queue,
				Reply:   &taskqueue.ReplySpec{Queue: callbackQueue},
				Metadata: map[string]string{
					"task_id": task.ID,
				},
			})
			if err != nil {
				_ = m.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusFailed, err.Error())
				return fmt.Errorf("failed to re-publish task: %w", err)
			}

			_ = m.store.UpdateBrokerTaskID(ctx, cb.TaskID, info.BrokerTaskID)
			_ = m.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusRunning, "")
			log.Printf("[Producer] Task re-queued for retry: id=%s, retry=%d/%d | trace_id=%s",
				cb.TaskID, task.RetryCount+1, task.MaxRetries, tc.TraceID)
			return nil
		}
		return m.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusFailed, cb.ErrorMessage)
	default:
		return fmt.Errorf("unknown callback status: %s", cb.Status)
	}
}

// QueryTask returns task status.
func (m *TaskManager) QueryTask(ctx context.Context, taskID string) (*store.Task, error) {
	return m.store.GetByID(ctx, taskID)
}

// ListTasks returns all tasks with the given status.
func (m *TaskManager) ListTasks(ctx context.Context, status string) ([]*store.Task, error) {
	return m.store.ListByStatus(ctx, status)
}

// Close closes resources.
func (m *TaskManager) Close() error {
	return m.store.Close()
}

func getTraceID(ctx context.Context) string {
	tc, ok := trace.TraceFromContext(ctx)
	if ok && tc != nil {
		return tc.TraceID
	}
	return ""
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	instanceID := trace.GetInstanceId()
	log.Printf("[Producer] Instance ID: %s", instanceID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rootTC := &trace.TraceContext{
		TraceID:    trace.NewTraceID(),
		SpanId:     trace.NewSpanId(),
		InstanceId: instanceID,
		Sampled:    true,
	}
	ctx = trace.ContextWithTrace(ctx, rootTC)
	ctx = logger.ContextWithTraceID(ctx, rootTC.TraceID)
	ctx = logger.ContextWithSpanID(ctx, rootTC.SpanId)

	redisOpt := asynq.RedisClientOpt{Addr: store.MustRedisAddr()}
	broker := asynqbroker.NewBroker(redisOpt)
	defer broker.Close()

	taskStore, err := store.NewTaskStore()
	if err != nil {
		log.Fatalf("[Producer] Failed to create task store: %v", err)
	}
	defer taskStore.Close()

	if err := taskStore.Migrate(ctx); err != nil {
		log.Fatalf("[Producer] Failed to initialize task store: %v", err)
	}

	manager := NewTaskManager(taskStore, broker)

	// Start dual callback consumers: one for order callbacks, one for notify callbacks
	startCallbackConsumer := func(queueName, label string) {
		cbConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
			Concurrency: 2,
			Queues:      map[string]int{queueName: 10},
		})
		cbConsumer.Handle("broker_task_callback", func(ctx context.Context, task *taskqueue.Task) error {
			var cb callbackMessage
			if err := json.Unmarshal(task.Payload, &cb); err != nil {
				return fmt.Errorf("failed to unmarshal callback: %w", err)
			}
			return manager.HandleCallback(ctx, &cb)
		})
		go func() {
			if err := cbConsumer.Start(ctx); err != nil {
				log.Printf("[Producer] %s callback consumer stopped: %v", label, err)
			}
		}()
		log.Printf("[Producer] Started %s callback consumer on %s", label, queueName)
	}

	startCallbackConsumer(store.CallbackQueueOrder, "Order")
	startCallbackConsumer(store.CallbackQueueNotify, "Notify")

	time.Sleep(200 * time.Millisecond)

	// Submit tasks with priority and callback routing
	submit := func(name, taskType, queue, callbackQueue string, payload map[string]interface{}) {
		data, err := json.Marshal(payload)
		if err != nil {
			log.Fatalf("[Producer] marshal %s failed: %v", taskType, err)
		}
		taskID, err := manager.SubmitTaskWithPriority(ctx, name, taskType, string(data), 3, queue, callbackQueue)
		if err != nil {
			log.Fatalf("[Producer] submit %s failed: %v", taskType, err)
		}
		log.Printf("[Producer] Submitted task %s (%s) -> %s", taskID, taskType, callbackQueue)
	}

	// 订单类任务 -> order callback queue
	submit("Payment Notification", "send:payment:notification", store.TaskQueueHigh, store.CallbackQueueOrder, map[string]interface{}{
		"payment_id": "PAY-001",
		"amount":     999.00,
		"user_id":    "U-001",
	})
	submit("Deduct Inventory", "deduct:inventory", store.TaskQueueHigh, store.CallbackQueueOrder, map[string]interface{}{
		"sku_id": "SKU-001",
		"count":  2,
	})
	submit("Generate Report", "generate:report", store.TaskQueueLow, store.CallbackQueueOrder, map[string]interface{}{
		"report_type": "daily_sales",
		"date":        "2024-01-01",
	})
	submit("Export Data", "export:data", store.TaskQueueLow, store.CallbackQueueOrder, map[string]interface{}{
		"format":  "csv",
		"user_id": "U-001",
	})

	// 通知类任务 -> notify callback queue
	submit("Send Email", "send:email", store.TaskQueueMiddle, store.CallbackQueueNotify, map[string]interface{}{
		"email":   "user@example.com",
		"subject": "Welcome!",
	})
	submit("Send Notification", "send:notification", store.TaskQueueMiddle, store.CallbackQueueNotify, map[string]interface{}{
		"channel":   "sms",
		"recipient": "+1234567890",
	})
	submit("Process Image", "process:image", store.TaskQueueMiddle, store.CallbackQueueNotify, map[string]interface{}{
		"image_url": "https://example.com/img.jpg",
		"operation": "resize",
	})

	// Failing task for retry demo (order type)
	submit("Always Fail", "always:fail", store.TaskQueueMiddle, store.CallbackQueueOrder, map[string]interface{}{
		"reason": "retry-demo",
	})

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	cancel()
}
