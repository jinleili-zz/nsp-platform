package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hibiken/asynq"

	replydemo "github.com/jinleili-zz/nsp-platform/examples/taskqueue-reply-demo"
	"github.com/jinleili-zz/nsp-platform/logger"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
)

func main() {
	if err := logger.Init(logger.DevelopmentConfig("taskqueue-reply-consumer")); err != nil {
		panic(err)
	}
	defer logger.Sync()

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	opt := asynq.RedisClientOpt{Addr: redisAddr}
	runtimeLog := logger.Platform().With("example", "taskqueue-reply-consumer")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown on signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		runtimeLog.Info("received shutdown signal")
		cancel()
	}()

	// ---------------------------------------------------------------
	// 1. Initialize Broker for sending replies
	// ---------------------------------------------------------------
	broker := asynqbroker.NewBrokerWithConfig(opt, asynqbroker.BrokerConfig{Logger: runtimeLog})
	defer broker.Close()

	// ---------------------------------------------------------------
	// 2. Create Consumer to listen on the request queue
	// ---------------------------------------------------------------
	consumer := asynqbroker.NewConsumer(opt, asynqbroker.ConsumerConfig{
		Concurrency: 5,
		Queues: map[string]int{
			replydemo.CalcRequestQueue: 1,
		},
		RuntimeLogger: runtimeLog,
	})

	// ---------------------------------------------------------------
	// 3. Register task handler: parse payload, execute calculation,
	//    and send reply to the queue specified in ReplySpec.
	// ---------------------------------------------------------------
	consumer.Handle(replydemo.TaskTypeCalc, func(ctx context.Context, t *taskqueue.Task) error {
		var calcTask replydemo.CalcTask
		if err := json.Unmarshal(t.Payload, &calcTask); err != nil {
			logger.ErrorContext(ctx, "failed to parse task payload", logger.FieldError, err)
			return err
		}

		logger.InfoContext(ctx, "processing calculation task",
			logger.FieldTaskID, calcTask.TaskID,
			"operation", calcTask.Operation,
			"operands", calcTask.Operands,
		)

		// Execute calculation.
		result := calculate(&calcTask)

		// Send reply if ReplySpec is present.
		if t.Reply != nil && t.Reply.Queue != "" {
			replyPayload, err := json.Marshal(result)
			if err != nil {
				logger.ErrorContext(ctx, "failed to marshal reply payload", logger.FieldError, err)
				return err
			}

			replyTask := &taskqueue.Task{
				Type:    replydemo.TaskTypeCalcReply,
				Payload: replyPayload,
				Queue:   t.Reply.Queue,
			}

			if _, err := broker.Publish(ctx, replyTask); err != nil {
				logger.ErrorContext(ctx, "failed to send reply task", logger.FieldError, err)
				return err
			}

			logger.InfoContext(ctx, "sent reply task",
				logger.FieldTaskID, result.TaskID,
				"result", result.Result,
				logger.FieldQueue, t.Reply.Queue,
			)
		}

		return nil
	})

	// ---------------------------------------------------------------
	// 4. Start consuming (blocking)
	// ---------------------------------------------------------------
	runtimeLog.Info("consumer started", logger.FieldQueue, replydemo.CalcRequestQueue)
	if err := consumer.Start(ctx); err != nil {
		runtimeLog.Fatal("consumer stopped with error", logger.FieldError, err)
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
