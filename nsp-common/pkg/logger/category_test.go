// Package logger provides a unified logging module for NSP platform microservices.
// category_test.go tests the per-category logger functionality.
package logger

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestCategoryFieldInjection verifies that each category logger automatically injects
// the log_category field with the correct value.
func TestCategoryFieldInjection(t *testing.T) {
	resetGlobalLogger()
	resetCategoryLoggers()

	cfg := &Config{
		Level:        LevelInfo,
		Format:       FormatJSON,
		ServiceName:  "cat-field-test",
		OutputPaths:  []string{"stdout"},
		EnableCaller: false,
	}
	if err := Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Sync()

	cases := []struct {
		name    string
		logFunc func() Logger
		want    string
	}{
		{"access", Access, string(CategoryAccess)},
		{"platform", Platform, string(CategoryPlatform)},
		{"business", Business, string(CategoryBusiness)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := captureOutput(t, func() {
				tc.logFunc().Info("category field test")
				Sync()
			})

			var entry map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &entry); err != nil {
				t.Fatalf("failed to parse JSON: %v\noutput: %s", err, output)
			}

			cat, ok := entry[FieldLogCategory].(string)
			if !ok || cat != tc.want {
				t.Errorf("expected %s=%q, got %v", FieldLogCategory, tc.want, entry[FieldLogCategory])
			}
		})
	}
}

// TestCategoryIndependentFileOutput verifies that a category logger writes to its own
// dedicated file when configured, while other categories write to global stdout.
func TestCategoryIndependentFileOutput(t *testing.T) {
	resetGlobalLogger()
	resetCategoryLoggers()

	tmpDir, err := os.MkdirTemp("", "logger_cat_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	accessLog := tmpDir + "/access.log"
	bizLog := tmpDir + "/business.log"

	cfg := &Config{
		Level:        LevelInfo,
		Format:       FormatJSON,
		ServiceName:  "cat-file-test",
		OutputPaths:  []string{"stdout"},
		EnableCaller: false,
		Categories: map[Category]CategoryConfig{
			CategoryAccess: {
				Level:       LevelInfo,
				OutputPaths: []string{accessLog},
			},
			CategoryBusiness: {
				Level:       LevelDebug,
				OutputPaths: []string{bizLog},
			},
		},
	}
	if err := Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	Access().Info("request received", FieldHTTPMethod, "GET", FieldPath, "/api/orders", FieldHTTPStatus, 200)
	Business().Info("order created", FieldBizDomain, "order", FieldBizID, "ORD-001", FieldOperation, "create")
	Platform().Warn("slow query", FieldLatencyMS, 3000)
	Sync()

	// Verify access.log
	accessContent := mustReadFile(t, accessLog)
	var accessEntry map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(accessContent)), &accessEntry); err != nil {
		t.Fatalf("failed to parse access log: %v\noutput: %s", err, accessContent)
	}
	if accessEntry[FieldLogCategory] != string(CategoryAccess) {
		t.Errorf("access log missing correct category, got: %v", accessEntry[FieldLogCategory])
	}
	if accessEntry[FieldHTTPMethod] != "GET" {
		t.Errorf("access log missing http_method, got: %v", accessEntry[FieldHTTPMethod])
	}

	// Verify business.log
	bizContent := mustReadFile(t, bizLog)
	var bizEntry map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(bizContent)), &bizEntry); err != nil {
		t.Fatalf("failed to parse business log: %v\noutput: %s", err, bizContent)
	}
	if bizEntry[FieldLogCategory] != string(CategoryBusiness) {
		t.Errorf("business log missing correct category, got: %v", bizEntry[FieldLogCategory])
	}
	if bizEntry[FieldBizDomain] != "order" {
		t.Errorf("business log missing biz_domain, got: %v", bizEntry[FieldBizDomain])
	}
}

// TestCategoryIndependentLevel verifies that per-category log level filtering works.
func TestCategoryIndependentLevel(t *testing.T) {
	resetGlobalLogger()
	resetCategoryLoggers()

	tmpDir, err := os.MkdirTemp("", "logger_cat_level_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	accessLog := tmpDir + "/access.log"
	bizLog := tmpDir + "/biz.log"

	cfg := &Config{
		Level:        LevelInfo,
		Format:       FormatJSON,
		ServiceName:  "cat-level-test",
		OutputPaths:  []string{"stdout"},
		EnableCaller: false,
		Categories: map[Category]CategoryConfig{
			// Access: warn level — debug and info should be suppressed
			CategoryAccess: {
				Level:       LevelWarn,
				OutputPaths: []string{accessLog},
			},
			// Business: debug level — all levels should appear
			CategoryBusiness: {
				Level:       LevelDebug,
				OutputPaths: []string{bizLog},
			},
		},
	}
	if err := Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	Access().Info("should be filtered out")
	Access().Warn("access warn message")
	Business().Debug("biz debug message")
	Business().Info("biz info message")
	Sync()

	// access log should only have warn message
	accessContent := mustReadFile(t, accessLog)
	if strings.Contains(accessContent, "should be filtered out") {
		t.Error("access log should not contain info message when level is warn")
	}
	if !strings.Contains(accessContent, "access warn message") {
		t.Error("access log should contain warn message")
	}

	// business log should have both debug and info messages
	bizContent := mustReadFile(t, bizLog)
	if !strings.Contains(bizContent, "biz debug message") {
		t.Error("business log should contain debug message when level is debug")
	}
	if !strings.Contains(bizContent, "biz info message") {
		t.Error("business log should contain info message when level is debug")
	}
}

// TestCategoryFallbackToGlobal verifies that a category without explicit config
// falls back to the global logger with the log_category field attached.
func TestCategoryFallbackToGlobal(t *testing.T) {
	resetGlobalLogger()
	resetCategoryLoggers()

	cfg := &Config{
		Level:        LevelInfo,
		Format:       FormatJSON,
		ServiceName:  "cat-fallback-test",
		OutputPaths:  []string{"stdout"},
		EnableCaller: false,
		// No Categories configured — all fall back to global.
	}
	if err := Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Sync()

	output := captureOutput(t, func() {
		Platform().Info("platform fallback message")
		Sync()
	})

	var entry map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &entry); err != nil {
		t.Fatalf("failed to parse JSON: %v\noutput: %s", err, output)
	}
	if entry[FieldLogCategory] != string(CategoryPlatform) {
		t.Errorf("expected log_category=%q, got %v", CategoryPlatform, entry[FieldLogCategory])
	}
	if entry["message"] != "platform fallback message" {
		t.Errorf("unexpected message: %v", entry["message"])
	}
}

