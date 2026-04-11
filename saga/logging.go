package saga

import (
	"context"

	"github.com/jinleili-zz/nsp-platform/logger"
	"github.com/jinleili-zz/nsp-platform/trace"
)

const (
	sagaComponent      = "saga"
	sagaFieldInstance  = "instance_id"
	sagaFieldTxID      = "tx_id"
	sagaFieldTxStatus  = "tx_status"
	sagaFieldStepID    = "step_id"
	sagaFieldStepName  = "step_name"
	sagaFieldStepState = "step_status"
)

func resolveSagaLogger(log logger.Logger) logger.Logger {
	if log == nil {
		log = logger.Platform()
	}
	return log.With(logger.FieldComponent, sagaComponent)
}

func appendTransactionLogFields(args []any, tx *Transaction) []any {
	if tx == nil {
		return args
	}

	args = append(args, sagaFieldTxID, tx.ID)
	if tx.Status != "" {
		args = append(args, sagaFieldTxStatus, string(tx.Status))
	}
	return args
}

func appendStepLogFields(args []any, step *Step) []any {
	if step == nil {
		return args
	}

	args = append(args, sagaFieldStepID, step.ID)
	if step.Name != "" {
		args = append(args, sagaFieldStepName, step.Name)
	}
	if step.Status != "" {
		args = append(args, sagaFieldStepState, string(step.Status))
	}
	return args
}

func withSagaTraceContext(ctx context.Context, tc *trace.TraceContext) context.Context {
	if tc == nil {
		return ctx
	}

	ctx = trace.ContextWithTrace(ctx, tc)
	if tc.TraceID != "" {
		ctx = logger.ContextWithTraceID(ctx, tc.TraceID)
	}
	if tc.SpanId != "" {
		ctx = logger.ContextWithSpanID(ctx, tc.SpanId)
	}
	return ctx
}

func rehydrateSagaTraceContext(ctx context.Context, tx *Transaction) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	if tc, ok := trace.TraceFromContext(ctx); ok && tc != nil {
		return withSagaTraceContext(ctx, tc)
	}
	if traceID := logger.TraceIDFromContext(ctx); traceID != "" {
		return ctx
	}
	return withSagaTraceContext(ctx, extractTraceFromPayload(txPayload(tx)))
}

func txPayload(tx *Transaction) map[string]any {
	if tx == nil {
		return nil
	}
	return tx.Payload
}
