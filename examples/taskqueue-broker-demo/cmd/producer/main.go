// TaskQueue Broker Demo - Producer
// This example demonstrates the producer side of a custom task queue implementation.
// The producer submits tasks to PostgreSQL, publishes messages to the broker,
// and handles callbacks to update task status.
//
// Usage:
//   go run ./cmd/producer
//
// Prerequisites:
//   - PostgreSQL running on localhost:5432
//   - Redis running on localhost:6379
//   - Database: CREATE DATABASE taskqueue_broker;

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

// TaskManager manages task lifecycle using broker for message delivery
type TaskManager struct {
	store  *store.TaskStore
	broker *asynqbroker.Broker
}

// NewTaskManager creates a new TaskManager
func NewTaskManager(s *store.TaskStore, b *asynqbroker.Broker) *TaskManager {
	return &TaskManager{
		store:  s,
		broker: b,
	}
}

// SubmitTask submits a new task: stores to DB + publishes to broker
// Uses default priority queue (middle)
func (m *TaskManager) SubmitTask(ctx context.Context, name, taskType, payload string, maxRetries int) (string, error) {
	return m.SubmitTaskWithPriority(ctx, name, taskType, payload, maxRetries, store.DefaultQueue)
}

// SubmitTaskWithPriority submits a new task with specified priority queue
func (m *TaskManager) SubmitTaskWithPriority(ctx context.Context, name, taskType, payload string, maxRetries int, queue string) (string, error) {
	taskID := uuid.New().String()
	now := time.Now()

	task := &store.Task{
		ID:         taskID,
		Name:       name,
		Type:       taskType,
		Payload:    payload,
		Status:     store.TaskStatusPending,
		MaxRetries: maxRetries,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Store to database first
	if err := m.store.Create(ctx, task); err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	// Publish to broker - use same format as workflow demo for worker compatibility
	taskPayload := map[string]interface{}{
		"task_id":     taskID,
		"resource_id": "",
		"task_params": payload,
	}
	payloadData, _ := json.Marshal(taskPayload)

	asynqTask := &taskqueue.Task{
		Type:    taskType,
		Payload: payloadData,
		Queue:   queue,
	}

	// 注入当前 trace context 的 metadata，供 worker 恢复追踪上下文
	metadata := trace.MetadataFromContext(ctx)
	if metadata != nil {
		asynqTask.Metadata = metadata
		log.Printf("[Producer] Attaching trace metadata: trace_id=%s", metadata["trace_id"])
	}

	info, err := m.broker.Publish(ctx, asynqTask)
	if err != nil {
		// Mark as failed if publish fails
		m.store.UpdateStatus(ctx, taskID, store.TaskStatusFailed, err.Error())
		return "", fmt.Errorf("failed to publish task: %w", err)
	}

	// Update broker task ID
	m.store.UpdateBrokerTaskID(ctx, taskID, info.BrokerTaskID)
	m.store.UpdateStatus(ctx, taskID, store.TaskStatusRunning, "")

	log.Printf("[Producer] Task submitted: id=%s, type=%s, queue=%s, broker_id=%s | trace_id=%s",
		taskID, taskType, queue, info.BrokerTaskID, getTraceID(ctx))
	return taskID, nil
}

// HandleCallback processes callback from worker (updates task status)
func (m *TaskManager) HandleCallback(ctx context.Context, cb *taskqueue.CallbackPayload) error {
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
		resultJSON, _ := json.Marshal(cb.Result)
		return m.store.UpdateResult(ctx, cb.TaskID, store.TaskStatusCompleted, string(resultJSON))

	case "failed":
		// Check if we should retry
		if task.RetryCount < task.MaxRetries {
			// Increment retry and re-enqueue
			m.store.IncrementRetry(ctx, cb.TaskID)
			m.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusPending, cb.ErrorMessage)

			// Re-publish to broker for retry (use same queue as original task)
			taskPayload := map[string]interface{}{
				"task_id":   task.ID,
				"task_name": task.Name,
				"payload":   task.Payload,
			}
			payloadData, _ := json.Marshal(taskPayload)

			asynqTask := &taskqueue.Task{
				Type:    task.Type,
				Payload: payloadData,
				Queue:   store.DefaultQueue, // 重试使用默认队列
			}

			// 重试时保留 trace metadata
			metadata := trace.MetadataFromContext(ctx)
			if metadata != nil {
				asynqTask.Metadata = metadata
			}

			info, err := m.broker.Publish(ctx, asynqTask)
			if err != nil {
				m.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusFailed, err.Error())
				return fmt.Errorf("failed to re-publish task: %w", err)
			}

			m.store.UpdateBrokerTaskID(ctx, cb.TaskID, info.BrokerTaskID)
			m.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusRunning, "")
			log.Printf("[Producer] Task re-queued for retry: id=%s, retry=%d/%d | trace_id=%s",
				cb.TaskID, task.RetryCount+1, task.MaxRetries, tc.TraceID)
			return nil
		}

		// No more retries
		return m.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusFailed, cb.ErrorMessage)

	default:
		return fmt.Errorf("unknown callback status: %s", cb.Status)
	}
}

