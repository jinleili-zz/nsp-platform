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

	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
	"github.com/jinleili-zz/nsp-platform/trace"
)

const (
	TaskQueueHigh   = "nsp:taskqueue:high"
	TaskQueueMiddle = "nsp:taskqueue:middle"
	TaskQueueLow    = "nsp:taskqueue:low"
)

func decodePayload(task *taskqueue.Task) (map[string]any, error) {
	var params map[string]any
	if err := json.Unmarshal(task.Payload, &params); err != nil {
		return nil, err
	}
	return params, nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		log.Fatal("REDIS_ADDR environment variable is required")
	}
	redisOpt := asynq.RedisClientOpt{Addr: redisAddr}

	consumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: 5,
		Queues: map[string]int{
			TaskQueueHigh:   30,
			TaskQueueMiddle: 20,
			TaskQueueLow:    10,
		},
	})

	// 高优先级 handler
	consumer.Handle("send:payment:notification", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodePayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [HIGH] payment notification: task_id=%v payment_id=%v trace_id=%s",
			params["task_id"], params["payment_id"], tc.TraceID)
		time.Sleep(200 * time.Millisecond)
		return nil
	})

	consumer.Handle("deduct:inventory", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodePayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [HIGH] deduct inventory: task_id=%v sku=%v trace_id=%s",
			params["task_id"], params["sku_id"], tc.TraceID)
		time.Sleep(150 * time.Millisecond)
		return nil
	})

	// 中优先级 handler
	consumer.Handle("send:email", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodePayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [MIDDLE] send email: task_id=%v email=%v trace_id=%s",
			params["task_id"], params["email"], tc.TraceID)
		time.Sleep(500 * time.Millisecond)
		return nil
	})

	consumer.Handle("send:notification", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodePayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [MIDDLE] send notification: task_id=%v channel=%v trace_id=%s",
			params["task_id"], params["channel"], tc.TraceID)
		time.Sleep(100 * time.Millisecond)
		return nil
	})

	consumer.Handle("process:image", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodePayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [MIDDLE] process image: task_id=%v image=%v trace_id=%s",
			params["task_id"], params["image_url"], tc.TraceID)
		time.Sleep(800 * time.Millisecond)
		return nil
	})

	consumer.Handle("always:fail", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, _ := decodePayload(task)
		log.Printf("[Consumer] [MIDDLE] always fail: task_id=%v reason=%v trace_id=%s",
			params["task_id"], params["reason"], tc.TraceID)
		return fmt.Errorf("simulated failure for demo")
	})

	// 低优先级 handler
	consumer.Handle("generate:report", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodePayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [LOW] generate report: task_id=%v report=%v trace_id=%s",
			params["task_id"], params["report_type"], tc.TraceID)
		time.Sleep(600 * time.Millisecond)
		return nil
	})

	consumer.Handle("export:data", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodePayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [LOW] export data: task_id=%v format=%v trace_id=%s",
			params["task_id"], params["format"], tc.TraceID)
		time.Sleep(700 * time.Millisecond)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := consumer.Start(ctx); err != nil {
			log.Printf("[Consumer] worker stopped: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	_ = consumer.Stop()
	cancel()
}
