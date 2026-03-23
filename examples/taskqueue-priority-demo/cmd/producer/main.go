// main.go - 生产者入口
// 负责任务的生产和发送，同时监听结果回调队列
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hibiken/asynq"

	"github.com/jinleili-zz/nsp-platform/examples/taskqueue-priority-demo/internal/config"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/taskqueue/asynqbroker"
	"github.com/jinleili-zz/nsp-platform/trace"
)

// TaskProducer 任务生产者
type TaskProducer struct {
	broker    taskqueue.Broker
	config    *config.Config
	inspector taskqueue.Inspector
}

// NewTaskProducer 创建任务生产者
func NewTaskProducer(cfg *config.Config) (*TaskProducer, error) {
	redisOpt := cfg.RedisConnOpt()

	broker := asynqbroker.NewBroker(redisOpt)
	inspector := asynqbroker.NewInspector(redisOpt)

	return &TaskProducer{
		broker:    broker,
		config:    cfg,
		inspector: inspector,
	}, nil
}

// Close 关闭生产者
func (p *TaskProducer) Close() error {
	if p.broker != nil {
		p.broker.Close()
	}
	if p.inspector != nil {
		p.inspector.Close()
	}
	return nil
}

// SendTask 发送任务到指定队列
func (p *TaskProducer) SendTask(ctx context.Context, taskType string, params map[string]interface{}, priority string) (*taskqueue.TaskInfo, error) {
	queue := config.GetQueueByPriority(priority)

	// 将 params 序列化为 JSON 字符串
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal params: %w", err)
	}

	// 构建符合 asynqbroker 期望的 payload 格式
	payload := map[string]interface{}{
		"task_id":     fmt.Sprintf("task_%d", time.Now().UnixNano()),
		"resource_id": "",
		"task_params": string(paramsBytes),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	task := &taskqueue.Task{
		Type:     taskType,
		Payload:  payloadBytes,
		Queue:    queue,
		Priority: getPriorityValue(priority),
		Metadata: map[string]string{
			"producer_id": p.config.InstanceID,
			"priority":    priority,
			"send_time":   time.Now().Format(time.RFC3339),
		},
	}

	info, err := p.broker.Publish(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("failed to publish task: %w", err)
	}

	log.Printf("[Producer] Task sent: type=%s, queue=%s, priority=%s, task_id=%s",
		taskType, queue, priority, info.BrokerTaskID)
	return info, nil
}

// SendTaskWithTimeout 发送带超时的任务
func (p *TaskProducer) SendTaskWithTimeout(ctx context.Context, taskType string, params map[string]interface{}, priority string, timeout time.Duration) (*taskqueue.TaskInfo, error) {
	queue := config.GetQueueByPriority(priority)

	// 将 params 序列化为 JSON 字符串
	paramsBytes, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal params: %w", err)
	}

	// 构建符合 asynqbroker 期望的 payload 格式
	payload := map[string]interface{}{
		"task_id":     fmt.Sprintf("task_%d", time.Now().UnixNano()),
		"resource_id": "",
		"task_params": string(paramsBytes),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	task := &taskqueue.Task{
		Type:     taskType,
		Payload:  payloadBytes,
		Queue:    queue,
		Priority: getPriorityValue(priority),
		Metadata: map[string]string{
			"producer_id": p.config.InstanceID,
			"priority":    priority,
			"send_time":   time.Now().Format(time.RFC3339),
			"timeout":     timeout.String(),
		},
	}

	info, err := p.broker.Publish(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("failed to publish task: %w", err)
	}

	log.Printf("[Producer] Task sent with timeout: type=%s, queue=%s, priority=%s, timeout=%v, task_id=%s",
		taskType, queue, priority, timeout, info.BrokerTaskID)
	return info, nil
}

// GetQueueStats 获取队列统计信息
func (p *TaskProducer) GetQueueStats() error {
	queues := []string{
		config.QueueTaskHigh,
		config.QueueTaskMedium,
		config.QueueTaskLow,
		config.QueueResultCallback,
	}

	ctx := context.Background()

	fmt.Println("\n========== Queue Statistics ==========")
	for _, queue := range queues {
		stats, err := p.inspector.GetQueueStats(ctx, queue)
		if err != nil {
			fmt.Printf("Queue: %-40s | Error: %v\n", queue, err)
			continue
		}
		fmt.Printf("Queue: %-40s | Pending: %3d | Active: %3d | Completed: %3d | Failed: %3d\n",
			queue, stats.Pending, stats.Active, stats.Completed, stats.Failed)
	}
	fmt.Println("======================================")
	return nil
}

