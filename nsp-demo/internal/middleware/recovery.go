// Package middleware provides HTTP middleware components.
package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/yourorg/nsp-common/pkg/logger"
)

// Recovery is a middleware that recovers from panics and logs the error.
// This is the net/http version for standard HTTP handlers.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// Log the panic with stack trace
				logger.ErrorContext(r.Context(), "panic recovered",
					logger.FieldError, err,
					"stacktrace", string(debug.Stack()),
				)

				// Return 500 Internal Server Error
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// GinRecovery is a Gin middleware that recovers from panics and logs the error.
// This is the Gin version for Gin-based applications.
func GinRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// Log the panic with stack trace
				logger.ErrorContext(c.Request.Context(), "panic recovered",
					logger.FieldError, err,
					"stacktrace", string(debug.Stack()),
				)

				// Return 500 Internal Server Error
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"code":     500,
					"message":  "Internal Server Error",
					"trace_id": logger.TraceIDFromContext(c.Request.Context()),
				})
			}
		}()

		c.Next()
	}
}
