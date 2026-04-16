package saga

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type waitQueryResult struct {
	tx  *Transaction
	err error
}

type submitAndWaitStore struct {
	mockStore
	txResults []waitQueryResult
	steps     []*Step
	getTxCall int
}

func (s *submitAndWaitStore) GetTransaction(ctx context.Context, id string) (*Transaction, error) {
	if s.getTxCall >= len(s.txResults) {
		last := s.txResults[len(s.txResults)-1]
		return last.tx, last.err
	}
	result := s.txResults[s.getTxCall]
	s.getTxCall++
	return result.tx, result.err
}

func (s *submitAndWaitStore) GetSteps(ctx context.Context, txID string) ([]*Step, error) {
	if s.steps == nil {
		return []*Step{
			{Index: 0, Name: "step-1", Status: StepStatusPending},
		}, nil
	}
	return s.steps, nil
}

// setSubmitAndWaitTestIntervals mutates package-level timing knobs and is not safe for parallel tests.
func setSubmitAndWaitTestIntervals(t *testing.T, pollInterval, retryBackoff, retryBackoffMax time.Duration, retryLimit int) {
	t.Helper()

	prevPollInterval := submitAndWaitPollInterval
	prevRetryBackoff := submitAndWaitQueryRetryBackoff
	prevRetryBackoffMax := submitAndWaitQueryRetryBackoffMax
	prevRetryLimit := submitAndWaitQueryRetryLimit

	submitAndWaitPollInterval = pollInterval
	submitAndWaitQueryRetryBackoff = retryBackoff
	submitAndWaitQueryRetryBackoffMax = retryBackoffMax
	submitAndWaitQueryRetryLimit = retryLimit

	t.Cleanup(func() {
		submitAndWaitPollInterval = prevPollInterval
		submitAndWaitQueryRetryBackoff = prevRetryBackoff
		submitAndWaitQueryRetryBackoffMax = prevRetryBackoffMax
		submitAndWaitQueryRetryLimit = prevRetryLimit
	})
}

func TestSubmitAndWaitSubmitFailure(t *testing.T) {
	engine := &Engine{}

	txID, status, err := engine.SubmitAndWait(context.Background(), nil)
	if err == nil {
		t.Fatal("SubmitAndWait() error = nil, want error")
	}
	if txID != "" {
		t.Fatalf("SubmitAndWait() txID = %q, want empty", txID)
	}
	if status != nil {
		t.Fatalf("SubmitAndWait() status = %#v, want nil", status)
	}
	if errors.Is(err, ErrTransactionFailed) {
		t.Fatal("SubmitAndWait() error wrapped ErrTransactionFailed, want original submit error")
	}
	if errors.Is(err, ErrTransactionDisappeared) {
		t.Fatal("SubmitAndWait() error wrapped ErrTransactionDisappeared, want original submit error")
	}
}

func TestQueryTransactionNotFound(t *testing.T) {
	store := &submitAndWaitStore{
		txResults: []waitQueryResult{
			{tx: nil, err: nil},
		},
	}
	engine := &Engine{store: store}

	status, err := engine.Query(context.Background(), "missing-tx")
	if !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("Query() error = %v, want ErrTransactionNotFound", err)
	}
	if status != nil {
		t.Fatalf("Query() status = %#v, want nil", status)
	}
}

func TestWaitForTransactionTemporaryQueryErrorsThenSuccess(t *testing.T) {
	setSubmitAndWaitTestIntervals(t, 5*time.Millisecond, 5*time.Millisecond, 20*time.Millisecond, 3)

	store := &submitAndWaitStore{
		txResults: []waitQueryResult{
			{err: errors.New("temporary query error")},
			{err: errors.New("temporary query error")},
			{tx: &Transaction{ID: "tx-1", Status: TxStatusPending, CreatedAt: time.Now()}},
			{tx: &Transaction{ID: "tx-1", Status: TxStatusSucceeded, CreatedAt: time.Now()}},
		},
		steps: []*Step{
			{Index: 0, Name: "step-1", Status: StepStatusSucceeded},
		},
	}
	engine := &Engine{store: store}

	status, err := engine.waitForTransaction(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("waitForTransaction() error = %v, want nil", err)
	}
	if status == nil || status.Status != string(TxStatusSucceeded) {
		t.Fatalf("waitForTransaction() status = %#v, want succeeded", status)
	}
}

func TestWaitForTransactionPersistentQueryErrors(t *testing.T) {
	setSubmitAndWaitTestIntervals(t, 5*time.Millisecond, 5*time.Millisecond, 20*time.Millisecond, 3)

	store := &submitAndWaitStore{
		txResults: []waitQueryResult{
			{tx: &Transaction{ID: "tx-2", Status: TxStatusPending, CreatedAt: time.Now()}},
			{err: errors.New("temporary query error")},
			{err: errors.New("temporary query error")},
			{err: errors.New("temporary query error")},
		},
	}
	engine := &Engine{store: store}

	status, err := engine.waitForTransaction(context.Background(), "tx-2")
	if err == nil {
		t.Fatal("waitForTransaction() error = nil, want infrastructure error")
	}
	if errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("waitForTransaction() error = %v, should not wrap ErrTransactionFailed", err)
	}
	if errors.Is(err, ErrTransactionDisappeared) {
		t.Fatalf("waitForTransaction() error = %v, should not wrap ErrTransactionDisappeared", err)
	}
	if status == nil || status.Status != string(TxStatusPending) {
		t.Fatalf("waitForTransaction() status = %#v, want last known pending status", status)
	}
}

