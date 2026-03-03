// Test RocketMQ integration
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

	_ "github.com/lib/pq"

	"github.com/yourorg/nsp-common/pkg/taskqueue"
	"github.com/yourorg/nsp-common/pkg/taskqueue/rocketmqbroker"
)

var (
	// RocketMQ and PostgreSQL config - read from environment or use defaults for local development
	nameServer = getEnv("ROCKETMQ_NAMESERVER", "127.0.0.1:9876")
	pgDSN      = getEnv("PG_DSN", "postgres://saga:saga123@127.0.0.1:5432/taskqueue_rmq_test?sslmode=disable")

	callbackQueue = "rmq_callbacks"
	taskQueue     = "rmq_tasks"
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("========================================")
	log.Println("RocketMQ TaskQueue Integration Test")
	log.Println("========================================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ========================================
	// Step 1: Setup Broker
	// ========================================
	broker, err := rocketmqbroker.NewBroker(&rocketmqbroker.BrokerConfig{
		NameServer: nameServer,
		GroupName:  "test_producer_group",
		RetryTimes: 2,
	})
	if err != nil {
		log.Fatalf("[Setup] Failed to create broker: %v", err)
	}
	defer broker.Close()
	log.Println("[Setup] Broker created")

	// ========================================
	// Step 2: Setup Engine
	// ========================================
	engine, err := taskqueue.NewEngine(&taskqueue.Config{
		DSN:           pgDSN,
		CallbackQueue: callbackQueue,
		QueueRouter: func(queueTag string, priority taskqueue.Priority) string {
			return taskQueue // Simple: all tasks go to same topic
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
	// Step 3: Setup Worker Consumer
	// ========================================
	callbackSender := taskqueue.NewCallbackSenderFromBroker(broker, callbackQueue)

	// Worker consumer - handles tasks
	workerConsumer, err := rocketmqbroker.NewConsumer(&rocketmqbroker.ConsumerConfig{
		NameServer:        nameServer,
		GroupName:         "test_worker_group",
		Queues:            map[string]string{taskQueue: "*"}, // Subscribe to all tags
		Concurrency:       5,
		MaxReconsumeTimes: 3,
	})
	if err != nil {
		log.Fatalf("[Setup] Failed to create worker consumer: %v", err)
	}

	// Register task handlers
	workerConsumer.Handle("send_email", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
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
		log.Printf("[Worker] Email sent to: %v", params["email"])
		return &taskqueue.TaskResult{Data: result}, nil
	})

	workerConsumer.Handle("create_record", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)

		log.Printf("[Worker] Creating record: %v (task_id=%s)", params["record_type"], payload.TaskID)
		time.Sleep(300 * time.Millisecond)

		result := map[string]interface{}{
			"message":   "Record created",
			"record_id": "REC-RMQ-12345",
		}

		if err := callbackSender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Worker] Record created: %v", params["record_type"])
		return &taskqueue.TaskResult{Data: result}, nil
	})

	// ========================================
	// Step 4: Setup Callback Consumer
	// ========================================
	callbackConsumer, err := rocketmqbroker.NewConsumer(&rocketmqbroker.ConsumerConfig{
		NameServer:        nameServer,
		GroupName:         "test_callback_group",
		Queues:            map[string]string{callbackQueue: "*"},
		Concurrency:       2,
		MaxReconsumeTimes: 3,
	})
	if err != nil {
		log.Fatalf("[Setup] Failed to create callback consumer: %v", err)
	}

	callbackConsumer.Handle("task_callback", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		var cb taskqueue.CallbackPayload
		if err := json.Unmarshal(payload.Params, &cb); err != nil {
			return nil, fmt.Errorf("failed to unmarshal callback: %w", err)
		}
		if err := engine.HandleCallback(ctx, &cb); err != nil {
			return nil, err
		}
		return &taskqueue.TaskResult{}, nil
	})

	// Start consumers
	go func() {
		log.Println("[Setup] Starting worker consumer...")
		if err := workerConsumer.Start(ctx); err != nil {
			log.Printf("[Setup] Worker consumer error: %v", err)
		}
	}()

	go func() {
		log.Println("[Setup] Starting callback consumer...")
		if err := callbackConsumer.Start(ctx); err != nil {
			log.Printf("[Setup] Callback consumer error: %v", err)
		}
	}()

	time.Sleep(5 * time.Second) // Wait for consumers to be ready

	// ========================================
	// Step 5: Submit Workflow
	// ========================================
	log.Println("========================================")
	log.Println("[Demo] Submitting workflow")
	log.Println("========================================")

	emailParams, _ := json.Marshal(map[string]interface{}{
		"email":   "test@example.com",
		"subject": "RocketMQ Test",
	})
	recordParams, _ := json.Marshal(map[string]interface{}{
		"record_type": "rmq_integration_test",
		"user_id":     "U-RMQ-001",
	})

	workflowID, err := engine.SubmitWorkflow(ctx, &taskqueue.WorkflowDefinition{
		Name:         "rmq-test-workflow",
		ResourceType: "test",
		ResourceID:   "test-rmq-001",
		Steps: []taskqueue.StepDefinition{
			{
				TaskType:   "create_record",
				TaskName:   "Create Test Record",
				Params:     string(recordParams),
				Priority:   taskqueue.PriorityNormal,
				MaxRetries: 3,
			},
			{
				TaskType:   "send_email",
				TaskName:   "Send Notification Email",
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
	// Step 6: Poll for Completion
	// ========================================
	log.Println("[Demo] Polling workflow status...")

	for i := 0; i < 30; i++ {
		time.Sleep(2 * time.Second)

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
	// Step 7: Graceful Shutdown
	// ========================================
	log.Println("[Demo] Press Ctrl+C to exit...")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		log.Println("[Demo] Shutdown signal received")
	case <-time.After(10 * time.Second):
		log.Println("[Demo] Auto-exit after 10 seconds")
	}

	workerConsumer.Stop()
	callbackConsumer.Stop()
	cancel()

	log.Println("[Demo] Done.")
}
