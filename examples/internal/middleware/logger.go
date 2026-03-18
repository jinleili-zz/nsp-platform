// Package middleware provides HTTP middleware components.
package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jinleili-zz/nsp-platform/logger"
)

// responseWriter wraps http.ResponseWriter to capture status code.
// This is used for the standard net/http middleware.
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
// This is the net/http version for standard HTTP handlers.
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

// GinLogger is a Gin middleware that logs HTTP requests with trace context.
// This is the Gin version for Gin-based applications.
func GinLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		// Log incoming request
		logger.InfoContext(c.Request.Context(), "request started",
			logger.FieldMethod, c.Request.Method,
			logger.FieldPath, path,
			logger.FieldPeerAddr, c.ClientIP(),
		)

		// Process request
		c.Next()

		// Calculate latency
		latency := time.Since(start)

		// Build log fields
		fields := []interface{}{
			logger.FieldMethod, c.Request.Method,
			logger.FieldPath, path,
			logger.FieldCode, c.Writer.Status(),
			logger.FieldLatencyMS, latency.Milliseconds(),
			"response_size", c.Writer.Size(),
		}

		if query != "" {
			fields = append(fields, "query", query)
		}

		// Log based on status code
		status := c.Writer.Status()
		if status >= 500 {
			logger.ErrorContext(c.Request.Context(), "request completed", fields...)
		} else if status >= 400 {
			logger.WarnContext(c.Request.Context(), "request completed", fields...)
		} else {
			logger.InfoContext(c.Request.Context(), "request completed", fields...)
		}
	}
}
