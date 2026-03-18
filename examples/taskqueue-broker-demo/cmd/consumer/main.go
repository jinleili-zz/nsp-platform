// TaskQueue Broker Demo - Consumer
// This example demonstrates the consumer side of a custom task queue implementation.
// The consumer handles actual tasks and callbacks, updating task status in PostgreSQL.
//
// Usage:
//   go run ./cmd/consumer
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

	"github.com/hibiken/asynq"

	"github.com/jinleili-zz/nsp-platform/examples/taskqueue-broker-demo/store"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
	"github.com/jinleili-zz/nsp-platform/trace"
)

// CallbackSender wraps broker for sending callbacks
type CallbackSender struct {
	broker *asynqbroker.Broker
	queue  string
}

// NewCallbackSender creates a new CallbackSender
func NewCallbackSender(broker *asynqbroker.Broker, queue string) *CallbackSender {
	return &CallbackSender{broker: broker, queue: queue}
}

// Success sends a success callback
func (s *CallbackSender) Success(ctx context.Context, taskID string, result interface{}) error {
	return s.send(ctx, taskID, "completed", result, "")
}

// Fail sends a failure callback
func (s *CallbackSender) Fail(ctx context.Context, taskID string, errMsg string) error {
	return s.send(ctx, taskID, "failed", nil, errMsg)
}

func (s *CallbackSender) send(ctx context.Context, taskID, status string, result interface{}, errorMsg string) error {
	cb := &taskqueue.CallbackPayload{
		TaskID:       taskID,
		Status:       status,
		Result:       result,
		ErrorMessage: errorMsg,
	}
	data, _ := json.Marshal(cb)

	task := &taskqueue.Task{
		Type:    "broker_task_callback",
		Payload: data,
		Queue:   s.queue,
	}

	// 注入当前 trace context 的 metadata
	metadata := trace.MetadataFromContext(ctx)
	if metadata != nil {
		task.Metadata = metadata
	}

	_, err := s.broker.Publish(ctx, task)
	if err != nil {
		return fmt.Errorf("failed to publish callback: %w", err)
	}

	tc := trace.MustTraceFromContext(ctx)
	log.Printf("[Callback] Sent: task_id=%s, status=%s | trace_id=%s", taskID, status, tc.TraceID)
	return nil
}

// CallbackHandler handles callbacks from workers
type CallbackHandler struct {
	store  *store.TaskStore
	broker *asynqbroker.Broker
}

// NewCallbackHandler creates a new CallbackHandler
func NewCallbackHandler(s *store.TaskStore, b *asynqbroker.Broker) *CallbackHandler {
	return &CallbackHandler{store: s, broker: b}
}

