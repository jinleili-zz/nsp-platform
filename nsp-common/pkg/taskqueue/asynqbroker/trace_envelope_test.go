package asynqbroker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/paic/nsp-common/pkg/logger"
	"github.com/paic/nsp-common/pkg/trace"
)

func TestWrapWithTrace_NoTraceInContext(t *testing.T) {
	payload := []byte(`{"task_id":"t1","resource_id":"r1"}`)
	ctx := context.Background()

	result := wrapWithTrace(ctx, payload)

	// Without trace, payload should be returned as-is.
	if string(result) != string(payload) {
		t.Errorf("expected original payload, got %s", result)
	}
}

func TestWrapWithTrace_WithTraceInContext(t *testing.T) {
	payload := []byte(`{"task_id":"t1","resource_id":"r1"}`)
	tc := &trace.TraceContext{
		TraceID:      "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanId:       "00f067aa0ba902b7",
		ParentSpanId: "",
		InstanceId:   "test-instance",
		Sampled:      true,
	}
	ctx := trace.ContextWithTrace(context.Background(), tc)

	result := wrapWithTrace(ctx, payload)

	// Should be an envelope.
	var env taskEnvelope
	if err := json.Unmarshal(result, &env); err != nil {
		t.Fatalf("failed to unmarshal envelope: %v", err)
	}
	if env.Version != 1 {
		t.Errorf("expected Version=1, got %d", env.Version)
	}
	if env.TraceID != tc.TraceID {
		t.Errorf("expected TraceID=%s, got %s", tc.TraceID, env.TraceID)
	}
	if env.SpanID != tc.SpanId {
		t.Errorf("expected SpanID=%s, got %s", tc.SpanId, env.SpanID)
	}
	if !env.Sampled {
		t.Error("expected Sampled=true")
	}
	// Verify nested payload is intact.
	if string(env.Payload) != string(payload) {
		t.Errorf("expected original payload in envelope, got %s", env.Payload)
	}
}

func TestWrapWithTrace_SampledFalse(t *testing.T) {
	payload := []byte(`{"task_id":"t1"}`)
	tc := &trace.TraceContext{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanId:  "00f067aa0ba902b7",
		Sampled: false,
	}
	ctx := trace.ContextWithTrace(context.Background(), tc)

	result := wrapWithTrace(ctx, payload)

	var env taskEnvelope
	if err := json.Unmarshal(result, &env); err != nil {
		t.Fatalf("failed to unmarshal envelope: %v", err)
	}
	if env.Sampled {
		t.Error("expected Sampled=false")
	}
}

func TestUnwrapEnvelope_ValidEnvelope(t *testing.T) {
	original := []byte(`{"task_id":"t1","resource_id":"r1"}`)
	envelope, _ := json.Marshal(taskEnvelope{
		Version: 1,
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:  "00f067aa0ba902b7",
		Sampled: true,
		Payload: original,
	})

	payload, metadata := UnwrapEnvelope(envelope)

	if string(payload) != string(original) {
		t.Errorf("expected original payload, got %s", payload)
	}
	if metadata == nil {
		t.Fatal("expected non-nil metadata")
	}
	if metadata["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("unexpected trace_id: %s", metadata["trace_id"])
	}
	if metadata["span_id"] != "00f067aa0ba902b7" {
		t.Errorf("unexpected span_id: %s", metadata["span_id"])
	}
	if metadata["sampled"] != "1" {
		t.Errorf("expected sampled=1, got %s", metadata["sampled"])
	}
}

func TestUnwrapEnvelope_SampledFalse(t *testing.T) {
	original := []byte(`{"task_id":"t1"}`)
	envelope, _ := json.Marshal(taskEnvelope{
		Version: 1,
		TraceID: "aaa",
		SpanID:  "bbb",
		Sampled: false,
		Payload: original,
	})

	_, metadata := UnwrapEnvelope(envelope)

	if metadata["sampled"] != "0" {
		t.Errorf("expected sampled=0, got %s", metadata["sampled"])
	}
}

func TestUnwrapEnvelope_LegacyPayload(t *testing.T) {
	// Legacy payload without envelope wrapping.
	legacy := []byte(`{"task_id":"t1","resource_id":"r1","task_params":"{}"}`)

	payload, metadata := UnwrapEnvelope(legacy)

	if string(payload) != string(legacy) {
		t.Errorf("expected legacy payload returned as-is, got %s", payload)
	}
	if metadata != nil {
		t.Errorf("expected nil metadata for legacy payload, got %v", metadata)
	}
}

func TestUnwrapEnvelope_InvalidJSON(t *testing.T) {
	garbage := []byte(`not json at all`)

	payload, metadata := UnwrapEnvelope(garbage)

	if string(payload) != string(garbage) {
		t.Errorf("expected garbage returned as-is, got %s", payload)
	}
	if metadata != nil {
		t.Errorf("expected nil metadata for garbage, got %v", metadata)
	}
}

