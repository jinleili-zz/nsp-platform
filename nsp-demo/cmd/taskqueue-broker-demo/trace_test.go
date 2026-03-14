// trace_test.go - TraceID 透传测试
package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/paic/nsp-common/pkg/logger"
	"github.com/paic/nsp-common/pkg/trace"
	"github.com/paic/nsp-common/pkg/taskqueue"
)

// TestTracePropagationInBrokerDemo 测试 broker demo 中的 traceid透传
func TestTracePropagationInBrokerDemo(t *testing.T) {
	// 模拟实例 ID
	instanceId := "test-broker-instance"

	// 创建根 TraceContext（模拟入口请求）
	rootTC := &trace.TraceContext{
		TraceID:      trace.NewTraceID(),
		SpanId:       trace.NewSpanId(),
		ParentSpanId: "", // root span
		InstanceId:   instanceId,
		Sampled:      true,
	}

	ctx := context.Background()
	ctx = trace.ContextWithTrace(ctx, rootTC)
	ctx = logger.ContextWithTraceID(ctx, rootTC.TraceID)
	ctx = logger.ContextWithSpanID(ctx, rootTC.SpanId)

	t.Logf("Root TraceID: %s", rootTC.TraceID)

	// 验证 1: 从 context 中能正确取出 TraceContext
	tc, ok := trace.TraceFromContext(ctx)
	if !ok {
		t.Fatal("Failed to get TraceContext from context")
	}
	if tc.TraceID != rootTC.TraceID {
		t.Errorf("TraceID mismatch: got %s, want %s", tc.TraceID, rootTC.TraceID)
	}
	if tc.ParentSpanId != "" {
		t.Errorf("Root span should have empty ParentSpanId, got %s", tc.ParentSpanId)
	}

	// 验证 2: MetadataFromContext 能正确提取 metadata
	metadata := trace.MetadataFromContext(ctx)
	if metadata == nil {
		t.Fatal("MetadataFromContext returned nil")
	}
	if metadata["trace_id"] != rootTC.TraceID {
		t.Errorf("Metadata trace_id mismatch: got %s, want %s", metadata["trace_id"], rootTC.TraceID)
	}
	if metadata["span_id"] != rootTC.SpanId {
		t.Errorf("Metadata span_id mismatch: got %s, want %s", metadata["span_id"], rootTC.SpanId)
	}

	// 验证 3: 模拟 worker 从 metadata 恢复 TraceContext
	workerTC := trace.TraceFromMetadata(metadata, instanceId)
	if workerTC == nil {
		t.Fatal("TraceFromMetadata returned nil")
	}
	if workerTC.TraceID != rootTC.TraceID {
		t.Errorf("Worker TraceID should match root TraceID: got %s, want %s", workerTC.TraceID, rootTC.TraceID)
	}
	// Worker 的 ParentSpanId 应该是 root 的 SpanId
	if workerTC.ParentSpanId != rootTC.SpanId {
		t.Errorf("Worker ParentSpanId should be root SpanId: got %s, want %s", workerTC.ParentSpanId, rootTC.SpanId)
	}
	// Worker 的 SpanId 应该是新生成的，不应该与 root 相同
	if workerTC.SpanId == rootTC.SpanId {
		t.Error("Worker SpanId should be different from root SpanId")
	}

	// 验证 4: LogFields 返回正确的字段
	fields := rootTC.LogFields()
	if fields["trace_id"] != rootTC.TraceID {
		t.Errorf("LogFields trace_id mismatch")
	}
	if fields["span_id"] != rootTC.SpanId {
		t.Errorf("LogFields span_id mismatch")
	}
	if fields["instance_id"] != instanceId {
		t.Errorf("LogFields instance_id mismatch")
	}
	// Root span 不应该有 parent_span_id
	if _, exists := fields["parent_span_id"]; exists {
		t.Error("Root span should not have parent_span_id in LogFields")
	}

	// 验证 5: 非 root span 的 LogFields 应该包含 parent_span_id
	workerFields := workerTC.LogFields()
	if workerFields["parent_span_id"] != rootTC.SpanId {
		t.Error("Non-root span should have parent_span_id in LogFields")
	}

	t.Log("All trace propagation tests passed!")
}