func TestWaitForTransactionDisappearedTransaction(t *testing.T) {
	setSubmitAndWaitTestIntervals(t, 5*time.Millisecond, 5*time.Millisecond, 20*time.Millisecond, 3)

	store := &submitAndWaitStore{
		txResults: []waitQueryResult{
			{tx: &Transaction{ID: "tx-3", Status: TxStatusPending, CreatedAt: time.Now()}},
			{tx: nil, err: nil},
		},
	}
	engine := &Engine{store: store}

	status, err := engine.waitForTransaction(context.Background(), "tx-3")
	if !errors.Is(err, ErrTransactionDisappeared) {
		t.Fatalf("waitForTransaction() error = %v, want ErrTransactionDisappeared", err)
	}
	if status == nil || status.Status != string(TxStatusPending) {
		t.Fatalf("waitForTransaction() status = %#v, want last known pending status", status)
	}
}

func TestWaitForTransactionInitialNotFoundReturnsDisappeared(t *testing.T) {
	setSubmitAndWaitTestIntervals(t, 5*time.Millisecond, 5*time.Millisecond, 20*time.Millisecond, 3)

	store := &submitAndWaitStore{
		txResults: []waitQueryResult{
			{tx: nil, err: nil},
			{tx: &Transaction{ID: "tx-4", Status: TxStatusPending, CreatedAt: time.Now()}},
		},
	}
	engine := &Engine{store: store}

	status, err := engine.waitForTransaction(context.Background(), "tx-4")
	if !errors.Is(err, ErrTransactionDisappeared) {
		t.Fatalf("waitForTransaction() error = %v, want ErrTransactionDisappeared", err)
	}
	if status != nil {
		t.Fatalf("waitForTransaction() status = %#v, want nil", status)
	}
	if store.getTxCall != 1 {
		t.Fatalf("waitForTransaction() queried %d times, want 1", store.getTxCall)
	}
}

func TestSubmitAndWaitWithoutStartReturnsContextError(t *testing.T) {
	db := setupTestDB(t)
	db.Close()

	setSubmitAndWaitTestIntervals(t, 10*time.Millisecond, 10*time.Millisecond, 20*time.Millisecond, 3)

	engine, err := NewEngine(&Config{
		DSN:         getTestDSN(),
		WorkerCount: 1,
		HTTPTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Skipf("Failed to create engine: %v", err)
	}
	defer engine.Stop()

	def, err := NewSaga("test-no-executor").
		WithTimeout(1).
		AddStep(Step{
			Name:             "Step 1",
			Type:             StepTypeSync,
			ActionMethod:     "POST",
			ActionURL:        "http://example.invalid/step1",
			CompensateMethod: "POST",
			CompensateURL:    "http://example.invalid/step1/rollback",
		}).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	txID, status, err := engine.SubmitAndWait(ctx, def)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SubmitAndWait() error = %v, want context deadline exceeded", err)
	}
	if txID == "" {
		t.Fatal("SubmitAndWait() txID = empty, want persisted transaction id")
	}
	if status == nil || status.Status != string(TxStatusPending) {
		t.Fatalf("SubmitAndWait() status = %#v, want pending", status)
	}
}

func TestSubmitAndWaitTimeoutWaitsForCompensation(t *testing.T) {
	db := setupTestDB(t)
	db.Close()

	setSubmitAndWaitTestIntervals(t, 10*time.Millisecond, 10*time.Millisecond, 20*time.Millisecond, 3)

	var compensateCalled atomic.Int32
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/step1/action":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case "/step1/rollback":
			compensateCalled.Add(1)
			w.WriteHeader(http.StatusOK)
		case "/step2/action":
			time.Sleep(3 * time.Second)
			w.WriteHeader(http.StatusOK)
		case "/step2/rollback":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer svc.Close()

	engine := newTestEngine(t, "submit-wait-timeout")
	cleanTables(t, engine.DB())
	engine.coordinator.config.TimeoutScanInterval = 100 * time.Millisecond
	ctx := context.Background()
	if err := engine.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer engine.Stop()

	def, err := NewSaga("submit-wait-timeout").
		WithTimeout(1).
		AddStep(Step{
			Name:             "Step 1",
			Type:             StepTypeSync,
			ActionMethod:     "POST",
			ActionURL:        svc.URL + "/step1/action",
			CompensateMethod: "POST",
			CompensateURL:    svc.URL + "/step1/rollback",
		}).
		AddStep(Step{
			Name:             "Step 2",
			Type:             StepTypeSync,
			ActionMethod:     "POST",
			ActionURL:        svc.URL + "/step2/action",
			CompensateMethod: "POST",
			CompensateURL:    svc.URL + "/step2/rollback",
			MaxRetry:         1,
		}).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	txID, status, err := engine.SubmitAndWait(waitCtx, def)
	if !errors.Is(err, ErrTransactionFailed) {
		t.Fatalf("SubmitAndWait() error = %v, want ErrTransactionFailed", err)
	}
	if txID == "" {
		t.Fatal("SubmitAndWait() txID = empty, want persisted transaction id")
	}
	if status == nil || status.Status != string(TxStatusFailed) {
		t.Fatalf("SubmitAndWait() status = %#v, want failed", status)
	}
	if compensateCalled.Load() < 1 {
		t.Fatalf("SubmitAndWait() compensation count = %d, want >= 1", compensateCalled.Load())
	}
}
