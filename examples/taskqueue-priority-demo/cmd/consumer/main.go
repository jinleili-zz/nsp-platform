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

	"github.com/jinleili-zz/nsp-platform/examples/taskqueue-priority-demo/store"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
	"github.com/jinleili-zz/nsp-platform/trace"
)

type callbackMessage struct {
	TaskID       string      `json:"task_id"`
	Status       string      `json:"status"`
	Result       interface{} `json:"result,omitempty"`
	ErrorMessage string      `json:"error_message,omitempty"`
}

type CallbackSender struct {
	broker *asynqbroker.Broker
}

func NewCallbackSender(broker *asynqbroker.Broker) *CallbackSender {
	return &CallbackSender{broker: broker}
}

func (s *CallbackSender) Success(ctx context.Context, task *taskqueue.Task, result interface{}) error {
	return s.send(ctx, task, "completed", result, "")
}

func (s *CallbackSender) Fail(ctx context.Context, task *taskqueue.Task, errMsg string) error {
	return s.send(ctx, task, "failed", nil, errMsg)
}

func (s *CallbackSender) send(ctx context.Context, task *taskqueue.Task, status string, result interface{}, errorMsg string) error {
	if task.Reply == nil || task.Reply.Queue == "" {
		return nil
	}

	cb := callbackMessage{
		TaskID:       task.Metadata["task_id"],
		Status:       status,
		Result:       result,
		ErrorMessage: errorMsg,
	}
	data, err := json.Marshal(cb)
	if err != nil {
		return fmt.Errorf("failed to marshal callback: %w", err)
	}

	_, err = s.broker.Publish(ctx, &taskqueue.Task{
		Type:    "broker_task_callback",
		Payload: data,
		Queue:   task.Reply.Queue,
	})
	return err
}

func decodeTaskPayload(task *taskqueue.Task) (map[string]interface{}, error) {
	var params map[string]interface{}
	if err := json.Unmarshal(task.Payload, &params); err != nil {
		return nil, err
	}
	return params, nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	redisOpt := asynq.RedisClientOpt{Addr: store.MustRedisAddr()}
	broker := asynqbroker.NewBroker(redisOpt)
	defer broker.Close()

	callbackSender := NewCallbackSender(broker)

	workerConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: 5,
		Queues: map[string]int{
			store.TaskQueueHigh:   30,
			store.TaskQueueMiddle: 20,
			store.TaskQueueLow:    10,
		},
	})

	// 订单类任务 handler
	workerConsumer.Handle("send:payment:notification", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodeTaskPayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [HIGH] payment notification: task_id=%s payment_id=%v trace_id=%s",
			task.Metadata["task_id"], params["payment_id"], tc.TraceID)
		time.Sleep(200 * time.Millisecond)
		return callbackSender.Success(ctx, task, map[string]any{
			"message":    "Payment notification sent",
			"payment_id": params["payment_id"],
		})
	})

	workerConsumer.Handle("deduct:inventory", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodeTaskPayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [HIGH] deduct inventory: task_id=%s sku=%v trace_id=%s",
			task.Metadata["task_id"], params["sku_id"], tc.TraceID)
		time.Sleep(150 * time.Millisecond)
		return callbackSender.Success(ctx, task, map[string]any{
			"message": "Inventory deducted",
			"sku_id":  params["sku_id"],
			"count":   params["count"],
		})
	})

	// 通知类任务 handler
	workerConsumer.Handle("send:email", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodeTaskPayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [MIDDLE] send email: task_id=%s email=%v trace_id=%s",
			task.Metadata["task_id"], params["email"], tc.TraceID)
		time.Sleep(500 * time.Millisecond)
		return callbackSender.Success(ctx, task, map[string]any{
			"message": "Email sent successfully",
			"email":   params["email"],
		})
	})

	workerConsumer.Handle("send:notification", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodeTaskPayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [MIDDLE] send notification: task_id=%s channel=%v trace_id=%s",
			task.Metadata["task_id"], params["channel"], tc.TraceID)
		time.Sleep(100 * time.Millisecond)
		return callbackSender.Success(ctx, task, map[string]any{
			"message":   "Notification sent",
			"channel":   params["channel"],
			"recipient": params["recipient"],
		})
	})

	workerConsumer.Handle("process:image", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodeTaskPayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [MIDDLE] process image: task_id=%s image=%v trace_id=%s",
			task.Metadata["task_id"], params["image_url"], tc.TraceID)
		time.Sleep(800 * time.Millisecond)
		return callbackSender.Success(ctx, task, map[string]any{
			"message":    "Image processed",
			"image_url":  params["image_url"],
			"operation":  params["operation"],
		})
	})

	workerConsumer.Handle("generate:report", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodeTaskPayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [LOW] generate report: task_id=%s report=%v trace_id=%s",
			task.Metadata["task_id"], params["report_type"], tc.TraceID)
		time.Sleep(600 * time.Millisecond)
		return callbackSender.Success(ctx, task, map[string]any{
			"message":   "Report generated",
			"report_id": "RPT-67890",
		})
	})

	workerConsumer.Handle("export:data", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		params, err := decodeTaskPayload(task)
		if err != nil {
			return err
		}
		log.Printf("[Consumer] [LOW] export data: task_id=%s format=%v trace_id=%s",
			task.Metadata["task_id"], params["format"], tc.TraceID)
		time.Sleep(700 * time.Millisecond)
		return callbackSender.Success(ctx, task, map[string]any{
			"message": "Data exported",
			"format":  params["format"],
		})
	})

	workerConsumer.Handle("always:fail", func(ctx context.Context, task *taskqueue.Task) error {
		tc := trace.MustTraceFromContext(ctx)
		log.Printf("[Consumer] [MIDDLE] always fail: task_id=%s trace_id=%s",
			task.Metadata["task_id"], tc.TraceID)
		return callbackSender.Fail(ctx, task, "Simulated failure for retry test")
	})

	go func() {
		if err := workerConsumer.Start(ctx); err != nil {
			log.Printf("[Consumer] worker stopped: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	_ = workerConsumer.Stop()
	cancel()
}
