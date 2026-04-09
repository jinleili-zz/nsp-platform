package saga

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jinleili-zz/nsp-platform/logger"
)

func captureSagaLogOutput(t *testing.T, fn func()) string {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "saga_logger_*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	oldStdout, err := syscall.Dup(int(os.Stdout.Fd()))
	if err != nil {
		tmpFile.Close()
		t.Fatalf("failed to dup stdout: %v", err)
	}
	defer func() {
		syscall.Dup2(oldStdout, int(os.Stdout.Fd()))
		syscall.Close(oldStdout)
	}()

	if err := syscall.Dup2(int(tmpFile.Fd()), int(os.Stdout.Fd())); err != nil {
		tmpFile.Close()
		t.Fatalf("failed to redirect stdout: %v", err)
	}

	fn()

	tmpFile.Close()

	content, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("failed to read captured log output: %v", err)
	}
	return string(content)
}

func initSagaTestLogger(t *testing.T) {
	t.Helper()

	if err := logger.Init(&logger.Config{
		Level:        logger.LevelDebug,
		Format:       logger.FormatJSON,
		ServiceName:  "saga-test",
		OutputPaths:  []string{"stdout"},
		EnableCaller: false,
	}); err != nil {
		t.Fatalf("logger.Init() error = %v", err)
	}
	t.Cleanup(func() {
		_ = logger.Sync()
	})
}

type retryErrorStore struct {
	*regressionStore
}

func (s *retryErrorStore) IncrementStepRetry(ctx context.Context, stepID string) error {
	return errors.New("retry store unavailable")
}

func TestCoordinatorSubmitLogsWithDefaultGlobalFallback(t *testing.T) {
	initSagaTestLogger(t)

	coordinator := NewCoordinator(newRegressionStore(), nil, nil, &CoordinatorConfig{
		InstanceID: "coord-1",
	})
	coordinator.taskQueue = make(chan string, 1)
	coordinator.taskQueue <- "busy"

	output := captureSagaLogOutput(t, func() {
		if got := coordinator.Submit("tx-overflow"); got {
			t.Fatal("Submit() = true, want false when queue is full")
		}
		_ = logger.Sync()
	})

	for _, want := range []string{
		`"component":"saga"`,
		`"tx_id":"tx-overflow"`,
		`"instance_id":"coord-1"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %s, got %s", want, output)
		}
	}
}

func TestExecutorUsesInjectedLoggerForRuntimeErrors(t *testing.T) {
	initSagaTestLogger(t)

	executor := NewExecutor(&retryErrorStore{regressionStore: newRegressionStore()}, &ExecutorConfig{
		HTTPTimeout: time.Second,
		Logger:      logger.GetLogger().With("custom_logger", "executor"),
	}, nil)

	ctx := logger.ContextWithTraceID(context.Background(), "trace-exec")
	ctx = logger.ContextWithSpanID(ctx, "span-exec")
	step := &Step{
		ID:       "step-1",
		Name:     "charge",
		Status:   StepStatusRunning,
		MaxRetry: 1,
	}

	output := captureSagaLogOutput(t, func() {
		_ = executor.handleHTTPError(ctx, step, errors.New("boom"))
		_ = logger.Sync()
	})

	for _, want := range []string{
		`"custom_logger":"executor"`,
		`"step_id":"step-1"`,
		`"step_name":"charge"`,
		`"trace_id":"trace-exec"`,
		`"span_id":"span-exec"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %s, got %s", want, output)
		}
	}
}

func TestPollerRehydratesTraceContextForBackgroundLogs(t *testing.T) {
	initSagaTestLogger(t)

	store := newRegressionStore()
	tx := &Transaction{
		ID:     "tx-poll",
		Status: TxStatusRunning,
		Payload: map[string]any{
			"_trace_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"_span_id":  "bbbbbbbbbbbbbbbb",
		},
	}
	step := &Step{
		ID:            "step-poll",
		TransactionID: tx.ID,
		Name:          "device-status",
		Status:        StepStatusPolling,
		PollURL:       "http://unit.test/poll",
		PollMethod:    "GET",
	}
	store.put(tx, step)

	executor := NewExecutor(store, &ExecutorConfig{
		HTTPTimeout: time.Second,
		Logger:      logger.GetLogger().With("custom_logger", "executor"),
	}, nil)
	executor.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})}

	poller := NewPoller(store, executor, &PollerConfig{
		InstanceID: "poller-1",
		Logger:     logger.GetLogger().With("custom_logger", "poller"),
	})

	output := captureSagaLogOutput(t, func() {
		poller.processPollTask(context.Background(), &PollTask{
			StepID:        step.ID,
			TransactionID: tx.ID,
		})
		_ = logger.Sync()
	})

	for _, want := range []string{
		`"custom_logger":"poller"`,
		`"tx_id":"tx-poll"`,
		`"step_id":"step-poll"`,
		`"step_name":"device-status"`,
		`"trace_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`,
		`"span_id":"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %s, got %s", want, output)
		}
	}
}
