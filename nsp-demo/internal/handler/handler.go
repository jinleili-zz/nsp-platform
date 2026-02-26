// Package handler provides HTTP request handlers.
package handler

import (
	"encoding/json"
	"net/http"

	"github.com/yourorg/nsp-common/pkg/logger"
)

// Response represents a standard API response.
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	TraceID string      `json:"trace_id,omitempty"`
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}

// Health handles health check requests.
func Health(w http.ResponseWriter, r *http.Request) {
	logger.InfoContext(r.Context(), "health check")

	writeJSON(w, http.StatusOK, Response{
		Code:    0,
		Message: "ok",
		TraceID: logger.TraceIDFromContext(r.Context()),
	})
}

// Hello handles hello requests with optional name parameter.
func Hello(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = "World"
	}

	// Log with context fields
	logger.InfoContext(r.Context(), "processing hello request",
		"name", name,
	)

	writeJSON(w, http.StatusOK, Response{
		Code:    0,
		Message: "Hello, " + name + "!",
		TraceID: logger.TraceIDFromContext(r.Context()),
	})
}

// User simulates fetching user info.
func User(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("id")
	if userID == "" {
		logger.WarnContext(r.Context(), "missing user id parameter")
		writeJSON(w, http.StatusBadRequest, Response{
			Code:    400,
			Message: "user id is required",
			TraceID: logger.TraceIDFromContext(r.Context()),
		})
		return
	}

	// Simulate user lookup with detailed logging
	log := logger.GetLogger().WithContext(r.Context()).With(
		logger.FieldUserID, userID,
		logger.FieldModule, "user-handler",
	)

	log.Info("fetching user from database")

	// Simulate user data
	userData := map[string]interface{}{
		"id":    userID,
		"name":  "Demo User",
		"email": "demo@example.com",
	}

	log.Info("user fetched successfully")

	writeJSON(w, http.StatusOK, Response{
		Code:    0,
		Message: "success",
		Data:    userData,
		TraceID: logger.TraceIDFromContext(r.Context()),
	})
}

// Error simulates an error scenario for testing error logging.
func Error(w http.ResponseWriter, r *http.Request) {
	logger.ErrorContext(r.Context(), "simulated error occurred",
		logger.FieldError, "this is a test error",
		logger.FieldModule, "error-handler",
	)

	writeJSON(w, http.StatusInternalServerError, Response{
		Code:    500,
		Message: "simulated error",
		TraceID: logger.TraceIDFromContext(r.Context()),
	})
}

// Panic simulates a panic for testing recovery middleware.
func Panic(w http.ResponseWriter, r *http.Request) {
	logger.InfoContext(r.Context(), "about to panic")
	panic("simulated panic for testing")
}
