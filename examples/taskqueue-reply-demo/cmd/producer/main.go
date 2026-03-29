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

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	replydemo "github.com/jinleili-zz/nsp-platform/examples/taskqueue-reply-demo"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
)

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	opt := asynq.RedisClientOpt{Addr: redisAddr}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown on signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Received shutdown signal")
		cancel()
	}()

	// ---------------------------------------------------------------
	// 1. Initialize Broker for sending tasks
	// ---------------------------------------------------------------
	broker := asynqbroker.NewBroker(opt)
	defer broker.Close()

	// ---------------------------------------------------------------
	// 2. Set up reply tracking: map task_id -> result
	// ---------------------------------------------------------------
	type pendingItem struct {
		task   *replydemo.CalcTask
		result *replydemo.CalcResult
	}
	var (
		mu      sync.Mutex
		pending = make(map[string]*pendingItem)
		wg      sync.WaitGroup
	)

	// ---------------------------------------------------------------
	// 3. Create reply consumer to listen on the response queue
	// ---------------------------------------------------------------
	replyConsumer := asynqbroker.NewConsumer(opt, asynqbroker.ConsumerConfig{
		Concurrency: 1,
		Queues: map[string]int{
			replydemo.CalcResponseQueue: 1,
		},
	})

	replyConsumer.Handle(replydemo.TaskTypeCalcReply, func(ctx context.Context, t *taskqueue.Task) error {
		var result replydemo.CalcResult
		if err := json.Unmarshal(t.Payload, &result); err != nil {
			return err
		}

		mu.Lock()
		if item, ok := pending[result.TaskID]; ok {
			item.result = &result
			mu.Unlock()
			log.Printf("[Reply] task_id=%s result=%.2f", result.TaskID[:8], result.Result)
			wg.Done()
		} else {
			mu.Unlock()
			log.Printf("[Reply] ignored unknown task_id=%s", result.TaskID[:8])
		}
		return nil
	})

	go func() {
		if err := replyConsumer.Start(ctx); err != nil {
			log.Printf("Reply consumer stopped: %v", err)
		}
	}()

	// Give the reply consumer a moment to connect.
	time.Sleep(500 * time.Millisecond)

	// ---------------------------------------------------------------
	// 4. Send multiple calculation tasks (add, subtract, multiply)
	// ---------------------------------------------------------------
	tasks := []*replydemo.CalcTask{
		{Operation: "add", Operands: []float64{10, 20}},
		{Operation: "subtract", Operands: []float64{100, 35}},
		{Operation: "multiply", Operands: []float64{7, 8}},
	}

	for _, ct := range tasks {
		ct.TaskID = uuid.New().String()

		payload, err := json.Marshal(ct)
		if err != nil {
			log.Fatalf("Failed to marshal task: %v", err)
		}

		task := &taskqueue.Task{
			Type:    replydemo.TaskTypeCalc,
			Payload: payload,
			Queue:   replydemo.CalcRequestQueue,
			Reply:   &taskqueue.ReplySpec{Queue: replydemo.CalcResponseQueue},
		}

		// Register in pending map and increment WaitGroup before Publish
		// to prevent race if the reply arrives before we finish setup.
		wg.Add(1)
		mu.Lock()
		pending[ct.TaskID] = &pendingItem{task: ct}
		mu.Unlock()

		info, err := broker.Publish(ctx, task)
		if err != nil {
			mu.Lock()
			delete(pending, ct.TaskID)
			mu.Unlock()
			wg.Done()
			log.Fatalf("Failed to publish task: %v", err)
		}

		log.Printf("[Send] task_id=%s op=%s operands=%v broker_id=%s",
			ct.TaskID[:8], ct.Operation, ct.Operands, info.BrokerTaskID)
	}

	// ---------------------------------------------------------------
	// 5. Wait for all replies with timeout
	// ---------------------------------------------------------------
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("All replies received!")
	case <-time.After(10 * time.Second):
		log.Println("Timeout waiting for replies")
	}

	// ---------------------------------------------------------------
	// 6. Print results summary
	// ---------------------------------------------------------------
	fmt.Println("\n=== Results Summary ===")
	mu.Lock()
	for _, item := range pending {
		if item.result != nil {
			fmt.Printf("  %v %s = %.2f  (task_id=%s)\n",
				item.task.Operands, item.task.Operation,
				item.result.Result, item.task.TaskID[:8])
		} else {
			fmt.Printf("  %v %s = TIMEOUT  (task_id=%s)\n",
				item.task.Operands, item.task.Operation, item.task.TaskID[:8])
		}
	}
	mu.Unlock()

	// ---------------------------------------------------------------
	// 7. Inspector demo: Queues, GetQueueStats, ListTasks
	// ---------------------------------------------------------------
	inspector := asynqbroker.NewInspector(opt)
	defer inspector.Close()

	fmt.Println("\n=== Inspector: Queues ===")
	queues, err := inspector.Queues(ctx)
	if err != nil {
		log.Printf("Failed to list queues: %v", err)
	} else {
		for _, q := range queues {
			fmt.Printf("  %s\n", q)
		}
	}

	fmt.Println("\n=== Inspector: Queue Stats ===")
	for _, q := range []string{replydemo.CalcRequestQueue, replydemo.CalcResponseQueue} {
		stats, err := inspector.GetQueueStats(ctx, q)
		if err != nil {
			fmt.Printf("  %s: (not found)\n", q)
			continue
		}
		fmt.Printf("  %s: pending=%d active=%d completed=%d failed=%d\n",
			q, stats.Pending, stats.Active, stats.Completed, stats.Failed)
	}

	// TaskReader: list completed tasks in the request queue
	fmt.Println("\n=== Inspector: Completed Tasks ===")
	taskResult, err := inspector.ListTasks(ctx, replydemo.CalcRequestQueue, taskqueue.TaskStateCompleted, nil)
	if err != nil {
		log.Printf("Failed to list tasks: %v", err)
	} else {
		fmt.Printf("  Total completed: %d\n", taskResult.Total)
		for _, t := range taskResult.Tasks {
			fmt.Printf("  id=%s type=%s state=%s\n", t.ID[:8], t.Type, t.State)
		}
	}

	cancel()
	replyConsumer.Stop()
	log.Println("Producer done.")
}
