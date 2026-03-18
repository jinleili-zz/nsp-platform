// TaskQueue Workflow Demo
// This example demonstrates using the Engine's full workflow orchestration capabilities.
//
// Prerequisites:
//   - PostgreSQL running on localhost:5432
//   - Redis running on localhost:6379
//   - Database: CREATE DATABASE taskqueue_workflow;

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
	_ "github.com/lib/pq"

	"github.com/jinleili-zz/nsp-platform/logger"
	"github.com/jinleili-zz/nsp-platform/trace"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
)

const (
	redisAddr     = "127.0.0.1:6379"
	pgDSN         = "postgres://admin:admin123@127.0.0.1:5432/taskqueue_workflow?sslmode=disable"
	callbackQueue = "workflow_callbacks"
	taskQueue     = "workflow_tasks"
)

// WorkflowTask represents a task stored in PostgreSQL (for broker-only mode)
type WorkflowTask struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Payload     string    `json:"payload"`
	Status      string    `json:"status"`
	Result      string    `json:"result,omitempty"`
	ErrorMsg    string    `json:"error_msg,omitempty"`
	RetryCount  int       `json:"retry_count"`
	MaxRetries  int       `json:"max_retries"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// 获取实例 ID（用于链路追踪）
	instanceId := trace.GetInstanceId()
	log.Printf("[Setup] Instance ID: %s", instanceId)

	log.Println("========================================")
	log.Println("TaskQueue Workflow Demo (Engine-based)")
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

	log.Printf("[Demo] Root TraceID: %s", rootTC.TraceID)

	// ========================================
	// Step 1: Setup Broker
	// ========================================
	redisOpt := asynq.RedisClientOpt{Addr: redisAddr}
	broker := asynqbroker.NewBroker(redisOpt)
	defer broker.Close()
	log.Println("[Setup] Broker created")

	// ========================================
	// Step 2: Setup Engine (Orchestrator)
	// ========================================
	engine, err := taskqueue.NewEngine(&taskqueue.Config{
		DSN:           pgDSN,
		CallbackQueue: callbackQueue,
		QueueRouter: func(queueTag string, priority taskqueue.Priority) string {
			return taskQueue // All tasks go to same queue
		},
	}, broker)
	if err != nil {
		log.Fatalf("[Setup] Failed to create engine: %v", err)
	}
	defer engine.Stop()

	// Run migrations
	if err := engine.Migrate(ctx); err != nil {
		log.Fatalf("[Setup] Failed to migrate database: %v", err)
	}
	log.Println("[Setup] Database migrated")

	// ========================================
	// Step 3: Setup Workers
	// ========================================
	callbackSender := taskqueue.NewCallbackSenderFromBroker(broker, callbackQueue)

	// Worker consumer - handles tasks
	workerConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: 5,
		Queues:       map[string]int{taskQueue: 10},
	})

	// Register task handlers
	workerConsumer.Handle("send_email", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		// 从 context 中提取 TraceContext
		tc := trace.MustTraceFromContext(ctx)
		log.Printf("[Worker] Processing send_email | trace_id=%s | span_id=%s",
			tc.TraceID, tc.SpanId)

		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)

		log.Printf("[Worker] Sending email to: %v (task_id=%s)", params["email"], payload.TaskID)
		time.Sleep(500 * time.Millisecond) // Simulate work

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

	// Callback consumer - handles callbacks
	callbackConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: 2,
		Queues:      map[string]int{callbackQueue: 10},
	})

	callbackConsumer.HandleRaw("task_callback", func(ctx context.Context, t *asynq.Task) error {
		var cb taskqueue.CallbackPayload
		if err := json.Unmarshal(t.Payload(), &cb); err != nil {
			return fmt.Errorf("failed to unmarshal callback: %w", err)
		}

		// 从 context 中提取 TraceContext
		tc := trace.MustTraceFromContext(ctx)
		log.Printf("[Callback] Processing callback for task_id=%s | trace_id=%s | status=%s",
			cb.TaskID, tc.TraceID, cb.Status)

		return engine.HandleCallback(ctx, &cb)
	})

	// Start consumers
	go workerConsumer.Start(ctx)
	go callbackConsumer.Start(ctx)
	log.Println("[Setup] Consumers started")

	time.Sleep(2 * time.Second) // Wait for consumers to be ready

	// ========================================
	// Step 4: Submit Workflow
	// ========================================
	log.Println("========================================")
	log.Println("[Demo] Submitting workflow")
	log.Println("========================================")

	emailParams, _ := json.Marshal(map[string]interface{}{
		"email":   "user@example.com",
		"subject": "Welcome!",
	})
	recordParams, _ := json.Marshal(map[string]interface{}{
		"record_type": "user_registration",
		"user_id":     "U-001",
	})

	workflowID, err := engine.SubmitWorkflow(ctx, &taskqueue.WorkflowDefinition{
		Name:         "user-onboarding",
		ResourceType: "user",
		ResourceID:   "user-001",
		Steps: []taskqueue.StepDefinition{
			{
				TaskType:   "create_record",
				TaskName:   "Create User Record",
				Params:     string(recordParams),
				Priority:   taskqueue.PriorityNormal,
				MaxRetries: 3,
			},
			{
				TaskType:   "send_email",
				TaskName:   "Send Welcome Email",
				Params:     string(emailParams),
				Priority:   taskqueue.PriorityNormal,
				MaxRetries: 3,
			},
		},
	})
	if err != nil {
		log.Fatalf("[Demo] Failed to submit workflow: %v", err)
	}
	log.Printf("[Demo] Workflow submitted: id=%s", workflowID)

	// ========================================
	// Step 5: Poll for Completion
	// ========================================
	log.Println("[Demo] Polling workflow status...")

	for i := 0; i < 20; i++ {
		time.Sleep(1 * time.Second)

		resp, err := engine.QueryWorkflow(ctx, workflowID)
		if err != nil {
			log.Printf("[Demo] Query error: %v", err)
			continue
		}

		log.Printf("[Demo] Status: %s (completed=%d/%d, failed=%d)",
			resp.Workflow.Status, resp.Stats.Completed, resp.Stats.Total, resp.Stats.Failed)

		for _, step := range resp.Steps {
			log.Printf("[Demo]   Step %d: %s [%s]", step.StepOrder, step.TaskName, step.Status)
		}

		if resp.Workflow.Status == taskqueue.WorkflowStatusSucceeded {
			log.Println("========================================")
			log.Println("[Demo] ✅ Workflow SUCCEEDED!")
			log.Println("========================================")
			break
		}
		if resp.Workflow.Status == taskqueue.WorkflowStatusFailed {
			log.Println("========================================")
			log.Printf("[Demo] ❌ Workflow FAILED: %s", resp.Workflow.ErrorMessage)
			log.Println("========================================")
			break
		}
	}

	// ========================================
	// Step 6: Graceful Shutdown
	// ========================================
	log.Println("[Demo] Press Ctrl+C to exit...")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		log.Println("[Demo] Shutdown signal received")
	case <-time.After(5 * time.Second):
		log.Println("[Demo] Auto-exit after 5 seconds")
	}

	workerConsumer.Stop()
	callbackConsumer.Stop()
	cancel()

	log.Println("[Demo] Done.")
}
