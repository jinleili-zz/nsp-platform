// main.go - 消费者入口
// 负责监听任务队列，执行任务，并将结果发送到回调队列
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

	"github.com/jinleili-zz/nsp-platform/examples/taskqueue-priority-demo/internal/config"
	"github.com/jinleili-zz/nsp-platform/examples/taskqueue-priority-demo/internal/handler"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
	"github.com/jinleili-zz/nsp-platform/trace"
)

// CallbackSender 回调发送器
type CallbackSender struct {
	broker    taskqueue.Broker
	queueName string
}

// NewCallbackSender 创建回调发送器
func NewCallbackSender(broker taskqueue.Broker, queueName string) *CallbackSender {
	return &CallbackSender{
		broker:    broker,
		queueName: queueName,
	}
}

// Send 发送任务执行结果回调
func (s *CallbackSender) Send(ctx context.Context, taskID string, taskType string, result *taskqueue.TaskResult) error {
	callbackPayload := map[string]interface{}{
		"task_id":   taskID,
		"task_type": taskType,
		"message":   result.Message,
		"data":      result.Data,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	callbackBytes, err := json.Marshal(callbackPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal callback payload: %w", err)
	}

	task := &taskqueue.Task{
		Type:     config.CallbackTaskType,
		Payload:  callbackBytes,
		Queue:    s.queueName,
		Priority: taskqueue.PriorityNormal,
		Metadata: map[string]string{
			"original_task_id":   taskID,
			"original_task_type": taskType,
		},
	}

	_, err = s.broker.Publish(ctx, task)
	return err
}

// TaskProcessor 任务处理器包装器
type TaskProcessor struct {
	callbackSender *CallbackSender
	handlers       map[string]handler.TaskHandler
}

// NewTaskProcessor 创建任务处理器
func NewTaskProcessor(callbackSender *CallbackSender) *TaskProcessor {
	return &TaskProcessor{
		callbackSender: callbackSender,
		handlers: map[string]handler.TaskHandler{
			config.TaskTypeEmailSend:      handler.HandleEmailSend,
			config.TaskTypeImageProcess:   handler.HandleImageProcess,
			config.TaskTypeDataExport:     handler.HandleDataExport,
			config.TaskTypeReportGenerate: handler.HandleReportGenerate,
			config.TaskTypeNotification:   handler.HandleNotification,
		},
	}
}

// Process 处理任务
func (p *TaskProcessor) Process(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
	tc := trace.MustTraceFromContext(ctx)
	fields := tc.LogFields()

	// 获取任务处理器
	taskHandler, exists := p.handlers[payload.TaskType]
	if !exists {
		log.Printf("[Consumer] Unknown task type: %s, trace_id=%s", payload.TaskType, fields["trace_id"])
		return &taskqueue.TaskResult{
			Message: fmt.Sprintf("unknown task type: %s", payload.TaskType),
		}, nil
	}

	// 记录开始处理
	log.Printf("[Consumer] Processing task: type=%s, id=%s, trace_id=%s",
		payload.TaskType, payload.TaskID, fields["trace_id"])

	startTime := time.Now()

	// 执行任务
	result := taskHandler(ctx, payload)

	duration := time.Since(startTime)
	log.Printf("[Consumer] Task completed: type=%s, id=%s, message=%s, duration=%v, trace_id=%s",
		payload.TaskType, payload.TaskID, result.Message, duration, fields["trace_id"])

	// 发送回调
	if err := p.callbackSender.Send(ctx, payload.TaskID, payload.TaskType, result); err != nil {
		log.Printf("[Consumer] Failed to send callback: %v, trace_id=%s", err, fields["trace_id"])
	}

	return result, nil
}

// createConsumer 创建并配置消费者
func createConsumer(cfg *config.Config, processor *TaskProcessor) taskqueue.Consumer {
	redisOpt := asynq.RedisClientOpt{
		Addr: cfg.RedisAddr,
		DB:   cfg.RedisDB,
	}

	consumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: cfg.Concurrency,
		Queues:      cfg.QueueWeights,
	})

	// 注册所有任务处理器
	for taskType := range processor.handlers {
		// 使用闭包捕获 taskType
		tType := taskType
		consumer.Handle(tType, func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
			return processor.Process(ctx, payload)
		})
		log.Printf("[Consumer] Registered handler for task type: %s", tType)
	}

	return consumer
}

// printConsumerInfo 打印消费者信息
func printConsumerInfo(cfg *config.Config) {
	fmt.Println("\n========== Consumer Configuration ==========")
	fmt.Printf("Instance ID:     %s\n", cfg.InstanceID)
	fmt.Printf("Redis Address:   %s\n", cfg.RedisAddr)
	fmt.Printf("Concurrency:     %d\n", cfg.Concurrency)
	fmt.Println("\nQueue Weights (Priority Order):")
	fmt.Printf("  %-40s weight=%d\n", config.QueueTaskHigh, cfg.QueueWeights[config.QueueTaskHigh])
	fmt.Printf("  %-40s weight=%d\n", config.QueueTaskMedium, cfg.QueueWeights[config.QueueTaskMedium])
	fmt.Printf("  %-40s weight=%d\n", config.QueueTaskLow, cfg.QueueWeights[config.QueueTaskLow])
	fmt.Println("\nRegistered Task Handlers:")
	fmt.Printf("  - %s\n", config.TaskTypeEmailSend)
	fmt.Printf("  - %s\n", config.TaskTypeImageProcess)
	fmt.Printf("  - %s\n", config.TaskTypeDataExport)
	fmt.Printf("  - %s\n", config.TaskTypeReportGenerate)
	fmt.Printf("  - %s\n", config.TaskTypeNotification)
	fmt.Println("============================================\n")
}

func main() {
	log.Println("[Consumer] Starting TaskQueue Priority Demo Consumer...")

	// 加载配置
	cfg := config.DefaultConfig()

	// 打印配置信息
	printConsumerInfo(cfg)

	// 创建 broker 用于发送回调
	redisOpt := asynq.RedisClientOpt{
		Addr: cfg.RedisAddr,
		DB:   cfg.RedisDB,
	}
	broker := asynqbroker.NewBroker(redisOpt)
	defer broker.Close()

	// 创建回调发送器
	callbackSender := NewCallbackSender(broker, config.QueueResultCallback)

	// 创建任务处理器
	processor := NewTaskProcessor(callbackSender)

	// 创建并配置消费者
	consumer := createConsumer(cfg, processor)

	// 设置优雅关闭
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 启动消费者
	go func() {
		log.Printf("[Consumer] Starting with concurrency=%d...", cfg.Concurrency)
		if err := consumer.Start(ctx); err != nil {
			log.Printf("[Consumer] Error: %v", err)
		}
	}()

	// 等待中断信号
	log.Println("[Consumer] Running... Press Ctrl+C to exit")
	<-sigChan

	log.Println("[Consumer] Shutting down gracefully...")

	// 创建带超时的关闭上下文
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := consumer.Stop(); err != nil {
		log.Printf("[Consumer] Error during shutdown: %v", err)
	}

	// 等待所有任务完成或超时
	select {
	case <-shutdownCtx.Done():
		log.Println("[Consumer] Shutdown timeout reached")
	case <-time.After(100 * time.Millisecond):
		log.Println("[Consumer] Shutdown complete")
	}
}

// 辅助函数：打印 JSON 数据
func printJSON(data interface{}) string {
	bytes, _ := json.MarshalIndent(data, "", "  ")
	return string(bytes)
}
