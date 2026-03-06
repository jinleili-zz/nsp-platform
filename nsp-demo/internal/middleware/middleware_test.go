package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/paic/nsp-common/pkg/logger"
	"github.com/paic/nsp-common/pkg/trace"
)

func init() {
	// Initialize logger for tests
	cfg := &logger.Config{
		Level:        logger.LevelDebug,
		Format:       logger.FormatJSON,
		ServiceName:  "middleware-test",
		OutputPaths:  []string{"stdout"},
		EnableCaller: false,
	}
	logger.Init(cfg)

	// Set Gin to test mode
	gin.SetMode(gin.TestMode)
}

func TestGenerateTraceID(t *testing.T) {
	id1 := trace.NewTraceID()
	id2 := trace.NewTraceID()

	// Should be 32 hex characters (16 bytes)
	if len(id1) != 32 {
		t.Errorf("expected trace ID length 32, got %d", len(id1))
	}

	// Should be unique
	if id1 == id2 {
		t.Error("generated trace IDs should be unique")
	}
}

func TestGenerateSpanID(t *testing.T) {
	id1 := trace.NewSpanId()
	id2 := trace.NewSpanId()

	// Should be 16 hex characters (8 bytes)
	if len(id1) != 16 {
		t.Errorf("expected span ID length 16, got %d", len(id1))
	}

	// Should be unique
	if id1 == id2 {
		t.Error("generated span IDs should be unique")
	}
}

// Tests for Gin TraceMiddleware
func TestGinTraceMiddleware(t *testing.T) {
	instanceId := "test-instance"

	t.Run("generates trace ID when not provided", func(t *testing.T) {
		r := gin.New()
		r.Use(trace.TraceMiddleware(instanceId))
		r.GET("/test", func(c *gin.Context) {
			traceID := logger.TraceIDFromContext(c.Request.Context())
			spanID := logger.SpanIDFromContext(c.Request.Context())

			if traceID == "" {
				t.Error("trace_id should be set in context")
			}
			if spanID == "" {
				t.Error("span_id should be set in context")
			}

			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		// Check B3 response headers
		traceID := rec.Header().Get(trace.HeaderTraceID)
		requestID := rec.Header().Get(trace.HeaderRequestID)

		if traceID == "" {
			t.Error("X-B3-TraceId header should be set")
		}
		if requestID == "" {
			t.Error("X-Request-Id header should be set")
		}
		// X-Request-Id should equal TraceID for compatibility
		if requestID != traceID {
			t.Errorf("X-Request-Id should equal X-B3-TraceId, got %s vs %s", requestID, traceID)
		}
	})

	t.Run("uses provided B3 trace ID", func(t *testing.T) {
		r := gin.New()
		r.Use(trace.TraceMiddleware(instanceId))
		r.GET("/test", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/test", nil)
		// Use valid 32-char hex string for TraceID
		req.Header.Set(trace.HeaderTraceID, "abcdef1234567890abcdef1234567890")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		traceID := rec.Header().Get(trace.HeaderTraceID)
		if traceID != "abcdef1234567890abcdef1234567890" {
			t.Errorf("expected provided trace ID, got %s", traceID)
		}
	})

	t.Run("uses X-Request-Id as fallback for trace ID", func(t *testing.T) {
		r := gin.New()
		r.Use(trace.TraceMiddleware(instanceId))
		r.GET("/test", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set(trace.HeaderRequestID, "request-id-fallback-12345")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		traceID := rec.Header().Get(trace.HeaderTraceID)
		if traceID != "request-id-fallback-12345" {
			t.Errorf("expected X-Request-Id as trace ID, got %s", traceID)
		}
	})

	t.Run("TraceFromGin returns TraceContext", func(t *testing.T) {
		r := gin.New()
		r.Use(trace.TraceMiddleware(instanceId))
		r.GET("/test", func(c *gin.Context) {
			tc, ok := trace.TraceFromGin(c)
			if !ok {
				t.Error("TraceFromGin should return TraceContext")
			}
			if tc == nil {
				t.Error("TraceContext should not be nil")
			}
			if tc.TraceID == "" {
				t.Error("TraceID should not be empty")
			}
			if tc.SpanId == "" {
				t.Error("SpanId should not be empty")
			}
			if tc.InstanceId != instanceId {
				t.Errorf("expected InstanceId %s, got %s", instanceId, tc.InstanceId)
			}
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	})

	t.Run("ParentSpanId is set from upstream X-B3-SpanId", func(t *testing.T) {
		r := gin.New()
		r.Use(trace.TraceMiddleware(instanceId))
		r.GET("/test", func(c *gin.Context) {
			tc, _ := trace.TraceFromGin(c)
			// Use valid 16-char hex string for SpanId
			if tc.ParentSpanId != "1234567890abcdef" {
				t.Errorf("expected ParentSpanId from upstream, got %s", tc.ParentSpanId)
			}
			c.Status(http.StatusOK)
		})

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set(trace.HeaderTraceID, "abcdef1234567890abcdef1234567890")
		req.Header.Set(trace.HeaderSpanId, "1234567890abcdef")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	})
}

// Tests for net/http Logger middleware
func TestLoggerMiddleware(t *testing.T) {
	called := false
	handler := Logger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	// Add trace context
	ctx := logger.ContextWithTraceID(req.Context(), "test-trace-id")
	ctx = logger.ContextWithSpanID(ctx, "test-span-id")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("handler should be called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

// Tests for Gin Logger middleware
func TestGinLoggerMiddleware(t *testing.T) {
	instanceId := "test-instance"

	r := gin.New()
	r.Use(trace.TraceMiddleware(instanceId))
	r.Use(GinLogger())

	called := false
	r.GET("/test", func(c *gin.Context) {
		called = true
		c.String(http.StatusOK, "OK")
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if !called {
		t.Error("handler should be called")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

// Tests for net/http Recovery middleware
func TestRecoveryMiddleware(t *testing.T) {
	handler := Recovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	ctx := logger.ContextWithTraceID(req.Context(), "test-trace-id")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()

	// Should not panic
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}
}

// Tests for Gin Recovery middleware
func TestGinRecoveryMiddleware(t *testing.T) {
	instanceId := "test-instance"

	r := gin.New()
	r.Use(trace.TraceMiddleware(instanceId))
	r.Use(GinRecovery())
	r.GET("/panic", func(c *gin.Context) {
		panic("test panic")
	})

	req := httptest.NewRequest("GET", "/panic", nil)
	rec := httptest.NewRecorder()

	// Should not panic
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}

	// Check response body
	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["code"] != float64(500) {
		t.Errorf("expected code 500, got %v", resp["code"])
	}
	if resp["message"] != "Internal Server Error" {
		t.Errorf("expected message 'Internal Server Error', got %v", resp["message"])
	}
}

func TestResponseWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	// Test WriteHeader
	rw.WriteHeader(http.StatusCreated)
	if rw.statusCode != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rw.statusCode)
	}

	// Test Write
	n, err := rw.Write([]byte("hello"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if rw.written != 5 {
		t.Errorf("expected written=5, got %d", rw.written)
	}
}
