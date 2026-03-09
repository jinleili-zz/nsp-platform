package asynqbroker

import (
	"context"
	"encoding/json"

	"github.com/paic/nsp-common/pkg/logger"
	"github.com/paic/nsp-common/pkg/trace"
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

// UnwrapEnvelope splits an asynq payload into the original business payload
// and trace metadata. If the data is not in envelope format (legacy messages),
// it returns (data, nil) for backward compatibility.
func UnwrapEnvelope(data []byte) (payload []byte, metadata map[string]string) {
	var env taskEnvelope
	if err := json.Unmarshal(data, &env); err != nil || env.Version != 1 {
		return data, nil
	}

	metadata = map[string]string{
		"trace_id": env.TraceID,
		"span_id":  env.SpanID,
		"sampled":  "1",
	}
	if !env.Sampled {
		metadata["sampled"] = "0"
	}
	return env.Payload, metadata
}

// injectTraceFromMetadata restores a TraceContext from metadata and injects it
// into ctx. Both trace and logger context keys are set so that
// logger.InfoContext(ctx, ...) automatically includes trace_id and span_id.
// If metadata is empty or has no trace_id, the original ctx is returned.
func injectTraceFromMetadata(ctx context.Context, metadata map[string]string) context.Context {
	if len(metadata) == 0 {
		return ctx
	}
	traceID := metadata["trace_id"]
	if traceID == "" {
		return ctx
	}

	tc := &trace.TraceContext{
		TraceID:      traceID,
		ParentSpanId: metadata["span_id"],
		SpanId:       trace.NewSpanId(),
		InstanceId:   trace.GetInstanceId(),
		Sampled:      metadata["sampled"] != "0",
	}

	ctx = trace.ContextWithTrace(ctx, tc)
	ctx = logger.ContextWithTraceID(ctx, tc.TraceID)
	ctx = logger.ContextWithSpanID(ctx, tc.SpanId)
	return ctx
}
