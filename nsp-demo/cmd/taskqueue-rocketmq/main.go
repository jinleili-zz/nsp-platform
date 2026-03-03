// Simple RocketMQ TaskQueue Demo
// This example demonstrates using RocketMQ as the message broker instead of Asynq/Redis
//
// Prerequisites:
//   - PostgreSQL running on localhost:5432
//   - RocketMQ NameServer running on localhost:9876
//   - RocketMQ Broker running (connected to NameServer)
//
// To start RocketMQ with Docker:
//   docker run -d --name rmqnamesrv -p 9876:9876 apache/rocketmq:5.1.4 sh mqnamesrv
//   docker run -d --name rmqbroker -p 10909:10909 -p 10911:10911 \
//     --link rmqnamesrv:namesrv \
//     -e "NAMESRV_ADDR=namesrv:9876" \
//     apache/rocketmq:5.1.4 sh mqbroker -n namesrv:9876 -c /home/rocketmq/rocketmq-5.1.4/conf/broker.conf

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
	nameServer    = getEnv("ROCKETMQ_NAMESERVER", "127.0.0.1:9876")
	pgDSN         = getEnv("PG_DSN", "postgres://saga:saga123@127.0.0.1:5432/taskqueue_rmq?sslmode=disable")
	callbackTopic = "taskqueue_callbacks"
	taskTopic     = "taskqueue_tasks"
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
	log.Println("TaskQueue + RocketMQ Demo")
	log.Println("========================================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ========================================
	// Step 1: Create Broker (RocketMQ)
	// ========================================
	broker, err := rocketmqbroker.NewBroker(&rocketmqbroker.BrokerConfig{
		NameServer: nameServer,
		GroupName:  "demo_producer_group",
		RetryTimes: 2,
	})
	if err != nil {
		log.Fatalf("[Setup] Failed to create broker: %v", err)
	}
	defer broker.Close()
	log.Println("[Setup] RocketMQ broker created")

	// ========================================
	// Step 2: Create Engine (Orchestrator)
	// ========================================
	engine, err := taskqueue.NewEngine(&taskqueue.Config{
		DSN:           pgDSN,
		CallbackQueue: callbackTopic,
		QueueRouter: func(queueTag string, priority taskqueue.Priority) string {
			return taskTopic // All tasks go to same topic
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
	// Step 3: Setup Worker
	// ========================================
	callbackSender := taskqueue.NewCallbackSenderFromBroker(broker, callbackTopic)

	// Worker consumer
	workerConsumer, err := rocketmqbroker.NewConsumer(&rocketmqbroker.ConsumerConfig{
		NameServer:        nameServer,
		GroupName:         "demo_worker_group",
		Queues:            map[string]string{taskTopic: "*"},
		Concurrency:       5,
		MaxReconsumeTimes: 3,
	})
	if err != nil {
		log.Fatalf("[Setup] Failed to create worker consumer: %v", err)
	}

	// Register handlers
	workerConsumer.Handle("send_email", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)

		log.Printf("[Worker] Sending email to: %v (task_id=%s)", params["email"], payload.TaskID)
		time.Sleep(500 * time.Millisecond)

		result := map[string]interface{}{
			"message": "Email sent via RocketMQ",
			"email":   params["email"],
		}

		if err := callbackSender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Worker] Email sent: %v", params["email"])
		return &taskqueue.TaskResult{Data: result}, nil
	})

	workerConsumer.Handle("create_record", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)

		log.Printf("[Worker] Creating record: %v (task_id=%s)", params["record_type"], payload.TaskID)
		time.Sleep(300 * time.Millisecond)

		result := map[string]interface{}{
			"message":   "Record created",
			"record_id": "RMQ-12345",
		}

		if err := callbackSender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Worker] Record created: %v", params["record_type"])
		return &taskqueue.TaskResult{Data: result}, nil
	})

	// Callback consumer
	callbackConsumer, err := rocketmqbroker.NewConsumer(&rocketmqbroker.ConsumerConfig{
		NameServer:        nameServer,
		GroupName:         "demo_callback_group",
		Queues:            map[string]string{callbackTopic: "*"},
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
		return nil, engine.HandleCallback(ctx, &cb)
	})

	// Start consumers in background
	go func() {
		log.Println("[Setup] Starting worker consumer...")
		if err := workerConsumer.Start(ctx); err != nil {
			log.Printf("[Error] Worker consumer: %v", err)
		}
	}()

	go func() {
		log.Println("[Setup] Starting callback consumer...")
		if err := callbackConsumer.Start(ctx); err != nil {
			log.Printf("[Error] Callback consumer: %v", err)
		}
	}()

	// Wait for consumers to initialize
	time.Sleep(5 * time.Second)

	// ========================================
	// Step 4: Submit Workflow
	// ========================================
	log.Println("========================================")
	log.Println("[Demo] Submitting workflow")
	log.Println("========================================")

	emailParams, _ := json.Marshal(map[string]interface{}{
		"email":   "user@example.com",
		"subject": "RocketMQ Test",
	})
	recordParams, _ := json.Marshal(map[string]interface{}{
		"record_type": "rocketmq_demo",
		"user_id":     "U-RMQ-001",
	})

	workflowID, err := engine.SubmitWorkflow(ctx, &taskqueue.WorkflowDefinition{
		Name:         "rocketmq-demo-workflow",
		ResourceType: "demo",
		ResourceID:   "demo-rmq-001",
		Steps: []taskqueue.StepDefinition{
			{
				TaskType:   "create_record",
				TaskName:   "Create Record (RocketMQ)",
				Params:     string(recordParams),
				Priority:   taskqueue.PriorityNormal,
				MaxRetries: 3,
			},
			{
				TaskType:   "send_email",
				TaskName:   "Send Email (RocketMQ)",
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
