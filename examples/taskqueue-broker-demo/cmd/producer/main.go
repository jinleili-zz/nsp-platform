// TaskQueue Broker Demo - Producer
// This example demonstrates the producer side of a custom task queue implementation.
// The producer submits tasks to PostgreSQL and publishes messages to the broker.
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
func (m *TaskManager) SubmitTask(ctx context.Context, name, taskType, payload string, maxRetries int) (string, error) {
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
		Queue:   store.TaskQueue,
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

	log.Printf("[Producer] Task submitted: id=%s, type=%s, broker_id=%s | trace_id=%s",
		taskID, taskType, info.BrokerTaskID, getTraceID(ctx))
	return taskID, nil
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
	// Step 4: Submit Tasks
	// ========================================
	log.Println("========================================")
	log.Println("[Producer] Submitting tasks...")
	log.Println("========================================")

	// Submit task 1: create_record
	recordParams, _ := json.Marshal(map[string]interface{}{
		"record_type": "user_registration",
		"user_id":     "U-001",
	})
	taskID1, err := manager.SubmitTask(ctx, "Create User Record", "create_record", string(recordParams), 3)
	if err != nil {
		log.Fatalf("[Producer] Failed to submit task 1: %v", err)
	}
	log.Printf("[Producer] Task 1 submitted: id=%s", taskID1)

	// Submit task 2: send_email
	emailParams, _ := json.Marshal(map[string]interface{}{
		"email":   "user@example.com",
		"subject": "Welcome!",
	})
	taskID2, err := manager.SubmitTask(ctx, "Send Welcome Email", "send_email", string(emailParams), 3)
	if err != nil {
		log.Fatalf("[Producer] Failed to submit task 2: %v", err)
	}
	log.Printf("[Producer] Task 2 submitted: id=%s", taskID2)

	// Submit task 3: always_fail (to test retry logic)
	failParams, _ := json.Marshal(map[string]interface{}{
		"fail": true,
	})
	taskID3, err := manager.SubmitTask(ctx, "Failing Task", "always_fail", string(failParams), 2)
	if err != nil {
		log.Printf("[Producer] Failed to submit failing task: %v", err)
	} else {
		log.Printf("[Producer] Task 3 (failing task) submitted: id=%s", taskID3)
	}

	// ========================================
	// Step 5: Poll for Completion
	// ========================================
	log.Println("[Producer] Polling task status...")

	allCompleted := false
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)

		task1, _ := manager.QueryTask(ctx, taskID1)
		task2, _ := manager.QueryTask(ctx, taskID2)

		log.Printf("[Producer] Task1 (%s): %s", task1.Type, task1.Status)
		log.Printf("[Producer] Task2 (%s): %s", task2.Type, task2.Status)

		if task1.Status == store.TaskStatusCompleted && task2.Status == store.TaskStatusCompleted {
			allCompleted = true
			break
		}

		if task1.Status == store.TaskStatusFailed || task2.Status == store.TaskStatusFailed {
			log.Printf("[Producer] Task failed!")
		}
	}

	if allCompleted {
		log.Println("========================================")
		log.Println("[Producer] All Tasks SUCCEEDED!")
		log.Println("========================================")
	}

	// Check failing task status
	if taskID3 != "" {
		failTask, _ := manager.QueryTask(ctx, taskID3)
		log.Printf("[Producer] Failing task final status: %s (retries=%d/%d)",
			failTask.Status, failTask.RetryCount, failTask.MaxRetries)
	}

	// ========================================
	// Step 6: Graceful Shutdown
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

	cancel()
	log.Println("[Producer] Done.")
}
