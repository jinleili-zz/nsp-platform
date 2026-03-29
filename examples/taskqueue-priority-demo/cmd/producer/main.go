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

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/jinleili-zz/nsp-platform/logger"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
	"github.com/jinleili-zz/nsp-platform/trace"
)

const (
	TaskQueueHigh   = "nsp:taskqueue:high"
	TaskQueueMiddle = "nsp:taskqueue:middle"
	TaskQueueLow    = "nsp:taskqueue:low"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	instanceID := trace.GetInstanceId()
	log.Printf("[Producer] Instance ID: %s", instanceID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rootTC := &trace.TraceContext{
		TraceID:    trace.NewTraceID(),
		SpanId:     trace.NewSpanId(),
		InstanceId: instanceID,
		Sampled:    true,
	}
	ctx = trace.ContextWithTrace(ctx, rootTC)
	ctx = logger.ContextWithTraceID(ctx, rootTC.TraceID)
	ctx = logger.ContextWithSpanID(ctx, rootTC.SpanId)

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		log.Fatal("REDIS_ADDR environment variable is required")
	}
	redisOpt := asynq.RedisClientOpt{Addr: redisAddr}

	broker := asynqbroker.NewBroker(redisOpt)
	defer broker.Close()

	inspector := asynqbroker.NewInspector(redisOpt)
	defer inspector.Close()

	// 发送任务：task_id 放入 payload，不使用 Metadata，不设置 Reply
	publish := func(taskType, queue string, payload map[string]any) {
		taskID := uuid.New().String()
		payload["task_id"] = taskID

		data, err := json.Marshal(payload)
		if err != nil {
			log.Fatalf("[Producer] marshal %s failed: %v", taskType, err)
		}

		task := &taskqueue.Task{
			Type:    taskType,
			Payload: data,
			Queue:   queue,
		}

		info, err := broker.Publish(ctx, task)
		if err != nil {
			log.Fatalf("[Producer] publish %s failed: %v", taskType, err)
		}
		log.Printf("[Producer] Task submitted: id=%s type=%s queue=%s broker_id=%s | trace_id=%s",
			taskID, taskType, queue, info.BrokerTaskID, rootTC.TraceID)
	}

	// 高优先级任务
	publish("send:payment:notification", TaskQueueHigh, map[string]any{
		"payment_id": "PAY-001", "amount": 999.00, "user_id": "U-001",
	})
	publish("deduct:inventory", TaskQueueHigh, map[string]any{
		"sku_id": "SKU-001", "count": 2,
	})

	// 中优先级任务
	publish("send:email", TaskQueueMiddle, map[string]any{
		"email": "user@example.com", "subject": "Welcome!",
	})
	publish("send:notification", TaskQueueMiddle, map[string]any{
		"channel": "sms", "recipient": "+1234567890",
	})
	publish("process:image", TaskQueueMiddle, map[string]any{
		"image_url": "https://example.com/img.jpg", "operation": "resize",
	})
	publish("always:fail", TaskQueueMiddle, map[string]any{
		"reason": "demo-failure",
	})

	// 低优先级任务
	publish("generate:report", TaskQueueLow, map[string]any{
		"report_type": "daily_sales", "date": "2024-01-01",
	})
	publish("export:data", TaskQueueLow, map[string]any{
		"format": "csv", "user_id": "U-001",
	})

	// 使用 Inspector 查询队列统计
	time.Sleep(500 * time.Millisecond)
	fmt.Println("\n========== Queue Statistics ==========")
	for _, queue := range []string{TaskQueueHigh, TaskQueueMiddle, TaskQueueLow} {
		stats, err := inspector.GetQueueStats(ctx, queue)
		if err != nil {
			fmt.Printf("Queue: %-25s | Error: %v\n", queue, err)
			continue
		}
		fmt.Printf("Queue: %-25s | Pending: %3d | Active: %3d | Completed: %3d | Failed: %3d\n",
			queue, stats.Pending, stats.Active, stats.Completed, stats.Failed)
	}
	fmt.Println("======================================")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	cancel()
}
