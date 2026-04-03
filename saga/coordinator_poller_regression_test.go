package saga

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func newExecutorWithResponder(store Store, responder roundTripFunc) *Executor {
	executor := NewExecutor(store, &ExecutorConfig{HTTPTimeout: time.Second})
	executor.client = &http.Client{Transport: responder}
	return executor
}

type regressionStore struct {
	mu        sync.Mutex
	txs       map[string]*Transaction
	steps     map[string]*Step
	stepOrder map[string][]*Step
	pollTasks map[string]*PollTask
}

func newRegressionStore() *regressionStore {
	return &regressionStore{
		txs:       make(map[string]*Transaction),
		steps:     make(map[string]*Step),
		stepOrder: make(map[string][]*Step),
		pollTasks: make(map[string]*PollTask),
	}
}

func cloneTransaction(tx *Transaction) *Transaction {
	if tx == nil {
		return nil
	}
	cp := *tx
	return &cp
}

func cloneStep(step *Step) *Step {
	if step == nil {
		return nil
	}
	cp := *step
	return &cp
}

func (s *regressionStore) put(tx *Transaction, steps ...*Step) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.txs[tx.ID] = cloneTransaction(tx)
	ordered := make([]*Step, 0, len(steps))
	for _, step := range steps {
		cp := cloneStep(step)
		s.steps[cp.ID] = cp
		ordered = append(ordered, cp)
	}
	s.stepOrder[tx.ID] = ordered
}

func (s *regressionStore) CreateTransaction(ctx context.Context, tx *Transaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.txs[tx.ID] = cloneTransaction(tx)
	return nil
}

func (s *regressionStore) CreateTransactionWithSteps(ctx context.Context, tx *Transaction, steps []*Step) error {
	s.put(tx, steps...)
	return nil
}

func (s *regressionStore) GetTransaction(ctx context.Context, id string) (*Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneTransaction(s.txs[id]), nil
}

func (s *regressionStore) UpdateTransactionStatus(ctx context.Context, id string, status TxStatus, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := s.txs[id]
	if tx == nil {
		return nil
	}
	tx.Status = status
	tx.LastError = lastError
	tx.UpdatedAt = time.Now()
	if status == TxStatusFailed || status == TxStatusSucceeded {
		now := time.Now()
		tx.FinishedAt = &now
	}
	return nil
}

func (s *regressionStore) UpdateTransactionStep(ctx context.Context, id string, currentStep int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := s.txs[id]
	if tx == nil {
		return nil
	}
	tx.CurrentStep = currentStep
	return nil
}

func (s *regressionStore) CreateSteps(ctx context.Context, steps []*Step) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, step := range steps {
		cp := cloneStep(step)
		s.steps[cp.ID] = cp
		s.stepOrder[cp.TransactionID] = append(s.stepOrder[cp.TransactionID], cp)
	}
	return nil
}

func (s *regressionStore) GetSteps(ctx context.Context, txID string) ([]*Step, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	steps := s.stepOrder[txID]
	result := make([]*Step, 0, len(steps))
	for _, step := range steps {
		result = append(result, cloneStep(step))
	}
	return result, nil
}

func (s *regressionStore) GetStep(ctx context.Context, stepID string) (*Step, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStep(s.steps[stepID]), nil
}

func (s *regressionStore) UpdateStepStatus(ctx context.Context, stepID string, status StepStatus, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	step := s.steps[stepID]
	if step == nil {
		return nil
	}
	step.Status = status
	step.LastError = lastError
	return nil
}

func (s *regressionStore) UpdateStepResponse(ctx context.Context, stepID string, response map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	step := s.steps[stepID]
	if step == nil {
		return nil
	}
	step.ActionResponse = response
	return nil
}

func (s *regressionStore) IncrementStepRetry(ctx context.Context, stepID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	step := s.steps[stepID]
	if step == nil {
		return nil
	}
	step.RetryCount++
	return nil
}

func (s *regressionStore) IncrementStepPollCount(ctx context.Context, stepID string, nextPollAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	step := s.steps[stepID]
	if step == nil {
		return nil
	}
	step.PollCount++
	step.NextPollAt = &nextPollAt
	if task := s.pollTasks[stepID]; task != nil {
		task.NextPollAt = nextPollAt
	}
	return nil
}

func (s *regressionStore) CreatePollTask(ctx context.Context, task *PollTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *task
	s.pollTasks[task.StepID] = &cp
	return nil
}

func (s *regressionStore) DeletePollTask(ctx context.Context, stepID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pollTasks, stepID)
	return nil
}

func (s *regressionStore) ListRecoverableTransactions(ctx context.Context, instanceID string, batchSize int, leaseDuration time.Duration) ([]*Transaction, error) {
	return nil, nil
}

func (s *regressionStore) ListTimedOutTransactions(ctx context.Context, instanceID string, leaseDuration time.Duration) ([]*Transaction, error) {
	return nil, nil
}

