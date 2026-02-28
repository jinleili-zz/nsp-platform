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
	redisNode1 = "127.0.0.1:7001"
	redisNode2 = "127.0.0.1:7002"
	redisNode3 = "127.0.0.1:7003"

	pgDSN = "postgres://saga:saga123@127.0.0.1:5432/taskqueue_test?sslmode=disable"

	callbackQueue = "demo_fail_callbacks"
	switchQueue   = "demo_fail_tasks_switch"
	firewallQueue = "demo_fail_tasks_firewall"
)

func redisClusterOpt() asynq.RedisClusterClientOpt {
	return asynq.RedisClusterClientOpt{
		Addrs: []string{redisNode1, redisNode2, redisNode3},
	}
}

var (
	failCountMu sync.Mutex
	failCount   = map[string]int{}
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("========================================")
	log.Println("TaskQueue Demo - Failure & Retry Test")
	log.Println("========================================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	broker := asynqbroker.NewBroker(redisClusterOpt())
	defer broker.Close()

	engine, err := taskqueue.NewEngine(&taskqueue.Config{
		DSN:           pgDSN,
		CallbackQueue: callbackQueue,
		QueueRouter: func(queueTag string, priority taskqueue.Priority) string {
			switch queueTag {
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

	if err := engine.Migrate(ctx); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	callbackSender := taskqueue.NewCallbackSenderFromBroker(broker, callbackQueue)

	workerConsumer := asynqbroker.NewConsumer(redisClusterOpt(), asynqbroker.ConsumerConfig{
		Concurrency: 4,
		Queues: map[string]int{
			switchQueue:   6,
			firewallQueue: 3,
		},
		StrictPriority: true,
	})

	// Step 2 handler fails on first attempt
	workerConsumer.Handle("create_vrf_on_switch", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		log.Printf("[Worker] Creating VRF (task_id=%s)", payload.TaskID)
		time.Sleep(500 * time.Millisecond)
		result := map[string]interface{}{"message": "VRF created"}
		callbackSender.Success(ctx, payload.TaskID, result)
		return &taskqueue.TaskResult{Data: result}, nil
	})

	workerConsumer.Handle("create_vlan_subinterface", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		failCountMu.Lock()
		failCount[payload.TaskID]++
		attempt := failCount[payload.TaskID]
		failCountMu.Unlock()

		log.Printf("[Worker] Creating VLAN (task_id=%s, attempt=%d)", payload.TaskID, attempt)
		time.Sleep(500 * time.Millisecond)

		if attempt == 1 {
			log.Printf("[Worker] VLAN creation FAILED (simulated, task_id=%s)", payload.TaskID)
			callbackSender.Fail(ctx, payload.TaskID, "simulated CIDR conflict error")
			return nil, fmt.Errorf("simulated failure")
		}

		result := map[string]interface{}{"message": "VLAN created on retry"}
		callbackSender.Success(ctx, payload.TaskID, result)
		log.Printf("[Worker] VLAN creation SUCCEEDED on retry (task_id=%s)", payload.TaskID)
		return &taskqueue.TaskResult{Data: result}, nil
	})

	workerConsumer.Handle("create_firewall_zone", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		log.Printf("[Worker] Creating firewall zone (task_id=%s)", payload.TaskID)
		time.Sleep(500 * time.Millisecond)
		result := map[string]interface{}{"message": "zone created"}
		callbackSender.Success(ctx, payload.TaskID, result)
		return &taskqueue.TaskResult{Data: result}, nil
	})

	callbackConsumer := asynqbroker.NewConsumer(redisClusterOpt(), asynqbroker.ConsumerConfig{
		Concurrency: 2,
		Queues:      map[string]int{callbackQueue: 10},
	})

	callbackConsumer.HandleRaw("task_callback", func(ctx context.Context, t *asynq.Task) error {
		var cb taskqueue.CallbackPayload
		if err := json.Unmarshal(t.Payload(), &cb); err != nil {
			return fmt.Errorf("failed to unmarshal callback: %w", err)
		}
		return engine.HandleCallback(ctx, &cb)
	})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); workerConsumer.Start(ctx) }()
	go func() { defer wg.Done(); callbackConsumer.Start(ctx) }()

	time.Sleep(2 * time.Second)

	// Submit workflow
	vrfParams, _ := json.Marshal(map[string]interface{}{"vpc_name": "fail-test-vpc"})
	vlanParams, _ := json.Marshal(map[string]interface{}{"vpc_name": "fail-test-vpc", "vlan_id": 200})
	fwParams, _ := json.Marshal(map[string]interface{}{"vpc_name": "fail-test-vpc", "firewall_zone": "zone-fail-1"})

	workflowID, err := engine.SubmitWorkflow(ctx, &taskqueue.WorkflowDefinition{
		Name:         "create-vpc-fail-test",
		ResourceType: "vpc",
		ResourceID:   "vpc-fail-001",
		Steps: []taskqueue.StepDefinition{
			{TaskType: "create_vrf_on_switch", TaskName: "Create VRF", Params: string(vrfParams), QueueTag: "switch"},
			{TaskType: "create_vlan_subinterface", TaskName: "Create VLAN (will fail)", Params: string(vlanParams), QueueTag: "switch"},
			{TaskType: "create_firewall_zone", TaskName: "Create FW Zone", Params: string(fwParams), QueueTag: "firewall"},
		},
	})
	if err != nil {
		log.Fatalf("[Demo] Submit failed: %v", err)
	}
	log.Printf("[Demo] Workflow submitted: %s", workflowID)

	// Wait for step 2 to fail
	time.Sleep(5 * time.Second)

	resp, _ := engine.QueryWorkflow(ctx, workflowID)
	log.Printf("[Demo] After initial run: status=%s, completed=%d, failed=%d",
		resp.Workflow.Status, resp.Stats.Completed, resp.Stats.Failed)
	for _, step := range resp.Steps {
		log.Printf("[Demo]   Step %d [%s]: %s", step.StepOrder, step.Status, step.TaskName)
	}

	if resp.Workflow.Status != taskqueue.WorkflowStatusFailed {
		log.Fatal("[Demo] Expected workflow to be in failed state!")
	}
	log.Println("[Demo] Workflow correctly in FAILED state. Now retrying failed step...")

	// Find the failed step and retry
	var failedStepID string
	for _, step := range resp.Steps {
		if step.Status == taskqueue.StepStatusFailed {
			failedStepID = step.ID
			break
		}
	}

	if failedStepID == "" {
		log.Fatal("[Demo] No failed step found!")
	}

	if err := engine.RetryStep(ctx, failedStepID); err != nil {
		log.Fatalf("[Demo] RetryStep failed: %v", err)
	}
	log.Printf("[Demo] Step retried: %s", failedStepID)

	// Poll for completion after retry
	for i := 0; i < 20; i++ {
		time.Sleep(2 * time.Second)
		resp, _ = engine.QueryWorkflow(ctx, workflowID)
		log.Printf("[Demo] After retry: status=%s, completed=%d, failed=%d",
			resp.Workflow.Status, resp.Stats.Completed, resp.Stats.Failed)

		if resp.Workflow.Status == taskqueue.WorkflowStatusSucceeded {
			log.Println("========================================")
			log.Println("[Demo] Workflow SUCCEEDED after retry!")
			log.Println("========================================")
			for _, step := range resp.Steps {
				log.Printf("[Demo]   Step %d [%s]: %s", step.StepOrder, step.Status, step.TaskName)
			}
			break
		}
	}

	workerConsumer.Stop()
	callbackConsumer.Stop()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-quit:
	case <-time.After(3 * time.Second):
	}
	cancel()
	log.Println("[Demo] Done.")
}