// TestTaskPayloadWithTrace 测试任务 payload 中包含 trace metadata
func TestTaskPayloadWithTrace(t *testing.T) {
	ctx := context.Background()
	rootTC := &trace.TraceContext{
		TraceID:      trace.NewTraceID(),
		SpanId:       trace.NewSpanId(),
		InstanceId:   "test-instance",
		Sampled:      true,
	}
	ctx = trace.ContextWithTrace(ctx, rootTC)

	// 模拟创建任务
	taskPayload := map[string]interface{}{
		"task_id":     "test-task-001",
		"resource_id": "",
		"task_params": `{"email": "test@example.com"}`,
	}
	payloadData, _ := json.Marshal(taskPayload)

	asynqTask := &taskqueue.Task{
		Type:    "send_email",
		Payload: payloadData,
		Queue:   "test_queue",
	}

	// 注入 trace metadata
	metadata := trace.MetadataFromContext(ctx)
	if metadata != nil {
		asynqTask.Metadata = metadata
	}

	// 验证 metadata 已正确附加
	if asynqTask.Metadata == nil {
		t.Fatal("Task metadata is nil")
	}
	if asynqTask.Metadata["trace_id"] != rootTC.TraceID {
		t.Errorf("Task metadata trace_id mismatch: got %s, want %s", asynqTask.Metadata["trace_id"], rootTC.TraceID)
	}

	t.Logf("Task created with trace_id=%s", asynqTask.Metadata["trace_id"])
}

// TestCallbackWithTrace 测试回调中的 trace 传递
func TestCallbackWithTrace(t *testing.T) {
	instanceId := "test-callback-instance"
	ctx := context.Background()
	rootTC := &trace.TraceContext{
		TraceID:      trace.NewTraceID(),
		SpanId:       trace.NewSpanId(),
		InstanceId:   instanceId,
		Sampled:      true,
	}
	ctx = trace.ContextWithTrace(ctx, rootTC)

	// 验证可以从 context 中获取 trace
	tc := trace.MustTraceFromContext(ctx)
	if tc.TraceID != rootTC.TraceID {
		t.Errorf("Callback trace_id mismatch: got %s, want %s", tc.TraceID, rootTC.TraceID)
	}

	t.Logf("Callback would be sent with trace_id=%s", tc.TraceID)
}

// TestRetryWithTrace 测试重试场景中 trace 的保持
func TestRetryWithTrace(t *testing.T) {
	instanceId := "test-retry-instance"
	ctx := context.Background()
	rootTC := &trace.TraceContext{
		TraceID:      trace.NewTraceID(),
		SpanId:       trace.NewSpanId(),
		InstanceId:   instanceId,
		Sampled:      true,
	}
	ctx = trace.ContextWithTrace(ctx, rootTC)

	// 第一次尝试
	metadata1 := trace.MetadataFromContext(ctx)
	if metadata1 == nil {
		t.Fatal("First attempt metadata is nil")
	}

	// 模拟失败后重试，trace 应该保持不变
	time.Sleep(10 * time.Millisecond)
	metadata2 := trace.MetadataFromContext(ctx)
	if metadata2 == nil {
		t.Fatal("Retry attempt metadata is nil")
	}

	// 验证 trace_id 在重试时保持一致
	if metadata1["trace_id"] != metadata2["trace_id"] {
		t.Errorf("TraceID changed during retry: %s -> %s", metadata1["trace_id"], metadata2["trace_id"])
	}

	t.Logf("Retry maintained same trace_id=%s", metadata1["trace_id"])
}

// TestIsRoot 测试 IsRoot 方法
func TestIsRoot(t *testing.T) {
	rootTC := &trace.TraceContext{
		TraceID:      trace.NewTraceID(),
		SpanId:       trace.NewSpanId(),
		ParentSpanId: "",
		InstanceId:   "test-instance",
	}

	childTC := &trace.TraceContext{
		TraceID:      rootTC.TraceID,
		SpanId:       trace.NewSpanId(),
		ParentSpanId: rootTC.SpanId,
		InstanceId:   "test-instance",
	}

	if !rootTC.IsRoot() {
		t.Error("Root span should return true for IsRoot()")
	}
	if childTC.IsRoot() {
		t.Error("Child span should return false for IsRoot()")
	}
}