func TestUnwrapEnvelope_WrongVersion(t *testing.T) {
	data, _ := json.Marshal(map[string]interface{}{
		"_v":      99,
		"payload": "some data",
	})

	payload, metadata := UnwrapEnvelope(data)

	if string(payload) != string(data) {
		t.Errorf("expected original data returned for wrong version, got %s", payload)
	}
	if metadata != nil {
		t.Errorf("expected nil metadata for wrong version, got %v", metadata)
	}
}

func TestRoundTrip_WrapAndUnwrap(t *testing.T) {
	original := []byte(`{"task_id":"t1","resource_id":"r1","task_params":"{\"key\":\"value\"}"}`)
	tc := &trace.TraceContext{
		TraceID:      "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanId:       "00f067aa0ba902b7",
		ParentSpanId: "1111111111111111",
		InstanceId:   "test-instance",
		Sampled:      true,
	}
	ctx := trace.ContextWithTrace(context.Background(), tc)

	wrapped := wrapWithTrace(ctx, original)
	payload, metadata := UnwrapEnvelope(wrapped)

	if string(payload) != string(original) {
		t.Errorf("round-trip payload mismatch: got %s", payload)
	}
	if metadata["trace_id"] != tc.TraceID {
		t.Errorf("round-trip trace_id mismatch: got %s", metadata["trace_id"])
	}
	if metadata["span_id"] != tc.SpanId {
		t.Errorf("round-trip span_id mismatch: got %s", metadata["span_id"])
	}
}

func TestInjectTraceFromMetadata_NilMetadata(t *testing.T) {
	ctx := context.Background()
	result := injectTraceFromMetadata(ctx, nil)

	// Should be the same ctx, no trace.
	if tc, ok := trace.TraceFromContext(result); ok && tc != nil {
		t.Error("expected no trace in ctx with nil metadata")
	}
}

func TestInjectTraceFromMetadata_EmptyTraceID(t *testing.T) {
	ctx := context.Background()
	metadata := map[string]string{
		"trace_id": "",
		"span_id":  "abc",
	}
	result := injectTraceFromMetadata(ctx, metadata)

	if tc, ok := trace.TraceFromContext(result); ok && tc != nil {
		t.Error("expected no trace in ctx with empty trace_id")
	}
}

func TestInjectTraceFromMetadata_Valid(t *testing.T) {
	ctx := context.Background()
	metadata := map[string]string{
		"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
		"span_id":  "00f067aa0ba902b7",
		"sampled":  "1",
	}

	result := injectTraceFromMetadata(ctx, metadata)

	// Verify trace.TraceContext is set.
	tc, ok := trace.TraceFromContext(result)
	if !ok || tc == nil {
		t.Fatal("expected TraceContext in ctx")
	}
	if tc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("expected TraceID=%s, got %s", "4bf92f3577b34da6a3ce929d0e0e4736", tc.TraceID)
	}
	if tc.ParentSpanId != "00f067aa0ba902b7" {
		t.Errorf("expected ParentSpanId=%s, got %s", "00f067aa0ba902b7", tc.ParentSpanId)
	}
	// Consumer generates its own SpanId.
	if tc.SpanId == "" {
		t.Error("expected non-empty SpanId")
	}
	if tc.SpanId == "00f067aa0ba902b7" {
		t.Error("consumer SpanId should differ from publisher's SpanId")
	}
	if !tc.Sampled {
		t.Error("expected Sampled=true")
	}

	// Verify logger context keys are also set.
	if tid := logger.TraceIDFromContext(result); tid != tc.TraceID {
		t.Errorf("logger TraceID mismatch: expected %s, got %s", tc.TraceID, tid)
	}
	if sid := logger.SpanIDFromContext(result); sid != tc.SpanId {
		t.Errorf("logger SpanID mismatch: expected %s, got %s", tc.SpanId, sid)
	}
}

func TestInjectTraceFromMetadata_SampledFalse(t *testing.T) {
	ctx := context.Background()
	metadata := map[string]string{
		"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
		"span_id":  "00f067aa0ba902b7",
		"sampled":  "0",
	}

	result := injectTraceFromMetadata(ctx, metadata)

	tc, ok := trace.TraceFromContext(result)
	if !ok || tc == nil {
		t.Fatal("expected TraceContext in ctx")
	}
	if tc.Sampled {
		t.Error("expected Sampled=false when metadata sampled=0")
	}
}

func TestInjectTraceFromMetadata_SampledMissing(t *testing.T) {
	ctx := context.Background()
	metadata := map[string]string{
		"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
		"span_id":  "00f067aa0ba902b7",
		// "sampled" is absent — should default to true.
	}

	result := injectTraceFromMetadata(ctx, metadata)

	tc, ok := trace.TraceFromContext(result)
	if !ok || tc == nil {
		t.Fatal("expected TraceContext in ctx")
	}
	if !tc.Sampled {
		t.Error("expected Sampled=true when sampled key is missing (default)")
	}
}
