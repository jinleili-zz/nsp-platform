package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

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
	// 1. Initialize Broker for sending replies
	// ---------------------------------------------------------------
	broker := asynqbroker.NewBroker(opt)
	defer broker.Close()

	// ---------------------------------------------------------------
	// 2. Create Consumer to listen on the request queue
	// ---------------------------------------------------------------
	consumer := asynqbroker.NewConsumer(opt, asynqbroker.ConsumerConfig{
		Concurrency: 5,
		Queues: map[string]int{
			replydemo.CalcRequestQueue: 1,
		},
	})

	// ---------------------------------------------------------------
	// 3. Register task handler: parse payload, execute calculation,
	//    and send reply to the queue specified in ReplySpec.
	// ---------------------------------------------------------------
	consumer.Handle(replydemo.TaskTypeCalc, func(ctx context.Context, t *taskqueue.Task) error {
		var calcTask replydemo.CalcTask
		if err := json.Unmarshal(t.Payload, &calcTask); err != nil {
			log.Printf("[Consumer] Failed to parse task payload: %v", err)
			return err
		}

		log.Printf("[Process] task_id=%s op=%s operands=%v",
			calcTask.TaskID[:8], calcTask.Operation, calcTask.Operands)

		// Execute calculation.
		result := calculate(&calcTask)

		// Send reply if ReplySpec is present.
		if t.Reply != nil && t.Reply.Queue != "" {
			replyPayload, err := json.Marshal(result)
			if err != nil {
				log.Printf("[Consumer] Failed to marshal reply: %v", err)
				return err
			}

			replyTask := &taskqueue.Task{
				Type:    replydemo.TaskTypeCalcReply,
				Payload: replyPayload,
				Queue:   t.Reply.Queue,
			}

			if _, err := broker.Publish(ctx, replyTask); err != nil {
				log.Printf("[Consumer] Failed to send reply: %v", err)
				return err
			}

			log.Printf("[Reply] task_id=%s result=%.2f -> %s",
				result.TaskID[:8], result.Result, t.Reply.Queue)
		}

		return nil
	})

	// ---------------------------------------------------------------
	// 4. Start consuming (blocking)
	// ---------------------------------------------------------------
	log.Printf("Consumer started, listening on queue: %s", replydemo.CalcRequestQueue)
	if err := consumer.Start(ctx); err != nil {
		log.Fatalf("Consumer error: %v", err)
	}
}

// calculate performs the arithmetic operation and returns a CalcResult.
func calculate(t *replydemo.CalcTask) *replydemo.CalcResult {
	res := &replydemo.CalcResult{TaskID: t.TaskID}

	if len(t.Operands) < 2 {
		res.Error = "need at least 2 operands"
		return res
	}

	a, b := t.Operands[0], t.Operands[1]
	switch t.Operation {
	case "add":
		res.Result = a + b
	case "subtract":
		res.Result = a - b
	case "multiply":
		res.Result = a * b
	case "divide":
		if b == 0 {
			res.Error = "division by zero"
		} else {
			res.Result = a / b
		}
	default:
		res.Error = fmt.Sprintf("unknown operation: %s", t.Operation)
	}

	return res
}
