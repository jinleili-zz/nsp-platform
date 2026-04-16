package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jinleili-zz/nsp-platform/saga"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dsn, source := exampleDSN()
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "missing PostgreSQL DSN: set SAGA_EXAMPLE_DSN (or TEST_DSN)")
		os.Exit(1)
	}

	baseURL, shutdownDemoService, err := startDemoService()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start demo service: %v\n", err)
		os.Exit(1)
	}
	defer shutdownDemoService(context.Background())

	engine, err := saga.NewEngine(&saga.Config{
		DSN:               dsn,
		WorkerCount:       2,
		PollBatchSize:     10,
		PollScanInterval:  200 * time.Millisecond,
		CoordScanInterval: 300 * time.Millisecond,
		HTTPTimeout:       5 * time.Second,
		InstanceID:        fmt.Sprintf("saga-demo-%d", os.Getpid()),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create saga engine: %v\n", err)
		os.Exit(1)
	}
	defer engine.Stop()

	migrationPath, err := ensureSagaSchema(engine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ensure saga schema: %v\n", err)
		os.Exit(1)
	}

	if err := engine.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "start saga engine: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== NSP Saga Demo ===")
	fmt.Printf("Using DSN from %s\n", source)
	fmt.Printf("Loaded schema from %s\n", migrationPath)
	fmt.Printf("Demo service: %s\n", baseURL)

	if err := runSubmitExample(ctx, engine, baseURL); err != nil {
		fmt.Fprintf(os.Stderr, "submit demo failed: %v\n", err)
		os.Exit(1)
	}

	if err := runSubmitAndWaitExample(ctx, engine, baseURL); err != nil {
		fmt.Fprintf(os.Stderr, "submit-and-wait demo failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("\nDemo complete.")
}

type demoService struct {
	asyncSeq atomic.Int32
	syncSeq  atomic.Int32

	mu        sync.Mutex
	pollCount map[string]int
}

func newDemoService() *demoService {
	return &demoService{
		pollCount: make(map[string]int),
	}
}

func (s *demoService) handler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/inventory/reserve":
		id := s.syncSeq.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{
			"reservation_id": fmt.Sprintf("RSV-%03d", id),
			"status":         "reserved",
		})
	case "/inventory/release":
		w.WriteHeader(http.StatusOK)
	case "/orders/create":
		id := s.syncSeq.Add(1)
		writeJSON(w, http.StatusOK, map[string]any{
			"order_id": fmt.Sprintf("ORD-%03d", id),
			"status":   "created",
		})
	case "/orders/cancel":
		w.WriteHeader(http.StatusOK)
	case "/devices/apply":
		taskID := fmt.Sprintf("TASK-%03d", s.asyncSeq.Add(1))

		s.mu.Lock()
		s.pollCount[taskID] = 0
		s.mu.Unlock()

		writeJSON(w, http.StatusOK, map[string]any{
			"task_id":  taskID,
			"accepted": true,
		})
	case "/devices/status":
		taskID := r.URL.Query().Get("task_id")
		if taskID == "" {
			http.Error(w, "missing task_id", http.StatusBadRequest)
			return
		}

		s.mu.Lock()
		count, ok := s.pollCount[taskID]
		if !ok {
			s.mu.Unlock()
			http.Error(w, "unknown task_id", http.StatusNotFound)
			return
		}
		count++
		s.pollCount[taskID] = count
		s.mu.Unlock()

		status := "pending"
		if count >= 3 {
			status = "success"
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"task_id":    taskID,
			"status":     status,
			"poll_count": count,
		})
	case "/devices/rollback":
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func startDemoService() (string, func(context.Context) error, error) {
	service := newDemoService()
	server := &http.Server{
		Handler:      http.HandlerFunc(service.handler),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "demo service stopped unexpectedly: %v\n", err)
		}
	}()

	return "http://" + listener.Addr().String(), server.Shutdown, nil
}

func exampleDSN() (string, string) {
	if v := os.Getenv("SAGA_EXAMPLE_DSN"); v != "" {
		return v, "SAGA_EXAMPLE_DSN"
	}
	if v := os.Getenv("TEST_DSN"); v != "" {
		return v, "TEST_DSN"
	}
	return "", ""
}

func ensureSagaSchema(engine *saga.Engine) (string, error) {
	migrationPath, migrationSQL, err := loadSagaMigration()
	if err != nil {
		return "", err
	}

	if _, err := engine.DB().Exec(string(migrationSQL)); err != nil {
		return "", fmt.Errorf("execute migration %s: %w", migrationPath, err)
	}

	return migrationPath, nil
}

func loadSagaMigration() (string, []byte, error) {
	candidates := []string{
		filepath.Join("saga", "migrations", "saga.sql"),
		filepath.Join("..", "..", "saga", "migrations", "saga.sql"),
	}

	var lastErr error
	for _, path := range candidates {
		content, err := os.ReadFile(path)
		if err == nil {
			return path, content, nil
		}
		lastErr = err
	}

	return "", nil, fmt.Errorf("read saga migration file: %w", lastErr)
}

