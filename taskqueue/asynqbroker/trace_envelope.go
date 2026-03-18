package asynqbroker

import (
	"context"
	"encoding/json"

	"github.com/jinleili-zz/nsp-platform/logger"
	"github.com/jinleili-zz/nsp-platform/trace"
)

// taskEnvelope wraps the original task payload with trace metadata for
// transparent propagation across the message queue boundary.
// Version field (always 1) is used to reliably detect envelope format,
// avoiding false positives from business payloads that may contain a "payload" key.
type taskEnvelope struct {
	Version int               `json:"_v"`
	TraceID string            `json:"_tid,omitempty"`
	SpanID  string            `json:"_sid,omitempty"` // publisher's SpanId -> consumer's ParentSpanId
	Sampled bool              `json:"_smpl"`
	Payload json.RawMessage   `json:"payload"`
}

// wrapWithTrace extracts TraceContext from ctx and wraps the payload into an
// envelope. If ctx has no trace or serialization fails, the original payload
// is returned unchanged (graceful degradation).
func wrapWithTrace(ctx context.Context, payload []byte) []byte {
	tc, ok := trace.TraceFromContext(ctx)
	if !ok || tc == nil {
		return payload
	}

	env := taskEnvelope{
		Version: 1,
		TraceID: tc.TraceID,
		SpanID:  tc.SpanId,
		Sampled: tc.Sampled,
		Payload: payload,
	}

	data, err := json.Marshal(env)
	if err != nil {
		// Degradation: return original payload so message delivery is not affected.
		return payload
	}
	return data
}

// unwrapEnvelope splits an asynq payload into the original business payload
// and trace metadata. If the data is not in envelope format (legacy messages),
// it returns (data, nil) for backward compatibility.
func unwrapEnvelope(data []byte) (payload []byte, metadata map[string]string) {
	var env taskEnvelope
	if err := json.Unmarshal(data, &env); err != nil || env.Version != 1 {
		return data, nil
	}

	// Use trace.MetadataFromTraceContext to avoid duplicate map construction
	// and ensure consistent format with MetadataFromContext in propagator.go
	tc := &trace.TraceContext{
		TraceID: env.TraceID,
		SpanId:  env.SpanID,
		Sampled: env.Sampled,
	}
	return env.Payload, trace.MetadataFromTraceContext(tc)
}

// injectTraceFromMetadata restores a TraceContext from metadata and injects it
// into ctx. Both trace and logger context keys are set so that
// logger.InfoContext(ctx, ...) automatically includes trace_id and span_id.
// If metadata is empty or has no valid trace_id, the original ctx is returned.
func injectTraceFromMetadata(ctx context.Context, metadata map[string]string) context.Context {
	// Delegate to TraceFromMetadata for trace context construction (avoids code duplication)
	tc := trace.TraceFromMetadata(metadata, trace.GetInstanceId())
	if tc == nil {
		return ctx
	}

	ctx = trace.ContextWithTrace(ctx, tc)
	ctx = logger.ContextWithTraceID(ctx, tc.TraceID)
	ctx = logger.ContextWithSpanID(ctx, tc.SpanId)
	return ctx
}