// getPriorityValue 将优先级字符串转换为 Priority 类型
func getPriorityValue(priority string) taskqueue.Priority {
	switch priority {
	case "high":
		return taskqueue.PriorityHigh
	case "medium":
		return taskqueue.PriorityNormal
	case "low":
		return taskqueue.PriorityLow
	default:
		return taskqueue.PriorityNormal
	}
}

// CallbackHandler 处理结果回调
func CallbackHandler(ctx context.Context, t *asynq.Task) error {
	tc := trace.MustTraceFromContext(ctx)
	fields := tc.LogFields()

	// 解析回调数据
	var callbackData map[string]interface{}
	if err := json.Unmarshal(t.Payload(), &callbackData); err != nil {
		log.Printf("[Callback] Failed to unmarshal callback data: %v, trace_id=%s", err, fields["trace_id"])
		return err
	}

	resultJSON, _ := json.MarshalIndent(callbackData, "", "  ")
	log.Printf("[Callback] Received result: trace_id=%s, result=%s", fields["trace_id"], string(resultJSON))

	// 这里可以添加业务逻辑，比如：
	// 1. 更新数据库中的任务状态
	// 2. 通知上游服务
	// 3. 记录任务执行日志

	return nil
}

// startCallbackConsumer 启动回调消费者
func startCallbackConsumer(cfg *config.Config, wg *sync.WaitGroup) {
	defer wg.Done()

	redisOpt := cfg.RedisConnOpt()

	consumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: cfg.CallbackConcurrency,
		Queues: map[string]int{
			config.QueueResultCallback: 10,
		},
	})

	// 注册回调处理器
	consumer.HandleRaw(config.CallbackTaskType, CallbackHandler)

	log.Printf("[Callback Consumer] Starting with concurrency=%d, queue=%s",
		cfg.CallbackConcurrency, config.QueueResultCallback)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := consumer.Start(ctx); err != nil {
		log.Printf("[Callback Consumer] Error: %v", err)
	}
}

// 模拟发送各种任务
func simulateTaskSending(producer *TaskProducer) {
	ctx := context.Background()

	// 发送高优先级任务
	for i := 1; i <= 3; i++ {
		_, err := producer.SendTask(ctx, config.TaskTypeEmailSend, map[string]interface{}{
			"to":      fmt.Sprintf("user%d@example.com", i),
			"subject": fmt.Sprintf("High Priority Email #%d", i),
			"body":    "This is a high priority email",
		}, "high")
		if err != nil {
			log.Printf("Failed to send high priority task: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 发送中优先级任务
	for i := 1; i <= 5; i++ {
		_, err := producer.SendTask(ctx, config.TaskTypeImageProcess, map[string]interface{}{
			"image_url": fmt.Sprintf("https://example.com/images/img%d.jpg", i),
			"operation": "resize",
			"width":     800,
			"height":    600,
		}, "medium")
		if err != nil {
			log.Printf("Failed to send medium priority task: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 发送低优先级任务
	for i := 1; i <= 3; i++ {
		_, err := producer.SendTask(ctx, config.TaskTypeDataExport, map[string]interface{}{
			"format":    "csv",
			"date_from": "2024-01-01",
			"date_to":   "2024-12-31",
			"user_id":   fmt.Sprintf("user_%d", i),
		}, "low")
		if err != nil {
			log.Printf("Failed to send low priority task: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 发送带超时的任务
	_, err := producer.SendTaskWithTimeout(ctx, config.TaskTypeReportGenerate, map[string]interface{}{
		"report_type": "monthly",
		"month":       "2024-12",
		"department":  "sales",
	}, "high", 60*time.Second)
	if err != nil {
		log.Printf("Failed to send timeout task: %v", err)
	}

	log.Println("[Producer] All tasks sent successfully")
}

func main() {
	log.Println("[Producer] Starting TaskQueue Priority Demo Producer...")

	// 加载配置
	cfg := config.DefaultConfig()
	log.Printf("[Producer] Config: redis=%s, instance_id=%s", strings.Join(cfg.RedisAddrs, ","), cfg.InstanceID)

	// 创建生产者
	producer, err := NewTaskProducer(cfg)
	if err != nil {
		log.Fatalf("Failed to create producer: %v", err)
	}
	defer producer.Close()

	// 启动回调消费者
	var wg sync.WaitGroup
	wg.Add(1)
	go startCallbackConsumer(cfg, &wg)

	// 等待回调消费者启动
	time.Sleep(500 * time.Millisecond)

	// 发送任务
	simulateTaskSending(producer)

	// 显示队列统计
	time.Sleep(500 * time.Millisecond)
	producer.GetQueueStats()

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("[Producer] Running... Press Ctrl+C to exit")

	// 定时显示队列统计
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			producer.GetQueueStats()
		case sig := <-sigChan:
			log.Printf("[Producer] Received signal: %v, shutting down...", sig)
			return
		}
	}
}