func runSubmitExample(ctx context.Context, engine *saga.Engine, baseURL string) error {
	fmt.Println("\n--- Submit: immediate return, caller polls Query ---")

	def, err := saga.NewSaga("demo-submit-async").
		AddStep(saga.Step{
			Name:              "下发设备配置",
			Type:              saga.StepTypeAsync,
			ActionMethod:      "POST",
			ActionURL:         baseURL + "/devices/apply",
			ActionPayload:     map[string]any{"device_id": "DEV-001", "profile": "edge"},
			CompensateMethod:  "POST",
			CompensateURL:     baseURL + "/devices/rollback",
			CompensatePayload: map[string]any{"device_id": "DEV-001"},
			PollURL:           baseURL + "/devices/status?task_id={action_response.task_id}",
			PollMethod:        "GET",
			PollIntervalSec:   1,
			PollMaxTimes:      10,
			PollSuccessPath:   "$.status",
			PollSuccessValue:  "success",
			PollFailurePath:   "$.status",
			PollFailureValue:  "failed",
		}).
		Build()
	if err != nil {
		return fmt.Errorf("build submit demo saga: %w", err)
	}

	txID, err := engine.Submit(ctx, def)
	if err != nil {
		return fmt.Errorf("submit async saga: %w", err)
	}

	fmt.Printf("Submit returned immediately. txID=%s\n", txID)

	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	status, err := waitByQuery(waitCtx, engine, txID, 500*time.Millisecond)
	if err != nil {
		return fmt.Errorf("poll query for tx %s: %w", txID, err)
	}
	if status.Status == "failed" {
		if status.LastError != "" {
			return fmt.Errorf("tx %s reached failed terminal state: %s", txID, status.LastError)
		}
		return fmt.Errorf("tx %s reached failed terminal state", txID)
	}

	fmt.Printf("Query observed terminal status. txID=%s status=%s\n", txID, status.Status)
	printSteps(status)
	return nil
}

func runSubmitAndWaitExample(ctx context.Context, engine *saga.Engine, baseURL string) error {
	fmt.Println("\n--- SubmitAndWait: wait for terminal status in one call ---")

	def, err := saga.NewSaga("demo-submit-and-wait-sync").
		WithPayload(map[string]any{
			"sku":   "SKU-001",
			"count": 2,
		}).
		AddStep(saga.Step{
			Name:         "预占库存",
			Type:         saga.StepTypeSync,
			ActionMethod: "POST",
			ActionURL:    baseURL + "/inventory/reserve",
			ActionPayload: map[string]any{
				"sku":   "{transaction.payload.sku}",
				"count": "{transaction.payload.count}",
			},
			CompensateMethod: "POST",
			CompensateURL:    baseURL + "/inventory/release",
			CompensatePayload: map[string]any{
				"sku": "{transaction.payload.sku}",
			},
		}).
		AddStep(saga.Step{
			Name:         "创建订单",
			Type:         saga.StepTypeSync,
			ActionMethod: "POST",
			ActionURL:    baseURL + "/orders/create",
			ActionPayload: map[string]any{
				"sku":   "{transaction.payload.sku}",
				"count": "{transaction.payload.count}",
			},
			CompensateMethod: "POST",
			CompensateURL:    baseURL + "/orders/cancel",
		}).
		Build()
	if err != nil {
		return fmt.Errorf("build submit-and-wait saga: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	txID, status, err := engine.SubmitAndWait(waitCtx, def)
	if err != nil {
		switch {
		case errors.Is(err, saga.ErrTransactionFailed):
			return fmt.Errorf("tx %s reached failed terminal state: %w", txID, err)
		case errors.Is(err, saga.ErrTransactionDisappeared):
			return fmt.Errorf("tx %s disappeared while waiting: %w", txID, err)
		default:
			return fmt.Errorf("submit-and-wait tx %s: %w", txID, err)
		}
	}

	fmt.Printf("SubmitAndWait returned terminal status directly. txID=%s status=%s\n", txID, status.Status)
	printSteps(status)
	return nil
}

func waitByQuery(ctx context.Context, engine *saga.Engine, txID string, interval time.Duration) (*saga.TransactionStatus, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastSummary := ""
	for {
		status, err := engine.Query(ctx, txID)
		if err != nil {
			return nil, err
		}

		summary := fmt.Sprintf("  polled status: %s (current_step=%d)", status.Status, status.CurrentStep)
		if summary != lastSummary {
			fmt.Println(summary)
			lastSummary = summary
		}

		if isTerminalStatus(status.Status) {
			return status, nil
		}

		select {
		case <-ctx.Done():
			return status, ctx.Err()
		case <-ticker.C:
		}
	}
}

func isTerminalStatus(status string) bool {
	return status == "succeeded" || status == "failed"
}

func printSteps(status *saga.TransactionStatus) {
	for _, step := range status.Steps {
		fmt.Printf("  step[%d] %s => %s\n", step.Index, step.Name, step.Status)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