func (s *regressionStore) AcquirePollTasks(ctx context.Context, instanceID string, batchSize int) ([]*PollTask, error) {
	return nil, nil
}

func (s *regressionStore) ReleasePollTask(ctx context.Context, stepID string) error {
	return nil
}

func (s *regressionStore) ClaimTransaction(ctx context.Context, txID string, instanceID string, leaseDuration time.Duration) (bool, error) {
	return true, nil
}

func (s *regressionStore) ReleaseTransaction(ctx context.Context, txID string, instanceID string) error {
	return nil
}

func (s *regressionStore) UpdateTransactionStatusCAS(ctx context.Context, txID string, expectedStatus TxStatus, newStatus TxStatus, lastError string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := s.txs[txID]
	if tx == nil || tx.Status != expectedStatus {
		return false, nil
	}
	tx.Status = newStatus
	tx.LastError = lastError
	return true, nil
}

func TestCoordinatorExecuteSyncStepContinuesIntoCompensation(t *testing.T) {
	store := newRegressionStore()
	tx := &Transaction{ID: "tx-sync", Status: TxStatusRunning}

	step := &Step{
		ID:            "step-sync",
		TransactionID: tx.ID,
		Index:         0,
		Name:          "sync-step",
		Type:          StepTypeSync,
		Status:        StepStatusPending,
		ActionMethod:  http.MethodPost,
		ActionURL:     "http://unit.test/sync",
		MaxRetry:      1,
	}
	store.put(tx, step)

	coordinator := NewCoordinator(store, newExecutorWithResponder(store, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader("boom")),
			Header:     make(http.Header),
		}, nil
	}), nil, &CoordinatorConfig{})
	if !coordinator.executeSyncStep(context.Background(), tx, step, []*Step{step}) {
		t.Fatal("expected coordinator to continue driving after switching to compensating")
	}

	updatedTx, _ := store.GetTransaction(context.Background(), tx.ID)
	if updatedTx.Status != TxStatusCompensating {
		t.Fatalf("expected transaction status %q, got %q", TxStatusCompensating, updatedTx.Status)
	}
}

func TestCoordinatorExecuteAsyncStepContinuesIntoCompensation(t *testing.T) {
	store := newRegressionStore()
	tx := &Transaction{ID: "tx-async", Status: TxStatusRunning}

	step := &Step{
		ID:              "step-async",
		TransactionID:   tx.ID,
		Index:           0,
		Name:            "async-step",
		Type:            StepTypeAsync,
		Status:          StepStatusPending,
		ActionMethod:    http.MethodPost,
		ActionURL:       "http://unit.test/async",
		PollIntervalSec: 1,
		PollMaxTimes:    3,
		MaxRetry:        1,
	}
	store.put(tx, step)

	coordinator := NewCoordinator(store, newExecutorWithResponder(store, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader("boom")),
			Header:     make(http.Header),
		}, nil
	}), nil, &CoordinatorConfig{})
	if !coordinator.executeAsyncStep(context.Background(), tx, step, []*Step{step}) {
		t.Fatal("expected coordinator to continue driving after async step switched transaction to compensating")
	}

	updatedTx, _ := store.GetTransaction(context.Background(), tx.ID)
	if updatedTx.Status != TxStatusCompensating {
		t.Fatalf("expected transaction status %q, got %q", TxStatusCompensating, updatedTx.Status)
	}
}

func TestCoordinatorWaitForAsyncStepRechecksStateAfterRegister(t *testing.T) {
	store := newRegressionStore()
	tx := &Transaction{ID: "tx-poll", Status: TxStatusRunning}
	step := &Step{
		ID:            "step-poll",
		TransactionID: tx.ID,
		Index:         0,
		Name:          "poll-step",
		Type:          StepTypeAsync,
		Status:        StepStatusSucceeded,
	}
	store.put(tx, step)

	poller := NewPoller(store, nil, &PollerConfig{})
	coordinator := NewCoordinator(store, nil, poller, &CoordinatorConfig{AsyncStepTimeout: time.Second})

	start := time.Now()
	if !coordinator.waitForAsyncStep(context.Background(), tx, &Step{ID: step.ID, Index: 0, Status: StepStatusPolling}, []*Step{step}) {
		t.Fatal("expected waitForAsyncStep to keep driving after observing completed step state")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("expected waitForAsyncStep to return immediately after rechecking state, took %v", elapsed)
	}

	updatedTx, _ := store.GetTransaction(context.Background(), tx.ID)
	if updatedTx.CurrentStep != 1 {
		t.Fatalf("expected current step to advance to 1, got %d", updatedTx.CurrentStep)
	}
}