// Handle processes callback from worker (custom implementation)
func (h *CallbackHandler) Handle(ctx context.Context, cb *taskqueue.CallbackPayload) error {
	tc := trace.MustTraceFromContext(ctx)
	log.Printf("[Callback] Callback received: task_id=%s, status=%s | trace_id=%s",
		cb.TaskID, cb.Status, tc.TraceID)

	task, err := h.store.GetByID(ctx, cb.TaskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}
	if task == nil {
		return fmt.Errorf("task not found: %s", cb.TaskID)
	}

	switch cb.Status {
	case "completed":
		resultJSON, _ := json.Marshal(cb.Result)
		return h.store.UpdateResult(ctx, cb.TaskID, store.TaskStatusCompleted, string(resultJSON))

	case "failed":
		// Check if we should retry
		if task.RetryCount < task.MaxRetries {
			// Increment retry and re-enqueue
			h.store.IncrementRetry(ctx, cb.TaskID)
			h.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusPending, cb.ErrorMessage)

			// Re-publish to broker for retry
			taskPayload := map[string]interface{}{
				"task_id":   task.ID,
				"task_name": task.Name,
				"payload":   task.Payload,
			}
			payloadData, _ := json.Marshal(taskPayload)

			asynqTask := &taskqueue.Task{
				Type:    task.Type,
				Payload: payloadData,
				Queue:   store.TaskQueue,
			}

			// 重试时保留 trace metadata
			metadata := trace.MetadataFromContext(ctx)
			if metadata != nil {
				asynqTask.Metadata = metadata
			}

			info, err := h.broker.Publish(ctx, asynqTask)
			if err != nil {
				h.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusFailed, err.Error())
				return fmt.Errorf("failed to re-publish task: %w", err)
			}

			h.store.UpdateBrokerTaskID(ctx, cb.TaskID, info.BrokerTaskID)
			h.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusRunning, "")
			log.Printf("[Callback] Task re-queued for retry: id=%s, retry=%d/%d | trace_id=%s",
				cb.TaskID, task.RetryCount+1, task.MaxRetries, tc.TraceID)
			return nil
		}

		// No more retries
		return h.store.UpdateStatus(ctx, cb.TaskID, store.TaskStatusFailed, cb.ErrorMessage)

	default:
		return fmt.Errorf("unknown callback status: %s", cb.Status)
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// 获取实例 ID（用于链路追踪）
	instanceId := trace.GetInstanceId()
	log.Printf("[Consumer] Instance ID: %s", instanceId)

	log.Println("========================================")
	log.Println("TaskQueue Broker Demo - Consumer")
	log.Println("========================================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ========================================
	// Step 1: Setup Broker
	// ========================================
	redisOpt := asynq.RedisClientOpt{Addr: store.RedisAddr}
	broker := asynqbroker.NewBroker(redisOpt)
	defer broker.Close()
	log.Println("[Consumer] Broker created")

	// ========================================
	// Step 2: Setup Task Store (PostgreSQL)
	// ========================================
	taskStore, err := store.NewTaskStore(store.PgDSN)
	if err != nil {
		log.Fatalf("[Consumer] Failed to connect to database: %v", err)
	}
	defer taskStore.Close()

	if err := taskStore.Migrate(ctx); err != nil {
		log.Fatalf("[Consumer] Failed to migrate: %v", err)
	}
	log.Println("[Consumer] Database migrated")

	// ========================================
	// Step 3: Setup Callback Sender & Handler
	// ========================================
	callbackSender := NewCallbackSender(broker, store.CallbackQueue)
	callbackHandler := NewCallbackHandler(taskStore, broker)

	// ========================================
	// Step 4: Setup Consumers
	// ========================================

	// Worker consumer - handles actual tasks
	workerConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: 5,
		Queues:      map[string]int{store.TaskQueue: 10},
	})

	// Register task handlers
	workerConsumer.Handle("send_email", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		// 从 context 中提取 TraceContext（由 broker 传递的 metadata 恢复）
		tc := trace.MustTraceFromContext(ctx)
		log.Printf("[Worker] Processing send_email | trace_id=%s | span_id=%s | parent_span_id=%s",
			tc.TraceID, tc.SpanId, tc.ParentSpanId)

		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)

		log.Printf("[Worker] Sending email to: %v (task_id=%s)", params["email"], payload.TaskID)
		time.Sleep(500 * time.Millisecond)

		result := map[string]interface{}{
			"message": "Email sent successfully",
			"email":   params["email"],
		}

		if err := callbackSender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Worker] Email sent to: %v | trace_id=%s", params["email"], tc.TraceID)
		return &taskqueue.TaskResult{Data: result}, nil
	})

	workerConsumer.Handle("create_record", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		// 从 context 中提取 TraceContext
		tc := trace.MustTraceFromContext(ctx)
		log.Printf("[Worker] Processing create_record | trace_id=%s | span_id=%s",
			tc.TraceID, tc.SpanId)

		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)

		log.Printf("[Worker] Creating record: %v (task_id=%s)", params["record_type"], payload.TaskID)
		time.Sleep(300 * time.Millisecond)

		result := map[string]interface{}{
			"message":   "Record created",
			"record_id": "REC-12345",
		}

		if err := callbackSender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Worker] Record created: %v | trace_id=%s", params["record_type"], tc.TraceID)
		return &taskqueue.TaskResult{Data: result}, nil
	})

	// Handler for always_fail task (to test retry logic)
	workerConsumer.Handle("always_fail", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		tc := trace.MustTraceFromContext(ctx)
		log.Printf("[Worker] Always fail task executed (task_id=%s) | trace_id=%s", payload.TaskID, tc.TraceID)
		// Always return error to trigger retry
		if err := callbackSender.Fail(ctx, payload.TaskID, "Simulated failure for retry test"); err != nil {
			return nil, err
		}
		return &taskqueue.TaskResult{Data: nil}, nil
	})

	// Callback consumer - handles callbacks
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
		log.Printf("[Callback] Processing callback for task_id=%s | trace_id=%s | status=%s",
			cb.TaskID, tc.TraceID, cb.Status)

		return callbackHandler.Handle(ctx, &cb)
	})

	// Start consumers
	go workerConsumer.Start(ctx)
	go callbackConsumer.Start(ctx)
	log.Println("[Consumer] Consumers started")
	log.Println("[Consumer] Waiting for tasks...")

	// ========================================
	// Step 5: Graceful Shutdown
	// ========================================
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit
	log.Println("[Consumer] Shutdown signal received")

	workerConsumer.Stop()
	callbackConsumer.Stop()
	cancel()

	log.Println("[Consumer] Done.")
}
