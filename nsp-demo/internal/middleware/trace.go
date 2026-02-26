// Package middleware provides HTTP middleware components.
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/yourorg/nsp-common/pkg/logger"
)

const (
	// HeaderTraceID is the HTTP header name for trace ID.
	HeaderTraceID = "X-Trace-ID"
	// HeaderSpanID is the HTTP header name for span ID.
	HeaderSpanID = "X-Span-ID"
	// HeaderParentSpanID is the HTTP header name for parent span ID.
	HeaderParentSpanID = "X-Parent-Span-ID"
)

// generateID generates a random hex ID of specified byte length.
func generateID(byteLen int) string {
	b := make([]byte, byteLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateTraceID generates a new trace ID (16 bytes / 32 hex chars).
func GenerateTraceID() string {
	return generateID(16)
}

// GenerateSpanID generates a new span ID (8 bytes / 16 hex chars).
func GenerateSpanID() string {
	return generateID(8)
}

// Trace is a middleware that extracts or generates trace_id and span_id,
// and injects them into the request context for logging.
func Trace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Extract or generate trace_id
		traceID := r.Header.Get(HeaderTraceID)
		if traceID == "" {
			traceID = GenerateTraceID()
		}
		ctx = logger.ContextWithTraceID(ctx, traceID)

		// Generate new span_id for this request
		spanID := GenerateSpanID()
		ctx = logger.ContextWithSpanID(ctx, spanID)

		// Set response headers for tracing
		w.Header().Set(HeaderTraceID, traceID)
		w.Header().Set(HeaderSpanID, spanID)

		// Continue with the updated context
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