func TestCoordinatorExecuteCompensationRetriesStepLeftCompensating(t *testing.T) {
	store := newRegressionStore()
	tx := &Transaction{ID: "tx-compensate", Status: TxStatusCompensating}

	var compensateCalls atomic.Int32

	step1 := &Step{
		ID:               "step-1",
		TransactionID:    tx.ID,
		Index:            0,
		Name:             "already-compensating",
		Type:             StepTypeSync,
		Status:           StepStatusCompensating,
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://unit.test/compensate",
		MaxRetry:         1,
	}
	step2 := &Step{
		ID:            "step-2",
		TransactionID: tx.ID,
		Index:         1,
		Name:          "pending-step",
		Type:          StepTypeSync,
		Status:        StepStatusPending,
	}
	store.put(tx, step1, step2)

	coordinator := NewCoordinator(store, newExecutorWithResponder(store, func(req *http.Request) (*http.Response, error) {
		compensateCalls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	}), nil, &CoordinatorConfig{})
	coordinator.executeCompensation(context.Background(), tx, []*Step{step1, step2})

	updatedStep, _ := store.GetStep(context.Background(), step1.ID)
	if updatedStep.Status != StepStatusCompensated {
		t.Fatalf("expected compensating step to be retried and compensated, got %q", updatedStep.Status)
	}
	if compensateCalls.Load() != 1 {
		t.Fatalf("expected exactly one compensation attempt, got %d", compensateCalls.Load())
	}

	skippedStep, _ := store.GetStep(context.Background(), step2.ID)
	if skippedStep.Status != StepStatusSkipped {
		t.Fatalf("expected pending step to be skipped, got %q", skippedStep.Status)
	}

	updatedTx, _ := store.GetTransaction(context.Background(), tx.ID)
	if updatedTx.Status != TxStatusFailed {
		t.Fatalf("expected transaction status %q after compensation, got %q", TxStatusFailed, updatedTx.Status)
	}
}

func TestPollerUnregisterNotifyLeavesChannelOpen(t *testing.T) {
	poller := NewPoller(newRegressionStore(), nil, &PollerConfig{})
	ch := poller.RegisterNotify("tx-notify")
	poller.UnregisterNotify("tx-notify")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("expected late send to be harmless after unregister, got panic: %v", r)
		}
	}()

	select {
	case ch <- PollResultSuccess:
	default:
	}

	poller.notify("tx-notify", PollResultFailure)
}

func TestPostgresStoreIncrementStepPollCountReschedulesPollTask(t *testing.T) {
	db := setupTestDB(t)
	store := NewPostgresStore(db)
	ctx := context.Background()

	tx := &Transaction{
		ID:          "tx-store-poll",
		Status:      TxStatusRunning,
		Payload:     map[string]any{},
		CurrentStep: 0,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	step := &Step{
		ID:               "step-store-poll",
		TransactionID:    tx.ID,
		Index:            0,
		Name:             "async-step",
		Type:             StepTypeAsync,
		Status:           StepStatusPolling,
		ActionMethod:     http.MethodPost,
		ActionURL:        "http://example.com/action",
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://example.com/compensate",
		PollURL:          "http://example.com/poll",
		PollMethod:       http.MethodGet,
		PollIntervalSec:  5,
		PollMaxTimes:     10,
		PollSuccessPath:  "$.status",
		PollSuccessValue: "success",
		PollFailurePath:  "$.status",
		PollFailureValue: "failed",
		MaxRetry:         1,
	}

	if err := store.CreateTransactionWithSteps(ctx, tx, []*Step{step}); err != nil {
		t.Fatalf("failed to create transaction with step: %v", err)
	}
	if err := store.CreatePollTask(ctx, &PollTask{
		StepID:        step.ID,
		TransactionID: tx.ID,
		NextPollAt:    time.Now(),
	}); err != nil {
		t.Fatalf("failed to create poll task: %v", err)
	}

	nextPollAt := time.Now().Add(2 * time.Minute).UTC().Round(time.Microsecond)
	if err := store.IncrementStepPollCount(ctx, step.ID, nextPollAt); err != nil {
		t.Fatalf("failed to reschedule poll task: %v", err)
	}

	var stepPollCount int
	var storedStepNextPollAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT poll_count, next_poll_at FROM saga_steps WHERE id = $1`, step.ID).
		Scan(&stepPollCount, &storedStepNextPollAt); err != nil {
		t.Fatalf("failed to query saga_steps: %v", err)
	}
	if stepPollCount != 1 {
		t.Fatalf("expected poll_count to be 1, got %d", stepPollCount)
	}
	if !storedStepNextPollAt.Equal(nextPollAt) {
		t.Fatalf("expected saga_steps.next_poll_at %v, got %v", nextPollAt, storedStepNextPollAt)
	}

	var storedTaskNextPollAt time.Time
	if err := db.QueryRowContext(ctx, `SELECT next_poll_at FROM saga_poll_tasks WHERE step_id = $1`, step.ID).
		Scan(&storedTaskNextPollAt); err != nil {
		t.Fatalf("failed to query saga_poll_tasks: %v", err)
	}
	if !storedTaskNextPollAt.Equal(nextPollAt) {
		t.Fatalf("expected saga_poll_tasks.next_poll_at %v, got %v", nextPollAt, storedTaskNextPollAt)
	}
}
