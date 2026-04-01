// Package logger provides tests for sugared logging behavior.
package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

func parseSingleLogEntry(t *testing.T, output string) map[string]any {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			return entry
		}
	}

	t.Fatalf("failed to parse log entry from output: %s", output)
	return nil
}

func newSugarTestConfig(serviceName string) *Config {
	return &Config{
		Level:            LevelDebug,
		Format:           FormatJSON,
		ServiceName:      serviceName,
		OutputPaths:      []string{"stdout"},
		EnableCaller:     true,
		EnableStackTrace: false,
		Sampling:         nil,
	}
}

func callerLineForNextLine() int {
	_, _, line, ok := runtime.Caller(1)
	if !ok {
		return 0
	}
	return line + 1
}

// TestSugaredLoggerFormattedMethods tests formatted logging output.
func TestSugaredLoggerFormattedMethods(t *testing.T) {
	resetGlobalLogger()

	if err := Init(newSugarTestConfig("sugar-format-test")); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Sync()

	sugar := GetLogger().Sugar()
	tests := []struct {
		name    string
		logFunc func()
		level   string
		message string
	}{
		{
			name:    "debugf",
			logFunc: func() { sugar.Debugf("user %d logged in", 42) },
			level:   "debug",
			message: "user 42 logged in",
		},
		{
			name:    "infof",
			logFunc: func() { sugar.Infof("user %d logged in", 42) },
			level:   "info",
			message: "user 42 logged in",
		},
		{
			name:    "warnf",
			logFunc: func() { sugar.Warnf("user %d logged in", 42) },
			level:   "warn",
			message: "user 42 logged in",
		},
		{
			name:    "errorf",
			logFunc: func() { sugar.Errorf("user %d logged in", 42) },
			level:   "error",
			message: "user 42 logged in",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := captureOutput(t, func() {
				tt.logFunc()
				Sync()
			})

			entry := parseSingleLogEntry(t, output)
			if entry["level"] != tt.level {
				t.Fatalf("expected level %q, got %v", tt.level, entry["level"])
			}
			if entry["message"] != tt.message {
				t.Fatalf("expected message %q, got %v", tt.message, entry["message"])
			}
		})
	}
}

// TestSugaredLoggerFatalf tests fatal formatted logging without exiting the test process.
func TestSugaredLoggerFatalf(t *testing.T) {
	resetGlobalLogger()

	if err := Init(newSugarTestConfig("sugar-fatal-test")); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Sync()

	originalExit := exitFunc
	defer func() { exitFunc = originalExit }()

	var exitCode int
	exitFunc = func(code int) {
		exitCode = code
	}

	output := captureOutput(t, func() {
		GetLogger().Sugar().Fatalf("fatal %s", "message")
		Sync()
	})

	entry := parseSingleLogEntry(t, output)
	if entry["message"] != "fatal message" {
		t.Fatalf("expected fatal formatted message, got %v", entry["message"])
	}
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
}

// TestSugaredLoggerContextf tests context-aware formatted logging.
func TestSugaredLoggerContextf(t *testing.T) {
	resetGlobalLogger()

	if err := Init(newSugarTestConfig("sugar-context-test")); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Sync()

	ctx := ContextWithSpanID(ContextWithTraceID(context.Background(), "trace-123"), "span-456")

	output := captureOutput(t, func() {
		GetLogger().Sugar().InfoContextf(ctx, "req %s done", "R-001")
		Sync()
	})

	entry := parseSingleLogEntry(t, output)
	if entry["message"] != "req R-001 done" {
		t.Fatalf("expected formatted context message, got %v", entry["message"])
	}
	if entry[FieldTraceID] != "trace-123" {
		t.Fatalf("expected trace_id, got %v", entry[FieldTraceID])
	}
	if entry[FieldSpanID] != "span-456" {
		t.Fatalf("expected span_id, got %v", entry[FieldSpanID])
	}
}

// TestSugaredLoggerWithChain tests With chaining across Logger and SugaredLogger.
func TestSugaredLoggerWithChain(t *testing.T) {
	resetGlobalLogger()

	if err := Init(newSugarTestConfig("sugar-with-test")); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Sync()

	output := captureOutput(t, func() {
		With("module", "auth").Sugar().Infof("login %s", "ok")
		Sync()
	})

	entry := parseSingleLogEntry(t, output)
	if entry["message"] != "login ok" {
		t.Fatalf("expected formatted message, got %v", entry["message"])
	}
	if entry["module"] != "auth" {
		t.Fatalf("expected inherited module field, got %v", entry["module"])
	}

	output = captureOutput(t, func() {
		GetLogger().Sugar().With("request_id", "req-001").Infof("request %s", "done")
		Sync()
	})

	entry = parseSingleLogEntry(t, output)
	if entry["request_id"] != "req-001" {
		t.Fatalf("expected request_id field, got %v", entry["request_id"])
	}
}

// TestCategorySugaredLogger tests sugar logging for category loggers.
func TestCategorySugaredLogger(t *testing.T) {
	resetGlobalLogger()
	resetCategoryManager()

	tmpDir, err := os.MkdirTemp("", "logger_sugar_category_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := FileMultiCategoryConfig("sugar-category-test", tmpDir)
	if err := InitMultiCategory(cfg); err != nil {
		t.Fatalf("InitMultiCategory failed: %v", err)
	}
	defer SyncAll()

	Access().Sugar().Infof("GET %s %d", "/health", 200)
	SyncAll()

	accessLog, err := os.ReadFile(tmpDir + "/access.log")
	if err != nil {
		t.Fatalf("failed to read access log: %v", err)
	}
	platformLog, err := os.ReadFile(tmpDir + "/platform.log")
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("failed to read platform log: %v", err)
	}
	businessLog, err := os.ReadFile(tmpDir + "/app.log")
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("failed to read business log: %v", err)
	}

	accessEntry := parseSingleLogEntry(t, string(accessLog))
	if accessEntry["message"] != "GET /health 200" {
		t.Fatalf("expected access log message, got %v", accessEntry["message"])
	}
	if accessEntry[FieldCategory] != string(CategoryAccess) {
		t.Fatalf("expected log_category access, got %v", accessEntry[FieldCategory])
	}
	if strings.Contains(string(platformLog), "GET /health 200") {
		t.Fatal("access sugar log should not be written to platform log")
	}
	if strings.Contains(string(businessLog), "GET /health 200") {
		t.Fatal("access sugar log should not be written to business log")
	}
}

// TestGlobalInfofCaller tests that package-level Infof preserves the business caller location.
func TestGlobalInfofCaller(t *testing.T) {
	resetGlobalLogger()

	if err := Init(newSugarTestConfig("sugar-global-caller-test")); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Sync()

	var expectedLine int
	output := captureOutput(t, func() {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("failed to get caller")
		}
		if !strings.HasSuffix(file, "logger/sugar_test.go") {
			t.Fatalf("unexpected caller file: %s", file)
		}
		expectedLine = callerLineForNextLine()
		Infof("hello %s", "world")
		Sync()
	})

	entry := parseSingleLogEntry(t, output)
	if entry["message"] != "hello world" {
		t.Fatalf("expected formatted message, got %v", entry["message"])
	}

	caller, _ := entry["caller"].(string)
	expectedSuffix := fmt.Sprintf("logger/sugar_test.go:%d", expectedLine)
	if !strings.HasSuffix(caller, expectedSuffix) {
		t.Fatalf("expected caller suffix %q, got %q", expectedSuffix, caller)
	}
}
