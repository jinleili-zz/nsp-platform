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

// TaskHandler processes a broker-level task.
type TaskHandler func(ctx context.Context, task *taskqueue.Task) error

func parseParams(task *taskqueue.Task) (map[string]interface{}, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(task.Payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// HandleEmailSend processes an email task.
func HandleEmailSend(ctx context.Context, task *taskqueue.Task) error {
	tc := trace.MustTraceFromContext(ctx)
	params, err := parseParams(task)
	if err != nil {
		return err
	}

	to, _ := params["to"].(string)
	subject, _ := params["subject"].(string)
	log.Printf("[Handler:Email] Sending email to=%s subject=%s trace_id=%s", to, subject, tc.TraceID)

	time.Sleep(time.Duration(200+rand.Intn(300)) * time.Millisecond)
	if rand.Float32() < 0.1 {
		return fmt.Errorf("email server temporarily unavailable")
	}
	return nil
}

// HandleImageProcess processes an image task.
func HandleImageProcess(ctx context.Context, task *taskqueue.Task) error {
	tc := trace.MustTraceFromContext(ctx)
	params, err := parseParams(task)
	if err != nil {
		return err
	}

	log.Printf("[Handler:Image] Processing image=%v operation=%v trace_id=%s",
		params["image_url"], params["operation"], tc.TraceID)
	time.Sleep(time.Duration(500+rand.Intn(1000)) * time.Millisecond)
	return nil
}

// HandleDataExport processes a data export task.
func HandleDataExport(ctx context.Context, task *taskqueue.Task) error {
	tc := trace.MustTraceFromContext(ctx)
	params, err := parseParams(task)
	if err != nil {
		return err
	}

	log.Printf("[Handler:Export] Export format=%v user=%v trace_id=%s",
		params["format"], params["user_id"], tc.TraceID)
	time.Sleep(time.Duration(1000+rand.Intn(2000)) * time.Millisecond)
	return nil
}

// HandleReportGenerate processes a report task.
func HandleReportGenerate(ctx context.Context, task *taskqueue.Task) error {
	tc := trace.MustTraceFromContext(ctx)
	params, err := parseParams(task)
	if err != nil {
		return err
	}

	log.Printf("[Handler:Report] Generate report=%v month=%v trace_id=%s",
		params["report_type"], params["month"], tc.TraceID)
	time.Sleep(time.Duration(800+rand.Intn(1500)) * time.Millisecond)
	return nil
}

// HandleNotification processes a notification task.
func HandleNotification(ctx context.Context, task *taskqueue.Task) error {
	tc := trace.MustTraceFromContext(ctx)
	params, err := parseParams(task)
	if err != nil {
		return err
	}

	log.Printf("[Handler:Notification] channel=%v recipient=%v trace_id=%s",
		params["channel"], params["recipient"], tc.TraceID)
	time.Sleep(time.Duration(100+rand.Intn(200)) * time.Millisecond)
	return nil
}

// RegisterCustomHandler registers a custom task handler.
func RegisterCustomHandler(handlers map[string]TaskHandler, taskType string, handler TaskHandler) error {
	if _, exists := handlers[taskType]; exists {
		return fmt.Errorf("handler for task type %s already exists", taskType)
	}
	handlers[taskType] = handler
	return nil
}
