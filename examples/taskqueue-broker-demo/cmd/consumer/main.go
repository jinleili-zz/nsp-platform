// TaskQueue Broker Demo - Consumer
// This example demonstrates the consumer side of a custom task queue implementation.
// The consumer handles actual tasks and sends callbacks to the producer.
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

// CallbackSender wraps broker for sending callbacks to producer
type CallbackSender struct {
	broker *asynqbroker.Broker
	queue  string
}

// NewCallbackSender creates a new CallbackSender
func NewCallbackSender(broker *asynqbroker.Broker, queue string) *CallbackSender {
	return &CallbackSender{broker: broker, queue: queue}
}

// Success sends a success callback to producer
func (s *CallbackSender) Success(ctx context.Context, taskID string, result interface{}) error {
	return s.send(ctx, taskID, "completed", result, "")
}

// Fail sends a failure callback to producer
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
	log.Printf("[Consumer] Callback sent: task_id=%s, status=%s | trace_id=%s", taskID, status, tc.TraceID)
	return nil
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
	// Step 2: Setup Callback Sender
	// ========================================
	callbackSender := NewCallbackSender(broker, store.CallbackQueue)

	// ========================================
	// Step 3: Setup Task Consumer
	// ========================================
	workerConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: 5,
		Queues:      map[string]int{store.TaskQueue: 10},
	})

	// Register task handlers
	workerConsumer.Handle("send_email", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		// 从 context 中提取 TraceContext（由 broker 传递的 metadata 恢复）
		tc := trace.MustTraceFromContext(ctx)
		log.Printf("[Consumer] Processing send_email | trace_id=%s | span_id=%s | parent_span_id=%s",
			tc.TraceID, tc.SpanId, tc.ParentSpanId)

		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)

		log.Printf("[Consumer] Sending email to: %v (task_id=%s)", params["email"], payload.TaskID)
		time.Sleep(500 * time.Millisecond)

		result := map[string]interface{}{
			"message": "Email sent successfully",
			"email":   params["email"],
		}

		// Send callback to producer
		if err := callbackSender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Consumer] Email sent to: %v | trace_id=%s", params["email"], tc.TraceID)
		return &taskqueue.TaskResult{Data: result}, nil
	})

	workerConsumer.Handle("create_record", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		// 从 context 中提取 TraceContext
		tc := trace.MustTraceFromContext(ctx)
		log.Printf("[Consumer] Processing create_record | trace_id=%s | span_id=%s",
			tc.TraceID, tc.SpanId)

		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)

		log.Printf("[Consumer] Creating record: %v (task_id=%s)", params["record_type"], payload.TaskID)
		time.Sleep(300 * time.Millisecond)

		result := map[string]interface{}{
			"message":   "Record created",
			"record_id": "REC-12345",
		}

		// Send callback to producer
		if err := callbackSender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Consumer] Record created: %v | trace_id=%s", params["record_type"], tc.TraceID)
		return &taskqueue.TaskResult{Data: result}, nil
	})

	// Handler for always_fail task (to test retry logic)
	workerConsumer.Handle("always_fail", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		tc := trace.MustTraceFromContext(ctx)
		log.Printf("[Consumer] Always fail task executed (task_id=%s) | trace_id=%s", payload.TaskID, tc.TraceID)
		// Send failure callback to producer (will trigger retry)
		if err := callbackSender.Fail(ctx, payload.TaskID, "Simulated failure for retry test"); err != nil {
			return nil, err
		}
		return &taskqueue.TaskResult{Data: nil}, nil
	})

	// Start consumer
	go workerConsumer.Start(ctx)
	log.Println("[Consumer] Task consumer started")
	log.Println("[Consumer] Waiting for tasks...")

	// ========================================
	// Step 4: Graceful Shutdown
	// ========================================
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit
	log.Println("[Consumer] Shutdown signal received")

	workerConsumer.Stop()
	cancel()

	log.Println("[Consumer] Done.")
}
