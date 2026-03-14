// trace_test.go - TraceID 透传测试
package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/paic/nsp-common/pkg/logger"
	"github.com/paic/nsp-common/pkg/trace"
	"github.com/paic/nsp-common/pkg/taskqueue"
)

// TestTracePropagationInWorkflowDemo 测试 workflow demo 中的 traceid透传
func TestTracePropagationInWorkflowDemo(t *testing.T) {
	// 模拟实例 ID
	instanceId := "test-workflow-instance"

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

	// 验证 2: MetadataFromContext 能正确提取 metadata
	metadata := trace.MetadataFromContext(ctx)
	if metadata == nil {
		t.Fatal("MetadataFromContext returned nil")
	}
	if metadata["trace_id"] != rootTC.TraceID {
		t.Errorf("Metadata trace_id mismatch: got %s, want %s", metadata["trace_id"], rootTC.TraceID)
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
	// Worker 的 SpanId 应该是新生成的
	if workerTC.SpanId == rootTC.SpanId {
		t.Error("Worker SpanId should be different from root SpanId")
	}

	t.Log("All workflow trace propagation tests passed!")
}

// TestWorkflowStepWithTrace 测试工作流步骤中的 trace 传递
func TestWorkflowStepWithTrace(t *testing.T) {
	instanceId := "test-step-instance"
	ctx := context.Background()
	rootTC := &trace.TraceContext{
		TraceID:      trace.NewTraceID(),
		SpanId:       trace.NewSpanId(),
		InstanceId:   instanceId,
		Sampled:      true,
	}
	ctx = trace.ContextWithTrace(ctx, rootTC)

	// 模拟工作流步骤定义
	stepParams := map[string]interface{}{
		"email":   "user@example.com",
		"subject": "Welcome!",
	}
	paramsData, _ := json.Marshal(stepParams)

	step := taskqueue.StepDefinition{
		TaskType:   "send_email",
		TaskName:   "Send Welcome Email",
		Params:     string(paramsData),
		MaxRetries: 3,
	}

	// 验证可以从 context 中获取 trace
	tc := trace.MustTraceFromContext(ctx)
	t.Logf("Workflow step '%s' created with trace_id=%s", step.TaskName, tc.TraceID)

	// 验证 metadata 可以传递给步骤执行
	metadata := trace.MetadataFromContext(ctx)
	if metadata == nil {
		t.Fatal("Step metadata is nil")
	}
	if metadata["trace_id"] != rootTC.TraceID {
		t.Errorf("Step metadata trace_id mismatch")
	}
}

// TestMultiStepWorkflowTrace 测试多步骤工作流的 trace 一致性
func TestMultiStepWorkflowTrace(t *testing.T) {
	instanceId := "test-multi-step-instance"
	ctx := context.Background()
	rootTC := &trace.TraceContext{
		TraceID:      trace.NewTraceID(),
		SpanId:       trace.NewSpanId(),
		InstanceId:   instanceId,
		Sampled:      true,
	}
	ctx = trace.ContextWithTrace(ctx, rootTC)

	// 模拟多个步骤
	steps := []string{"create_record", "send_email", "notify_user"}
	traceIDs := make([]string, 0, len(steps))

	for _, stepName := range steps {
		// 每个步骤都应该使用同一个 trace_id
		tc := trace.MustTraceFromContext(ctx)
		traceIDs = append(traceIDs, tc.TraceID)
		t.Logf("Step '%s' executing with trace_id=%s", stepName, tc.TraceID)
	}

	// 验证所有步骤的 trace_id 一致
	for i := 1; i < len(traceIDs); i++ {
		if traceIDs[i] != traceIDs[0] {
			t.Errorf("Step %d trace_id changed: %s -> %s", i, traceIDs[0], traceIDs[i])
		}
	}

	t.Logf("All %d steps maintained same trace_id=%s", len(steps), traceIDs[0])
}

// TestCallbackSenderWithTrace 测试 CallbackSender 中的 trace 传递
func TestCallbackSenderWithTrace(t *testing.T) {
	instanceId := "test-callback-sender-instance"
	ctx := context.Background()
	rootTC := &trace.TraceContext{
		TraceID:      trace.NewTraceID(),
		SpanId:       trace.NewSpanId(),
		InstanceId:   instanceId,
		Sampled:      true,
	}
	ctx = trace.ContextWithTrace(ctx, rootTC)

	// 模拟发送成功回调
	cb := &taskqueue.CallbackPayload{
		TaskID:       "workflow-step-001",
		Status:       "completed",
		Result:       map[string]interface{}{"record_id": "REC-12345"},
		ErrorMessage: "",
	}

	// 验证 trace 上下文存在
	tc := trace.MustTraceFromContext(ctx)
	if tc.TraceID != rootTC.TraceID {
		t.Errorf("Callback trace_id mismatch")
	}

	// 验证 metadata 可以附加到回调任务
	metadata := trace.MetadataFromContext(ctx)
	callbackTask := &taskqueue.Task{
		Type:    "task_callback",
		Payload: func() []byte { data, _ := json.Marshal(cb); return data }(),
		Queue:   "workflow_callbacks",
		Metadata: metadata,
	}

	if callbackTask.Metadata == nil {
		t.Fatal("Callback task metadata is nil")
	}
	if callbackTask.Metadata["trace_id"] != rootTC.TraceID {
		t.Errorf("Callback task metadata trace_id mismatch")
	}

	t.Logf("Callback sent with trace_id=%s", callbackTask.Metadata["trace_id"])
}

// TestTraceLogFields 测试 LogFields 输出
func TestTraceLogFields(t *testing.T) {
	instanceId := "test-log-fields"

	// Root span
	rootTC := &trace.TraceContext{
		TraceID:      trace.NewTraceID(),
		SpanId:       trace.NewSpanId(),
		ParentSpanId: "",
		InstanceId:   instanceId,
		Sampled:      true,
	}

	fields := rootTC.LogFields()
	t.Logf("Root span LogFields: %+v", fields)

	if fields["trace_id"] != rootTC.TraceID {
		t.Error("trace_id missing in LogFields")
	}
	if fields["span_id"] != rootTC.SpanId {
		t.Error("span_id missing in LogFields")
	}
	if fields["instance_id"] != instanceId {
		t.Error("instance_id missing in LogFields")
	}
	if _, exists := fields["parent_span_id"]; exists {
		t.Error("Root span should not have parent_span_id")
	}

	// Child span
	childTC := &trace.TraceContext{
		TraceID:      rootTC.TraceID,
		SpanId:       trace.NewSpanId(),
		ParentSpanId: rootTC.SpanId,
		InstanceId:   instanceId,
		Sampled:      true,
	}

	childFields := childTC.LogFields()
	t.Logf("Child span LogFields: %+v", childFields)

	if childFields["parent_span_id"] != rootTC.SpanId {
		t.Error("Child span should have parent_span_id")
	}
}

// TestTraceContextIntegration 测试 TraceContext 与 context 集成
func TestTraceContextIntegration(t *testing.T) {
	instanceId := "test-integration"
	rootTC := &trace.TraceContext{
		TraceID:      trace.NewTraceID(),
		SpanId:       trace.NewSpanId(),
		InstanceId:   instanceId,
		Sampled:      true,
	}

	ctx := context.Background()
	ctx = trace.ContextWithTrace(ctx, rootTC)

	// 验证 MustTraceFromContext 不会 panic
	tc := trace.MustTraceFromContext(ctx)
	if tc == nil {
		t.Fatal("MustTraceFromContext returned nil")
	}
	if tc.TraceID != rootTC.TraceID {
		t.Errorf("TraceID mismatch")
	}

	// 验证空 context 的处理
	emptyCtx := context.Background()
	emptyTC := trace.MustTraceFromContext(emptyCtx)
	if emptyTC == nil {
		t.Error("MustTraceFromContext should return empty struct, not nil")
	}
	// 空 structure 的字段应该都是空字符串
	if emptyTC.TraceID != "" || emptyTC.SpanId != "" || emptyTC.ParentSpanId != "" {
		t.Error("Empty TraceContext should have all empty fields")
	}

	t.Log("TraceContext integration test passed")
}
