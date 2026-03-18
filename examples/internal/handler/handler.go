// Package handler provides HTTP request handlers.
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jinleili-zz/nsp-platform/auth"
	"github.com/jinleili-zz/nsp-platform/logger"
)

// Response represents a standard API response.
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	TraceID string      `json:"trace_id,omitempty"`
}

// Health handles health check requests.
func Health(c *gin.Context) {
	ctx := c.Request.Context()
	logger.InfoContext(ctx, "health check")

	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "ok",
		TraceID: logger.TraceIDFromContext(ctx),
	})
}

// Hello handles hello requests with optional name parameter.
func Hello(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Query("name")
	if name == "" {
		name = "World"
	}

	// Log with context fields
	logger.InfoContext(ctx, "processing hello request",
		"name", name,
	)

	// Get credential info if available
	var clientLabel string
	if cred, ok := auth.CredentialFromGin(c); ok {
		clientLabel = cred.Label
	}

	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "Hello, " + name + "!",
		Data: gin.H{
			"client": clientLabel,
		},
		TraceID: logger.TraceIDFromContext(ctx),
	})
}

// User simulates fetching user info.
func User(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.Query("id")
	if userID == "" {
		logger.WarnContext(ctx, "missing user id parameter")
		c.JSON(http.StatusBadRequest, Response{
			Code:    400,
			Message: "user id is required",
			TraceID: logger.TraceIDFromContext(ctx),
		})
		return
	}

	// Simulate user lookup with detailed logging
	log := logger.GetLogger().WithContext(ctx).With(
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

	// Include credential info if available
	if cred, ok := auth.CredentialFromGin(c); ok {
		userData["requested_by"] = cred.Label
	}

	log.Info("user fetched successfully")

	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "success",
		Data:    userData,
		TraceID: logger.TraceIDFromContext(ctx),
	})
}

// Error simulates an error scenario for testing error logging.
func Error(c *gin.Context) {
	ctx := c.Request.Context()
	logger.ErrorContext(ctx, "simulated error occurred",
		logger.FieldError, "this is a test error",
		logger.FieldModule, "error-handler",
	)

	c.JSON(http.StatusInternalServerError, Response{
		Code:    500,
		Message: "simulated error",
		TraceID: logger.TraceIDFromContext(ctx),
	})
}

// Panic simulates a panic for testing recovery middleware.
func Panic(c *gin.Context) {
	ctx := c.Request.Context()
	logger.InfoContext(ctx, "about to panic")
	panic("simulated panic for testing")
}
