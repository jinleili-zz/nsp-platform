package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/paic/nsp-common/pkg/logger"
)

func init() {
	// Initialize logger for tests
	cfg := &logger.Config{
		Level:        logger.LevelDebug,
		Format:       logger.FormatJSON,
		ServiceName:  "handler-test",
		OutputPaths:  []string{"stdout"},
		EnableCaller: false,
	}
	logger.Init(cfg)

	// Set Gin to test mode
	gin.SetMode(gin.TestMode)
}

func setupRouter() *gin.Engine {
	r := gin.New()
	return r
}

func makeGinRequest(t *testing.T, r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	// Add trace context
	ctx := logger.ContextWithTraceID(req.Context(), "test-trace-123")
	ctx = logger.ContextWithSpanID(ctx, "test-span-456")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestHealth(t *testing.T) {
	r := setupRouter()
	r.GET("/health", Health)

	rec := makeGinRequest(t, r, "GET", "/health")

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Code != 0 {
		t.Errorf("expected code 0, got %d", resp.Code)
	}
	if resp.Message != "ok" {
		t.Errorf("expected message 'ok', got %s", resp.Message)
	}
	if resp.TraceID != "test-trace-123" {
		t.Errorf("expected trace_id 'test-trace-123', got %s", resp.TraceID)
	}
}

func TestHello(t *testing.T) {
	t.Run("without name parameter", func(t *testing.T) {
		r := setupRouter()
		r.GET("/hello", Hello)

		rec := makeGinRequest(t, r, "GET", "/hello")

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}

		var resp Response
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Message != "Hello, World!" {
			t.Errorf("expected 'Hello, World!', got %s", resp.Message)
		}
	})

	t.Run("with name parameter", func(t *testing.T) {
		r := setupRouter()
		r.GET("/hello", Hello)

		rec := makeGinRequest(t, r, "GET", "/hello?name=Alice")

		var resp Response
		json.NewDecoder(rec.Body).Decode(&resp)

		if resp.Message != "Hello, Alice!" {
			t.Errorf("expected 'Hello, Alice!', got %s", resp.Message)
		}
	})
}

func TestUser(t *testing.T) {
	t.Run("without user id", func(t *testing.T) {
		r := setupRouter()
		r.GET("/user", User)

		rec := makeGinRequest(t, r, "GET", "/user")

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rec.Code)
		}

		var resp Response
		json.NewDecoder(rec.Body).Decode(&resp)

		if resp.Code != 400 {
			t.Errorf("expected code 400, got %d", resp.Code)
		}
	})

	t.Run("with user id", func(t *testing.T) {
		r := setupRouter()
		r.GET("/user", User)

		rec := makeGinRequest(t, r, "GET", "/user?id=123")

		if rec.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", rec.Code)
		}

		var resp Response
		json.NewDecoder(rec.Body).Decode(&resp)

		if resp.Code != 0 {
			t.Errorf("expected code 0, got %d", resp.Code)
		}
		if resp.Data == nil {
			t.Error("expected user data")
		}

		userData, ok := resp.Data.(map[string]interface{})
		if !ok {
			t.Fatal("expected data to be a map")
		}
		if userData["id"] != "123" {
			t.Errorf("expected user id '123', got %v", userData["id"])
		}
	})
}

func TestError(t *testing.T) {
	r := setupRouter()
	r.GET("/error", Error)

	rec := makeGinRequest(t, r, "GET", "/error")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}

	var resp Response
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Code != 500 {
		t.Errorf("expected code 500, got %d", resp.Code)
	}
}

func TestPanic(t *testing.T) {
	r := setupRouter()
	r.GET("/panic", Panic)

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}
	}()

	makeGinRequest(t, r, "GET", "/panic")
}
