package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yourorg/nsp-common/pkg/logger"
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
}

func makeRequest(t *testing.T, method, path string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	// Add trace context
	ctx := logger.ContextWithTraceID(req.Context(), "test-trace-123")
	ctx = logger.ContextWithSpanID(ctx, "test-span-456")
	req = req.WithContext(ctx)
	return httptest.NewRecorder(), req
}

func TestHealth(t *testing.T) {
	rec, req := makeRequest(t, "GET", "/health")

	Health(rec, req)

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
		rec, req := makeRequest(t, "GET", "/hello")

		Hello(rec, req)

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
		rec, req := makeRequest(t, "GET", "/hello?name=Alice")

		Hello(rec, req)

		var resp Response
		json.NewDecoder(rec.Body).Decode(&resp)

		if resp.Message != "Hello, Alice!" {
			t.Errorf("expected 'Hello, Alice!', got %s", resp.Message)
		}
	})
}

func TestUser(t *testing.T) {
	t.Run("without user id", func(t *testing.T) {
		rec, req := makeRequest(t, "GET", "/user")

		User(rec, req)

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
		rec, req := makeRequest(t, "GET", "/user?id=123")

		User(rec, req)

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
	rec, req := makeRequest(t, "GET", "/error")

	Error(rec, req)

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
	rec, req := makeRequest(t, "GET", "/panic")

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}
	}()

	Panic(rec, req)
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()

	resp := Response{
		Code:    0,
		Message: "test",
		Data:    map[string]string{"key": "value"},
	}

	writeJSON(rec, http.StatusOK, resp)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got %s", contentType)
	}

	var decoded Response
	if err := json.NewDecoder(rec.Body).Decode(&decoded); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if decoded.Message != "test" {
		t.Errorf("expected message 'test', got %s", decoded.Message)
	}
}
