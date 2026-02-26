// Package middleware provides HTTP middleware components.
package middleware

import (
	"net/http"
	"runtime/debug"

	"github.com/yourorg/nsp-common/pkg/logger"
)

// Recovery is a middleware that recovers from panics and logs the error.
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