// TestCategoryBackwardCompatibility verifies that existing global functions
// (logger.Info, logger.Error, etc.) continue to work unchanged after Init with Categories.
func TestCategoryBackwardCompatibility(t *testing.T) {
	resetGlobalLogger()
	resetCategoryLoggers()

	tmpDir, err := os.MkdirTemp("", "logger_compat_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &Config{
		Level:        LevelInfo,
		Format:       FormatJSON,
		ServiceName:  "compat-test",
		OutputPaths:  []string{"stdout"},
		EnableCaller: false,
		Categories: map[Category]CategoryConfig{
			CategoryAccess: {
				Level:       LevelInfo,
				OutputPaths: []string{tmpDir + "/access.log"},
			},
		},
	}
	if err := Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Sync()

	// Original global functions must still work.
	output := captureOutput(t, func() {
		Info("global info still works", "key", "value")
		Warn("global warn still works")
		Error("global error still works")
		Sync()
	})

	if !strings.Contains(output, "global info still works") {
		t.Error("global Info() stopped working after category init")
	}
	if !strings.Contains(output, "global warn still works") {
		t.Error("global Warn() stopped working after category init")
	}
	if !strings.Contains(output, "global error still works") {
		t.Error("global Error() stopped working after category init")
	}
}

// TestCategoryWith verifies that With() on a category logger preserves the log_category field.
func TestCategoryWith(t *testing.T) {
	resetGlobalLogger()
	resetCategoryLoggers()

	cfg := &Config{
		Level:        LevelInfo,
		Format:       FormatJSON,
		ServiceName:  "cat-with-test",
		OutputPaths:  []string{"stdout"},
		EnableCaller: false,
	}
	if err := Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer Sync()

	childLogger := Business().With(FieldBizDomain, "order")

	output := captureOutput(t, func() {
		childLogger.Info("child logger test")
		Sync()
	})

	var entry map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &entry); err != nil {
		t.Fatalf("failed to parse JSON: %v\noutput: %s", err, output)
	}
	if entry[FieldLogCategory] != string(CategoryBusiness) {
		t.Errorf("With() lost log_category field, got: %v", entry[FieldLogCategory])
	}
	if entry[FieldBizDomain] != "order" {
		t.Errorf("With() field not attached, got: %v", entry[FieldBizDomain])
	}
}

// TestCategoryConfigPreset verifies that CategoryConfig with only OutputPaths inherits
// the global rotation config.
func TestCategoryInheritedRotation(t *testing.T) {
	resetGlobalLogger()
	resetCategoryLoggers()

	tmpDir, err := os.MkdirTemp("", "logger_cat_rotation_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	globalRotation := &RotationConfig{MaxSize: 50, MaxBackups: 3, MaxAge: 7, Compress: false, LocalTime: true}
	cfg := &Config{
		Level:        LevelInfo,
		Format:       FormatJSON,
		ServiceName:  "cat-rotation-test",
		OutputPaths:  []string{"stdout"},
		EnableCaller: false,
		Rotation:     globalRotation,
		Categories: map[Category]CategoryConfig{
			CategoryAccess: {
				OutputPaths: []string{tmpDir + "/access.log"},
				// No Rotation set — should inherit global rotation
			},
		},
	}
	if err := Init(cfg); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	Access().Info("rotation inheritance test")
	Sync()

	content := mustReadFile(t, tmpDir+"/access.log")
	if !strings.Contains(content, "rotation inheritance test") {
		t.Error("access log should contain the test message")
	}
}

// resetCategoryLoggers clears category logger state for test isolation.
func resetCategoryLoggers() {
	categoryMu.Lock()
	defer categoryMu.Unlock()
	categoryLoggers = nil
}

// mustReadFile reads a file and fails the test if there's an error.
func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file %s: %v", path, err)
	}
	return string(content)
}
