// Package middleware provides HTTP middleware components.
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
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
// This is the net/http version for standard HTTP handlers.
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

// GinTrace is a Gin middleware that extracts or generates trace_id and span_id,
// and injects them into the request context for logging.
// This is the Gin version for Gin-based applications.
func GinTrace() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		// Extract or generate trace_id
		traceID := c.GetHeader(HeaderTraceID)
		if traceID == "" {
			traceID = GenerateTraceID()
		}
		ctx = logger.ContextWithTraceID(ctx, traceID)

		// Generate new span_id for this request
		spanID := GenerateSpanID()
		ctx = logger.ContextWithSpanID(ctx, spanID)

		// Set response headers for tracing
		c.Header(HeaderTraceID, traceID)
		c.Header(HeaderSpanID, spanID)

		// Update request context
		c.Request = c.Request.WithContext(ctx)

		// Continue with the next handler
		c.Next()
	}
}
