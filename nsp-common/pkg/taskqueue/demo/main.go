package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	_ "github.com/lib/pq"

	"github.com/yourorg/nsp-common/pkg/taskqueue"
	"github.com/yourorg/nsp-common/pkg/taskqueue/asynqbroker"
)

const (
	// Redis cluster nodes (host-mapped ports)
	redisNode1 = "127.0.0.1:7001"
	redisNode2 = "127.0.0.1:7002"
	redisNode3 = "127.0.0.1:7003"

	// PostgreSQL DSN
	pgDSN = "postgres://saga:saga123@127.0.0.1:5432/taskqueue_test?sslmode=disable"

	// Queue names
	callbackQueue = "demo_callbacks"
	switchQueue   = "demo_tasks_switch"
	firewallQueue = "demo_tasks_firewall"
)

func redisClusterOpt() asynq.RedisClusterClientOpt {
	return asynq.RedisClusterClientOpt{
		Addrs: []string{redisNode1, redisNode2, redisNode3},
	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("========================================")
	log.Println("TaskQueue Demo - Starting")
	log.Println("========================================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ======================================================
	// 1. Create Broker (shared by orchestrator and worker)
	// ======================================================
	broker := asynqbroker.NewBroker(redisClusterOpt())
	defer broker.Close()

	// ======================================================
	// 2. Create Engine (orchestrator side)
	// ======================================================
	engine, err := taskqueue.NewEngine(&taskqueue.Config{
		DSN:           pgDSN,
		CallbackQueue: callbackQueue,
		QueueRouter: func(queueTag string, priority taskqueue.Priority) string {
			switch queueTag {
			case "switch":
				return switchQueue
			case "firewall":
				return firewallQueue
			default:
				return switchQueue
			}
		},
	}, broker)
	if err != nil {
		log.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Stop()

	// Run migrations
	if err := engine.Migrate(ctx); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}
	log.Println("[Demo] Database migration complete")

	// ======================================================
	// 3. Start Worker (consumer side)
	// ======================================================
	callbackSender := taskqueue.NewCallbackSenderFromBroker(broker, callbackQueue)

	// Worker consumer - handles actual device tasks
	workerConsumer := asynqbroker.NewConsumer(redisClusterOpt(), asynqbroker.ConsumerConfig{
		Concurrency: 4,
		Queues: map[string]int{
			switchQueue:   6,
			firewallQueue: 3,
		},
		StrictPriority: true,
	})

	registerWorkerHandlers(workerConsumer, callbackSender)

	// Callback consumer - handles callbacks on orchestrator side
	callbackConsumer := asynqbroker.NewConsumer(redisClusterOpt(), asynqbroker.ConsumerConfig{
		Concurrency: 2,
		Queues: map[string]int{
			callbackQueue: 10,
		},
	})

	callbackConsumer.HandleRaw("task_callback", func(ctx context.Context, t *asynq.Task) error {
		var cb taskqueue.CallbackPayload
		if err := json.Unmarshal(t.Payload(), &cb); err != nil {
			return fmt.Errorf("failed to unmarshal callback: %w", err)
		}
		return engine.HandleCallback(ctx, &cb)
	})

	// Start both consumers in goroutines
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("[Demo] Worker consumer starting...")
		if err := workerConsumer.Start(ctx); err != nil {
			log.Printf("[Demo] Worker consumer error: %v", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Println("[Demo] Callback consumer starting...")
		if err := callbackConsumer.Start(ctx); err != nil {
			log.Printf("[Demo] Callback consumer error: %v", err)
		}
	}()

	// Wait for consumers to be ready
	time.Sleep(2 * time.Second)

	// ======================================================
	// 4. Submit a workflow (simulating VPC creation)
	// ======================================================
	log.Println("========================================")
	log.Println("[Demo] Submitting VPC creation workflow")
	log.Println("========================================")

	vrfParams, _ := json.Marshal(map[string]interface{}{
		"vpc_name": "demo-vpc-1",
		"vrf_name": "VRF-DEMO-1",
	})
	vlanParams, _ := json.Marshal(map[string]interface{}{
		"vpc_name": "demo-vpc-1",
		"vrf_name": "VRF-DEMO-1",
		"vlan_id":  100,
	})
	fwParams, _ := json.Marshal(map[string]interface{}{
		"vpc_name":      "demo-vpc-1",
		"firewall_zone": "zone-demo-1",
	})

	workflowID, err := engine.SubmitWorkflow(ctx, &taskqueue.WorkflowDefinition{
		Name:         "create-vpc",
		ResourceType: "vpc",
		ResourceID:   "vpc-demo-001",
		Steps: []taskqueue.StepDefinition{
			{
				TaskType:   "create_vrf_on_switch",
				TaskName:   "Create VRF",
				Params:     string(vrfParams),
				QueueTag:   "switch",
				Priority:   taskqueue.PriorityNormal,
				MaxRetries: 3,
			},
			{
				TaskType:   "create_vlan_subinterface",
				TaskName:   "Create VLAN SubInterface",
				Params:     string(vlanParams),
				QueueTag:   "switch",
				Priority:   taskqueue.PriorityNormal,
				MaxRetries: 3,
			},
			{
				TaskType:   "create_firewall_zone",
				TaskName:   "Create Firewall Zone",
				Params:     string(fwParams),
				QueueTag:   "firewall",
				Priority:   taskqueue.PriorityNormal,
				MaxRetries: 3,
			},
		},
	})
	if err != nil {
		log.Fatalf("[Demo] Failed to submit workflow: %v", err)
	}
	log.Printf("[Demo] Workflow submitted: id=%s", workflowID)

	// ======================================================
	// 5. Poll for workflow completion
	// ======================================================
	log.Println("[Demo] Polling workflow status...")
	completed := false
	for i := 0; i < 30; i++ {
		time.Sleep(2 * time.Second)

		resp, err := engine.QueryWorkflow(ctx, workflowID)
		if err != nil {
			log.Printf("[Demo] Query error: %v", err)
			continue
		}

		log.Printf("[Demo] Workflow status: %s (completed=%d/%d, failed=%d)",
			resp.Workflow.Status, resp.Stats.Completed, resp.Stats.Total, resp.Stats.Failed)

		for _, step := range resp.Steps {
			log.Printf("[Demo]   Step %d [%s]: %s (broker_id=%s)",
				step.StepOrder, step.Status, step.TaskName, step.BrokerTaskID)
		}

		if resp.Workflow.Status == taskqueue.WorkflowStatusSucceeded {
			log.Println("========================================")
			log.Println("[Demo] Workflow SUCCEEDED!")
			log.Println("========================================")
			completed = true
			break
		}
		if resp.Workflow.Status == taskqueue.WorkflowStatusFailed {
			log.Println("========================================")
			log.Printf("[Demo] Workflow FAILED: %s", resp.Workflow.ErrorMessage)
			log.Println("========================================")
			completed = true
			break
		}
	}

	if !completed {
		log.Println("[Demo] Timeout waiting for workflow completion")
	}

	// ======================================================
	// 6. Graceful shutdown
	// ======================================================
	log.Println("[Demo] Shutting down...")
	workerConsumer.Stop()
	callbackConsumer.Stop()

	// Wait for signal or auto-exit
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		log.Println("[Demo] Signal received")
	case <-time.After(3 * time.Second):
		log.Println("[Demo] Auto-exit after demo completion")
	}

	cancel()
	log.Println("[Demo] Done.")
}

// registerWorkerHandlers registers simulated device task handlers.
func registerWorkerHandlers(consumer *asynqbroker.Consumer, sender *taskqueue.CallbackSender) {
	// VRF creation handler
	consumer.Handle("create_vrf_on_switch", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)
		log.Printf("[Worker] Creating VRF: %v (task_id=%s)", params["vrf_name"], payload.TaskID)

		time.Sleep(1 * time.Second) // simulate work

		result := map[string]interface{}{
			"message":  fmt.Sprintf("VRF %s created successfully", params["vrf_name"]),
			"vrf_name": params["vrf_name"],
		}
		if err := sender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Worker] VRF created: %v", params["vrf_name"])
		return &taskqueue.TaskResult{Data: result}, nil
	})

	// VLAN subinterface creation handler
	consumer.Handle("create_vlan_subinterface", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)
		log.Printf("[Worker] Creating VLAN subinterface: vlan_id=%v (task_id=%s)", params["vlan_id"], payload.TaskID)

		time.Sleep(1 * time.Second)

		result := map[string]interface{}{
			"message": fmt.Sprintf("VLAN %v subinterface created", params["vlan_id"]),
			"vlan_id": params["vlan_id"],
		}
		if err := sender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Worker] VLAN subinterface created: vlan_id=%v", params["vlan_id"])
		return &taskqueue.TaskResult{Data: result}, nil
	})

	// Firewall zone creation handler
	consumer.Handle("create_firewall_zone", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)
		log.Printf("[Worker] Creating firewall zone: %v (task_id=%s)", params["firewall_zone"], payload.TaskID)

		time.Sleep(1 * time.Second)

		result := map[string]interface{}{
			"message":       fmt.Sprintf("Firewall zone %s created", params["firewall_zone"]),
			"firewall_zone": params["firewall_zone"],
		}
		if err := sender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Worker] Firewall zone created: %v", params["firewall_zone"])
		return &taskqueue.TaskResult{Data: result}, nil
	})
}
