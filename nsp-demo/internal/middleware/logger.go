// Package middleware provides HTTP middleware components.
package middleware

import (
	"net/http"
	"time"

	"github.com/yourorg/nsp-common/pkg/logger"
)

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    int64
}

// WriteHeader captures the status code.
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Write captures the response size.
func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.written += int64(n)
	return n, err
}

// Logger is a middleware that logs HTTP requests with trace context.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Log incoming request
		logger.InfoContext(r.Context(), "request started",
			logger.FieldMethod, r.Method,
			logger.FieldPath, r.URL.Path,
			logger.FieldPeerAddr, r.RemoteAddr,
		)

		// Process request
		next.ServeHTTP(wrapped, r)

		// Calculate latency
		latency := time.Since(start)

		// Log completed request
		logger.InfoContext(r.Context(), "request completed",
			logger.FieldMethod, r.Method,
			logger.FieldPath, r.URL.Path,
			logger.FieldCode, wrapped.statusCode,
			logger.FieldLatencyMS, latency.Milliseconds(),
			"response_size", wrapped.written,
		)
	})
}