// QueryTask returns task status
func (m *TaskManager) QueryTask(ctx context.Context, taskID string) (*store.Task, error) {
	return m.store.GetByID(ctx, taskID)
}

// ListTasks returns all tasks with given status
func (m *TaskManager) ListTasks(ctx context.Context, status string) ([]*store.Task, error) {
	return m.store.ListByStatus(ctx, status)
}

// Close closes resources
func (m *TaskManager) Close() error {
	return m.store.Close()
}

// getTraceID 从 context 中获取 trace_id（辅助函数）
func getTraceID(ctx context.Context) string {
	tc, ok := trace.TraceFromContext(ctx)
	if ok && tc != nil {
		return tc.TraceID
	}
	return ""
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// 获取实例 ID（用于链路追踪）
	instanceId := trace.GetInstanceId()
	log.Printf("[Producer] Instance ID: %s", instanceId)

	log.Println("========================================")
	log.Println("TaskQueue Broker Demo - Producer")
	log.Println("========================================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 为整个 demo 生成一个根 TraceContext（模拟入口请求）
	rootTC := &trace.TraceContext{
		TraceID:      trace.NewTraceID(),
		SpanId:       trace.NewSpanId(),
		ParentSpanId: "", // root span
		InstanceId:   instanceId,
		Sampled:      true,
	}
	ctx = trace.ContextWithTrace(ctx, rootTC)
	ctx = logger.ContextWithTraceID(ctx, rootTC.TraceID)
	ctx = logger.ContextWithSpanID(ctx, rootTC.SpanId)

	log.Printf("[Producer] Root TraceID: %s", rootTC.TraceID)

	// ========================================
	// Step 1: Setup Broker
	// ========================================
	redisOpt := asynq.RedisClientOpt{Addr: store.RedisAddr}
	broker := asynqbroker.NewBroker(redisOpt)
	defer broker.Close()
	log.Println("[Producer] Broker created")

	// ========================================
	// Step 2: Setup Task Store (PostgreSQL)
	// ========================================
	taskStore, err := store.NewTaskStore(store.PgDSN)
	if err != nil {
		log.Fatalf("[Producer] Failed to connect to database: %v", err)
	}
	defer taskStore.Close()

	if err := taskStore.Migrate(ctx); err != nil {
		log.Fatalf("[Producer] Failed to migrate: %v", err)
	}
	log.Println("[Producer] Database migrated")

	// ========================================
	// Step 3: Setup Task Manager
	// ========================================
	manager := NewTaskManager(taskStore, broker)

	// ========================================
	// Step 4: Setup Callback Consumer
	// ========================================
	callbackConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: 2,
		Queues:      map[string]int{store.CallbackQueue: 10},
	})

	callbackConsumer.HandleRaw("broker_task_callback", func(ctx context.Context, t *asynq.Task) error {
		var cb taskqueue.CallbackPayload
		if err := json.Unmarshal(t.Payload(), &cb); err != nil {
			return fmt.Errorf("failed to unmarshal callback: %w", err)
		}

		// 从 context 中提取 TraceContext
		tc := trace.MustTraceFromContext(ctx)
		log.Printf("[Producer] Processing callback for task_id=%s | trace_id=%s | status=%s",
			cb.TaskID, tc.TraceID, cb.Status)

		return manager.HandleCallback(ctx, &cb)
	})

	// Start callback consumer
	go callbackConsumer.Start(ctx)
	log.Println("[Producer] Callback consumer started")

	// Wait for consumer to be ready
	time.Sleep(1 * time.Second)

	// ========================================
	// Step 5: Submit Tasks with Different Priorities
	// ========================================
	log.Println("========================================")
	log.Println("[Producer] Submitting tasks with different priorities...")
	log.Println("========================================")

	// --- High Priority Tasks (紧急任务) ---
	log.Println("\n[High Priority] Submitting urgent tasks...")
	
	// 高优先级任务 1：支付通知（需要立即处理）
	paymentParams, _ := json.Marshal(map[string]interface{}{
		"payment_id": "PAY-001",
		"amount":     999.00,
		"user_id":    "U-001",
	})
	highTaskID1, err := manager.SubmitTaskWithPriority(ctx, "Payment Notification", "send_payment_notification", string(paymentParams), 3, store.TaskQueueHigh)
	if err != nil {
		log.Fatalf("[Producer] Failed to submit high priority task 1: %v", err)
	}
	log.Printf("[Producer] High Priority Task 1 submitted: id=%s", highTaskID1)

	// 高优先级任务 2：库存扣减（下单后立即可用）
	inventoryParams, _ := json.Marshal(map[string]interface{}{
		"sku_id": "SKU-001",
		"count":  2,
	})
	highTaskID2, err := manager.SubmitTaskWithPriority(ctx, "Deduct Inventory", "deduct_inventory", string(inventoryParams), 3, store.TaskQueueHigh)
	if err != nil {
		log.Fatalf("[Producer] Failed to submit high priority task 2: %v", err)
	}
	log.Printf("[Producer] High Priority Task 2 submitted: id=%s", highTaskID2)

	// --- Middle Priority Tasks (普通任务) ---
	log.Println("\n[Middle Priority] Submitting normal tasks...")
	
	// 中优先级任务 1：创建用户记录
	recordParams, _ := json.Marshal(map[string]interface{}{
		"record_type": "user_registration",
		"user_id":     "U-001",
	})
	middleTaskID1, err := manager.SubmitTaskWithPriority(ctx, "Create User Record", "create_record", string(recordParams), 3, store.TaskQueueMiddle)
	if err != nil {
		log.Fatalf("[Producer] Failed to submit middle priority task 1: %v", err)
	}
	log.Printf("[Producer] Middle Priority Task 1 submitted: id=%s", middleTaskID1)

	// 中优先级任务 2：发送欢迎邮件
	emailParams, _ := json.Marshal(map[string]interface{}{
		"email":   "user@example.com",
		"subject": "Welcome!",
	})
	middleTaskID2, err := manager.SubmitTaskWithPriority(ctx, "Send Welcome Email", "send_email", string(emailParams), 3, store.TaskQueueMiddle)
	if err != nil {
		log.Fatalf("[Producer] Failed to submit middle priority task 2: %v", err)
	}
	log.Printf("[Producer] Middle Priority Task 2 submitted: id=%s", middleTaskID2)

	// --- Low Priority Tasks (低优先级任务) ---
	log.Println("\n[Low Priority] Submitting background tasks...")
	
	// 低优先级任务 1：生成报表
	reportParams, _ := json.Marshal(map[string]interface{}{
		"report_type": "daily_sales",
		"date":        "2024-01-01",
	})
	lowTaskID1, err := manager.SubmitTaskWithPriority(ctx, "Generate Daily Report", "generate_report", string(reportParams), 3, store.TaskQueueLow)
	if err != nil {
		log.Fatalf("[Producer] Failed to submit low priority task 1: %v", err)
	}
	log.Printf("[Producer] Low Priority Task 1 submitted: id=%s", lowTaskID1)

	// 低优先级任务 2：清理过期数据
	cleanupParams, _ := json.Marshal(map[string]interface{}{
		"table_name": "temp_logs",
		"days":       30,
	})
	lowTaskID2, err := manager.SubmitTaskWithPriority(ctx, "Cleanup Expired Data", "cleanup_data", string(cleanupParams), 3, store.TaskQueueLow)
	if err != nil {
		log.Fatalf("[Producer] Failed to submit low priority task 2: %v", err)
	}
	log.Printf("[Producer] Low Priority Task 2 submitted: id=%s", lowTaskID2)

	// --- Retry Test Task (always fail) ---
	failParams, _ := json.Marshal(map[string]interface{}{
		"fail": true,
	})
	failTaskID, err := manager.SubmitTaskWithPriority(ctx, "Failing Task", "always_fail", string(failParams), 2, store.TaskQueueMiddle)
	if err != nil {
		log.Printf("[Producer] Failed to submit failing task: %v", err)
	} else {
		log.Printf("[Producer] Retry Test Task submitted: id=%s", failTaskID)
	}

	log.Println("\n[Producer] All tasks submitted!")
	log.Println("========================================")

	// ========================================
	// Step 6: Poll for Completion
	// ========================================
	log.Println("[Producer] Polling task status...")

	allCompleted := false
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)

		// Check high priority tasks
		highTask1, _ := manager.QueryTask(ctx, highTaskID1)
		highTask2, _ := manager.QueryTask(ctx, highTaskID2)
		
		// Check middle priority tasks
		middleTask1, _ := manager.QueryTask(ctx, middleTaskID1)
		middleTask2, _ := manager.QueryTask(ctx, middleTaskID2)
		
		// Check low priority tasks
		lowTask1, _ := manager.QueryTask(ctx, lowTaskID1)
		lowTask2, _ := manager.QueryTask(ctx, lowTaskID2)

		log.Printf("[Producer] High   - Task1 (%s): %s, Task2 (%s): %s", 
			highTask1.Type, highTask1.Status, highTask2.Type, highTask2.Status)
		log.Printf("[Producer] Middle - Task1 (%s): %s, Task2 (%s): %s", 
			middleTask1.Type, middleTask1.Status, middleTask2.Type, middleTask2.Status)
		log.Printf("[Producer] Low    - Task1 (%s): %s, Task2 (%s): %s", 
			lowTask1.Type, lowTask1.Status, lowTask2.Type, lowTask2.Status)

		// Check if all tasks completed
		if highTask1.Status == store.TaskStatusCompleted && 
		   highTask2.Status == store.TaskStatusCompleted &&
		   middleTask1.Status == store.TaskStatusCompleted && 
		   middleTask2.Status == store.TaskStatusCompleted &&
		   lowTask1.Status == store.TaskStatusCompleted && 
		   lowTask2.Status == store.TaskStatusCompleted {
			allCompleted = true
			break
		}

		// Check for failures
		if highTask1.Status == store.TaskStatusFailed || highTask2.Status == store.TaskStatusFailed {
			log.Printf("[Producer] High priority task failed!")
		}
	}

	if allCompleted {
		log.Println("========================================")
		log.Println("[Producer] All Tasks SUCCEEDED!")
		log.Println("========================================")
	}

	// Check failing task status
	if failTaskID != "" {
		failTask, _ := manager.QueryTask(ctx, failTaskID)
		log.Printf("[Producer] Failing task final status: %s (retries=%d/%d)",
			failTask.Status, failTask.RetryCount, failTask.MaxRetries)
	}

	// ========================================
	// Step 7: Graceful Shutdown
	// ========================================
	log.Println("[Producer] Press Ctrl+C to exit...")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		log.Println("[Producer] Shutdown signal received")
	case <-time.After(10 * time.Second):
		log.Println("[Producer] Auto-exit after 10 seconds")
	}

	callbackConsumer.Stop()
	cancel()
	log.Println("[Producer] Done.")
}
