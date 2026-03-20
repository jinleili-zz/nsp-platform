// handler.go - 任务处理器实现
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/trace"
)

// TaskHandler 任务处理器函数类型
type TaskHandler func(ctx context.Context, payload *taskqueue.TaskPayload) *taskqueue.TaskResult

// parseParams 解析 JSON 参数
func parseParams(params []byte) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(params, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// HandleEmailSend 处理邮件发送任务
func HandleEmailSend(ctx context.Context, payload *taskqueue.TaskPayload) *taskqueue.TaskResult {
	tc := trace.MustTraceFromContext(ctx)
	fields := tc.LogFields()

	// 解析任务参数
	params, err := parseParams(payload.Params)
	if err != nil {
		return &taskqueue.TaskResult{
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	to, _ := params["to"].(string)
	subject, _ := params["subject"].(string)
	_, _ = params["body"].(string) // body 用于实际发送邮件，此处仅记录

	log.Printf("[Handler:Email] Sending email to=%s, subject=%s, trace_id=%s",
		to, subject, fields["trace_id"])

	// 模拟邮件发送耗时
	sleepTime := time.Duration(200+rand.Intn(300)) * time.Millisecond
	time.Sleep(sleepTime)

	// 模拟偶尔的失败（10% 概率）
	if rand.Float32() < 0.1 {
		return &taskqueue.TaskResult{
			Message: "email server temporarily unavailable",
			Data: map[string]interface{}{
				"to":      to,
				"subject": subject,
				"error":   "SMTP connection timeout",
			},
		}
	}

	return &taskqueue.TaskResult{
		Message: "email sent successfully",
		Data: map[string]interface{}{
			"to":          to,
			"subject":     subject,
			"message_id":  fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			"sent_at":     time.Now().Format(time.RFC3339),
			"duration_ms": sleepTime.Milliseconds(),
		},
	}
}

// HandleImageProcess 处理图片处理任务
func HandleImageProcess(ctx context.Context, payload *taskqueue.TaskPayload) *taskqueue.TaskResult {
	tc := trace.MustTraceFromContext(ctx)
	fields := tc.LogFields()

	// 解析任务参数
	params, err := parseParams(payload.Params)
	if err != nil {
		return &taskqueue.TaskResult{
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	imageURL, _ := params["image_url"].(string)
	operation, _ := params["operation"].(string)
	width, _ := params["width"].(float64)
	height, _ := params["height"].(float64)

	log.Printf("[Handler:Image] Processing image url=%s, operation=%s, size=%dx%d, trace_id=%s",
		imageURL, operation, int(width), int(height), fields["trace_id"])

	// 模拟图片处理耗时（图片处理通常较慢）
	sleepTime := time.Duration(500+rand.Intn(1000)) * time.Millisecond
	time.Sleep(sleepTime)

	// 模拟处理结果
	processedURL := fmt.Sprintf("https://cdn.example.com/processed/%d_%s",
		time.Now().Unix(), extractFilename(imageURL))

	return &taskqueue.TaskResult{
		Message: "image processed successfully",
		Data: map[string]interface{}{
			"original_url":  imageURL,
			"processed_url": processedURL,
			"operation":     operation,
			"width":         int(width),
			"height":        int(height),
			"format":        "jpeg",
			"size_kb":       rand.Intn(500) + 100,
			"processed_at":  time.Now().Format(time.RFC3339),
			"duration_ms":   sleepTime.Milliseconds(),
		},
	}
}

// HandleDataExport 处理数据导出任务
func HandleDataExport(ctx context.Context, payload *taskqueue.TaskPayload) *taskqueue.TaskResult {
	tc := trace.MustTraceFromContext(ctx)
	fields := tc.LogFields()

	// 解析任务参数
	params, err := parseParams(payload.Params)
	if err != nil {
		return &taskqueue.TaskResult{
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	format, _ := params["format"].(string)
	dateFrom, _ := params["date_from"].(string)
	dateTo, _ := params["date_to"].(string)
	userID, _ := params["user_id"].(string)

	log.Printf("[Handler:Export] Exporting data format=%s, user=%s, range=%s~%s, trace_id=%s",
		format, userID, dateFrom, dateTo, fields["trace_id"])

	// 模拟数据导出耗时（导出任务通常较慢）
	sleepTime := time.Duration(1000+rand.Intn(2000)) * time.Millisecond
	time.Sleep(sleepTime)

	// 模拟导出文件信息
	fileName := fmt.Sprintf("export_%s_%s_%d.%s", userID, dateFrom, time.Now().Unix(), format)
	downloadURL := fmt.Sprintf("https://storage.example.com/exports/%s", fileName)

	return &taskqueue.TaskResult{
		Message: "data exported successfully",
		Data: map[string]interface{}{
			"user_id":      userID,
			"format":       format,
			"date_range":   fmt.Sprintf("%s to %s", dateFrom, dateTo),
			"file_name":    fileName,
			"download_url": downloadURL,
			"file_size_mb": fmt.Sprintf("%.2f", rand.Float64()*50+1),
			"record_count": rand.Intn(10000) + 1000,
			"expires_at":   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
			"duration_ms":  sleepTime.Milliseconds(),
		},
	}
}

// HandleReportGenerate 处理报表生成任务
func HandleReportGenerate(ctx context.Context, payload *taskqueue.TaskPayload) *taskqueue.TaskResult {
	tc := trace.MustTraceFromContext(ctx)
	fields := tc.LogFields()

	// 解析任务参数
	params, err := parseParams(payload.Params)
	if err != nil {
		return &taskqueue.TaskResult{
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	reportType, _ := params["report_type"].(string)
	month, _ := params["month"].(string)
	department, _ := params["department"].(string)

	log.Printf("[Handler:Report] Generating report type=%s, month=%s, dept=%s, trace_id=%s",
		reportType, month, department, fields["trace_id"])

	// 模拟报表生成耗时
	sleepTime := time.Duration(800+rand.Intn(1500)) * time.Millisecond
	time.Sleep(sleepTime)

	// 模拟报表数据
	reportID := fmt.Sprintf("RPT-%s-%s-%d", department, month, time.Now().Unix())

	return &taskqueue.TaskResult{
		Message: "report generated successfully",
		Data: map[string]interface{}{
			"report_id":     reportID,
			"report_type":   reportType,
			"month":         month,
			"department":    department,
			"total_records": rand.Intn(5000) + 500,
			"summary": map[string]interface{}{
				"total_amount":    fmt.Sprintf("%.2f", rand.Float64()*100000),
				"total_orders":    rand.Intn(1000) + 100,
				"avg_order_value": fmt.Sprintf("%.2f", rand.Float64()*500),
			},
			"generated_at": time.Now().Format(time.RFC3339),
			"duration_ms":  sleepTime.Milliseconds(),
		},
	}
}

// HandleNotification 处理通知发送任务
func HandleNotification(ctx context.Context, payload *taskqueue.TaskPayload) *taskqueue.TaskResult {
	tc := trace.MustTraceFromContext(ctx)
	fields := tc.LogFields()

	// 解析任务参数
	params, err := parseParams(payload.Params)
	if err != nil {
		return &taskqueue.TaskResult{
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	channel, _ := params["channel"].(string) // sms, push, webhook
	recipient, _ := params["recipient"].(string)
	_, _ = params["content"].(string) // content 用于实际发送通知，此处仅记录

	log.Printf("[Handler:Notification] Sending notification channel=%s, recipient=%s, trace_id=%s",
		channel, recipient, fields["trace_id"])

	// 模拟通知发送耗时
	sleepTime := time.Duration(100+rand.Intn(200)) * time.Millisecond
	time.Sleep(sleepTime)

	return &taskqueue.TaskResult{
		Message: "notification sent successfully",
		Data: map[string]interface{}{
			"channel":     channel,
			"recipient":   recipient,
			"message_id":  fmt.Sprintf("ntf_%d", time.Now().UnixNano()),
			"sent_at":     time.Now().Format(time.RFC3339),
			"duration_ms": sleepTime.Milliseconds(),
		},
	}
}

// extractFilename 从 URL 中提取文件名
func extractFilename(url string) string {
	// 简单实现，实际项目中可能需要更完善的 URL 解析
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' {
			return url[i+1:]
		}
	}
	return url
}

// RegisterCustomHandler 注册自定义任务处理器
// 可用于扩展更多任务类型
func RegisterCustomHandler(handlers map[string]TaskHandler, taskType string, handler TaskHandler) error {
	if _, exists := handlers[taskType]; exists {
		return fmt.Errorf("handler for task type %s already exists", taskType)
	}
	handlers[taskType] = handler
	return nil
}
