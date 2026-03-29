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

	"github.com/jinleili-zz/nsp-platform/examples/taskqueue-broker-demo/store"
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
func (m *TaskManager) SubmitTask(ctx context.Context, name, taskType, payload string, maxRetries int) (string, error) {
	return m.SubmitTaskWithPriority(ctx, name, taskType, payload, maxRetries, store.DefaultQueue)
}

// SubmitTaskWithPriority submits a new task to the given queue.
func (m *TaskManager) SubmitTaskWithPriority(ctx context.Context, name, taskType, payload string, maxRetries int, queue string) (string, error) {
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
		Reply:   &taskqueue.ReplySpec{Queue: store.CallbackQueue},
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

	log.Printf("[Producer] Task submitted: id=%s, type=%s, queue=%s, broker_id=%s | trace_id=%s",
		taskID, taskType, queue, info.BrokerTaskID, getTraceID(ctx))
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

			info, err := m.broker.Publish(ctx, &taskqueue.Task{
				Type:    task.Type,
				Payload: []byte(task.Payload),
				Queue:   task.Queue,
				Reply:   &taskqueue.ReplySpec{Queue: store.CallbackQueue},
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

	callbackConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: 2,
		Queues:      map[string]int{store.CallbackQueue: 10},
	})
	callbackConsumer.Handle("broker_task_callback", func(ctx context.Context, task *taskqueue.Task) error {
		var cb callbackMessage
		if err := json.Unmarshal(task.Payload, &cb); err != nil {
			return fmt.Errorf("failed to unmarshal callback: %w", err)
		}
		return manager.HandleCallback(ctx, &cb)
	})
	go func() {
		if err := callbackConsumer.Start(ctx); err != nil {
			log.Printf("[Producer] callback consumer stopped: %v", err)
		}
	}()

	time.Sleep(200 * time.Millisecond)

	submit := func(name, taskType, queue string, payload map[string]interface{}) {
		data, err := json.Marshal(payload)
		if err != nil {
			log.Fatalf("[Producer] marshal %s failed: %v", taskType, err)
		}
		taskID, err := manager.SubmitTaskWithPriority(ctx, name, taskType, string(data), 3, queue)
		if err != nil {
			log.Fatalf("[Producer] submit %s failed: %v", taskType, err)
		}
		log.Printf("[Producer] Submitted task %s (%s)", taskID, taskType)
	}

	submit("Payment Notification", "send_payment_notification", store.TaskQueueHigh, map[string]interface{}{
		"payment_id": "PAY-001",
		"amount":     999.00,
		"user_id":    "U-001",
	})
	submit("Deduct Inventory", "deduct_inventory", store.TaskQueueHigh, map[string]interface{}{
		"sku_id": "SKU-001",
		"count":  2,
	})
	submit("Create User Record", "create_record", store.TaskQueueMiddle, map[string]interface{}{
		"record_type": "user_registration",
		"user_id":     "U-001",
	})
	submit("Send Welcome Email", "send_email", store.TaskQueueMiddle, map[string]interface{}{
		"email":   "user@example.com",
		"subject": "Welcome!",
	})
	submit("Generate Daily Report", "generate_report", store.TaskQueueLow, map[string]interface{}{
		"report_type": "daily_sales",
		"date":        "2024-01-01",
	})
	submit("Cleanup Expired Data", "cleanup_data", store.TaskQueueLow, map[string]interface{}{
		"table_name": "session_logs",
		"days":       30,
	})
	submit("Failing Task", "always_fail", store.TaskQueueMiddle, map[string]interface{}{
		"reason": "retry-demo",
	})

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	_ = callbackConsumer.Stop()
	cancel()
}
