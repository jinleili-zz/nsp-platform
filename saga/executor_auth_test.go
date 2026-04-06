package saga

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jinleili-zz/nsp-platform/auth"
)

func newAuthTestExecutor(store Store, credStore auth.CredentialStore) *Executor {
	return NewExecutor(store, &ExecutorConfig{HTTPTimeout: time.Second}, credStore)
}

func TestExecuteStepSignsActionRequestWhenAuthAKConfigured(t *testing.T) {
	var capturedAuth string
	var capturedTimestamp string
	var capturedNonce string
	var capturedSignedHeaders string

	store := newRegressionStore()
	executor := newAuthTestExecutor(store, auth.NewMemoryStore([]*auth.Credential{{
		AccessKey: "test-ak",
		SecretKey: "test-sk",
		Enabled:   true,
	}}))
	executor.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedAuth = r.Header.Get(auth.HeaderAuthorization)
		capturedTimestamp = r.Header.Get(auth.HeaderTimestamp)
		capturedNonce = r.Header.Get(auth.HeaderNonce)
		capturedSignedHeaders = r.Header.Get(auth.HeaderSignedHeaders)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Header:     make(http.Header),
		}, nil
	})}

	tx := &Transaction{ID: "tx-auth-action", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-auth-action",
		TransactionID:    tx.ID,
		Name:             "signed-action",
		Type:             StepTypeSync,
		Status:           StepStatusPending,
		ActionMethod:     http.MethodPost,
		ActionURL:        "http://unit.test/action",
		ActionPayload:    map[string]any{"hello": "world"},
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://unit.test/compensate",
		MaxRetry:         3,
		AuthAK:           "test-ak",
	}
	store.put(tx, step)

	if err := executor.ExecuteStep(context.Background(), tx, step, []*Step{step}); err != nil {
		t.Fatalf("ExecuteStep() error = %v", err)
	}

	if capturedAuth == "" {
		t.Fatal("expected Authorization header to be set")
	}
	if capturedTimestamp == "" {
		t.Fatal("expected X-NSP-Timestamp header to be set")
	}
	if capturedNonce == "" {
		t.Fatal("expected X-NSP-Nonce header to be set")
	}
	if capturedSignedHeaders == "" {
		t.Fatal("expected X-NSP-SignedHeaders header to be set")
	}
}

func TestExecuteStepLeavesRequestUnsignedWhenAuthAKEmpty(t *testing.T) {
	var capturedAuth string
	var capturedTimestamp string
	var capturedNonce string
	var capturedSignedHeaders string

	store := newRegressionStore()
	executor := newAuthTestExecutor(store, auth.NewMemoryStore([]*auth.Credential{{
		AccessKey: "unused-ak",
		SecretKey: "unused-sk",
		Enabled:   true,
	}}))
	executor.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedAuth = r.Header.Get(auth.HeaderAuthorization)
		capturedTimestamp = r.Header.Get(auth.HeaderTimestamp)
		capturedNonce = r.Header.Get(auth.HeaderNonce)
		capturedSignedHeaders = r.Header.Get(auth.HeaderSignedHeaders)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Header:     make(http.Header),
		}, nil
	})}

	tx := &Transaction{ID: "tx-unsigned", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-unsigned",
		TransactionID:    tx.ID,
		Name:             "unsigned-action",
		Type:             StepTypeSync,
		Status:           StepStatusPending,
		ActionMethod:     http.MethodPost,
		ActionURL:        "http://unit.test/action",
		ActionPayload:    map[string]any{"hello": "world"},
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://unit.test/compensate",
		MaxRetry:         3,
	}
	store.put(tx, step)

	if err := executor.ExecuteStep(context.Background(), tx, step, []*Step{step}); err != nil {
		t.Fatalf("ExecuteStep() error = %v", err)
	}

	if capturedAuth != "" || capturedTimestamp != "" || capturedNonce != "" || capturedSignedHeaders != "" {
		t.Fatalf("expected unsigned request, got auth=%q timestamp=%q nonce=%q signedHeaders=%q", capturedAuth, capturedTimestamp, capturedNonce, capturedSignedHeaders)
	}
}

func TestSignRequestIfNeededReturnsSigningErrorWhenCredentialMissing(t *testing.T) {
	executor := newAuthTestExecutor(newRegressionStore(), auth.NewMemoryStore(nil))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}

	err = executor.signRequestIfNeeded(context.Background(), &Step{AuthAK: "missing-ak"}, req)
	if err == nil {
		t.Fatal("expected signing error, got nil")
	}
	if !IsSigningError(err) {
		t.Fatalf("expected signing error, got %v", err)
	}
}

func TestExecuteStepSigningFailureIsFatalWithoutRetryIncrement(t *testing.T) {
	store := newRegressionStore()
	executor := newAuthTestExecutor(store, auth.NewMemoryStore(nil))

	tx := &Transaction{ID: "tx-sign-fatal", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-sign-fatal",
		TransactionID:    tx.ID,
		Name:             "fatal-sign",
		Type:             StepTypeSync,
		Status:           StepStatusPending,
		ActionMethod:     http.MethodPost,
		ActionURL:        "http://example.com/action",
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://example.com/compensate",
		MaxRetry:         3,
		AuthAK:           "missing-ak",
	}
	store.put(tx, step)

	err := executor.ExecuteStep(context.Background(), tx, step, []*Step{step})
	if !errors.Is(err, ErrStepFatal) {
		t.Fatalf("expected ErrStepFatal, got %v", err)
	}
	if step.RetryCount != 0 {
		t.Fatalf("expected in-memory retry count to remain 0, got %d", step.RetryCount)
	}

	storedStep, _ := store.GetStep(context.Background(), step.ID)
	if storedStep.RetryCount != 0 {
		t.Fatalf("expected stored retry count to remain 0, got %d", storedStep.RetryCount)
	}
	if storedStep.Status != StepStatusFailed {
		t.Fatalf("expected stored step status failed, got %q", storedStep.Status)
	}
}

func TestCompensateAndPollRequestsAreSignedWhenAuthAKConfigured(t *testing.T) {
	headersByPath := make(map[string]http.Header)

	store := newRegressionStore()
	executor := newAuthTestExecutor(store, auth.NewMemoryStore([]*auth.Credential{{
		AccessKey: "test-ak",
		SecretKey: "test-sk",
		Enabled:   true,
	}}))
	executor.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		headersByPath[r.URL.Path] = r.Header.Clone()
		switch r.URL.Path {
		case "/compensate":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				Header:     make(http.Header),
			}, nil
		case "/poll":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":"success"}`)),
				Header:     make(http.Header),
			}, nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("not found")),
				Header:     make(http.Header),
			}, nil
		}
	})}

	tx := &Transaction{ID: "tx-auth-other", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-auth-other",
		TransactionID:    tx.ID,
		Name:             "signed-other",
		Type:             StepTypeAsync,
		Status:           StepStatusSucceeded,
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://unit.test/compensate",
		PollURL:          "http://unit.test/poll",
		PollMethod:       http.MethodGet,
		PollSuccessPath:  "$.status",
		PollSuccessValue: "success",
		PollFailurePath:  "$.status",
		PollFailureValue: "failed",
		MaxRetry:         1,
		AuthAK:           "test-ak",
	}
	store.put(tx, step)

	if err := executor.CompensateStep(context.Background(), tx, step, []*Step{step}); err != nil {
		t.Fatalf("CompensateStep() error = %v", err)
	}

	if _, err := executor.Poll(context.Background(), tx, step, []*Step{step}); err != nil {
		t.Fatalf("Poll() error = %v", err)
	}

	for _, path := range []string{"/compensate", "/poll"} {
		headers := headersByPath[path]
		if headers.Get(auth.HeaderAuthorization) == "" {
			t.Fatalf("expected Authorization header on %s request", path)
		}
		if headers.Get(auth.HeaderTimestamp) == "" {
			t.Fatalf("expected X-NSP-Timestamp header on %s request", path)
		}
		if headers.Get(auth.HeaderNonce) == "" {
			t.Fatalf("expected X-NSP-Nonce header on %s request", path)
		}
		if headers.Get(auth.HeaderSignedHeaders) == "" {
			t.Fatalf("expected X-NSP-SignedHeaders header on %s request", path)
		}
	}
}

func TestPostgresStorePersistsAuthAKRoundTrip(t *testing.T) {
	db := setupTestDB(t)
	if _, err := db.Exec(`ALTER TABLE saga_steps ADD COLUMN IF NOT EXISTS auth_ak TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("failed to ensure auth_ak column exists: %v", err)
	}

	store := NewPostgresStore(db)
	ctx := context.Background()

	tx := &Transaction{
		ID:          "tx-auth-roundtrip",
		Status:      TxStatusPending,
		Payload:     map[string]any{},
		CurrentStep: 0,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	step := &Step{
		ID:               "step-auth-roundtrip",
		TransactionID:    tx.ID,
		Index:            0,
		Name:             "roundtrip",
		Type:             StepTypeSync,
		Status:           StepStatusPending,
		ActionMethod:     http.MethodPost,
		ActionURL:        "http://example.com/action",
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://example.com/compensate",
		MaxRetry:         1,
		AuthAK:           "persisted-ak",
	}

	if err := store.CreateTransactionWithSteps(ctx, tx, []*Step{step}); err != nil {
		t.Fatalf("CreateTransactionWithSteps() error = %v", err)
	}

	gotStep, err := store.GetStep(ctx, step.ID)
	if err != nil {
		t.Fatalf("GetStep() error = %v", err)
	}
	if gotStep == nil || gotStep.AuthAK != "persisted-ak" {
		t.Fatalf("expected GetStep AuthAK persisted-ak, got %+v", gotStep)
	}

	gotSteps, err := store.GetSteps(ctx, tx.ID)
	if err != nil {
		t.Fatalf("GetSteps() error = %v", err)
	}
	if len(gotSteps) != 1 || gotSteps[0].AuthAK != "persisted-ak" {
		t.Fatalf("expected GetSteps AuthAK persisted-ak, got %+v", gotSteps)
	}
}

func TestPollerTreatsSigningFailureAsTerminal(t *testing.T) {
	store := newRegressionStore()
	executor := newAuthTestExecutor(store, auth.NewMemoryStore(nil))
	poller := NewPoller(store, executor, &PollerConfig{})

	tx := &Transaction{ID: "tx-poll-sign", Status: TxStatusRunning, Payload: map[string]any{}}
	step := &Step{
		ID:               "step-poll-sign",
		TransactionID:    tx.ID,
		Index:            0,
		Name:             "poll-sign-fail",
		Type:             StepTypeAsync,
		Status:           StepStatusPolling,
		ActionMethod:     http.MethodPost,
		ActionURL:        "http://example.com/action",
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://example.com/compensate",
		PollURL:          "http://example.com/poll",
		PollMethod:       http.MethodGet,
		PollMaxTimes:     3,
		PollIntervalSec:  1,
		PollSuccessPath:  "$.status",
		PollSuccessValue: "success",
		PollFailurePath:  "$.status",
		PollFailureValue: "failed",
		MaxRetry:         1,
		AuthAK:           "missing-ak",
	}
	task := &PollTask{StepID: step.ID, TransactionID: tx.ID, NextPollAt: time.Now()}
	store.put(tx, step)
	if err := store.CreatePollTask(context.Background(), task); err != nil {
		t.Fatalf("CreatePollTask() error = %v", err)
	}

	poller.processPollTask(context.Background(), task)

	updatedStep, _ := store.GetStep(context.Background(), step.ID)
	if updatedStep.Status != StepStatusFailed {
		t.Fatalf("expected step status failed, got %q", updatedStep.Status)
	}
	if !strings.Contains(updatedStep.LastError, ErrSigningFailed.Error()) {
		t.Fatalf("expected signing error in last error, got %q", updatedStep.LastError)
	}

	store.mu.Lock()
	_, exists := store.pollTasks[step.ID]
	store.mu.Unlock()
	if exists {
		t.Fatal("expected poll task to be deleted after signing failure")
	}
}

type submitValidationStore struct {
	mockStore
	createCalls int
}

func (s *submitValidationStore) CreateTransactionWithSteps(ctx context.Context, tx *Transaction, steps []*Step) error {
	s.createCalls++
	return nil
}

func TestEngineSubmitRejectsUnknownAuthAK(t *testing.T) {
	store := &submitValidationStore{}
	engine := &Engine{
		store:  store,
		config: &Config{CredentialStore: auth.NewMemoryStore(nil)},
		coordinator: NewCoordinator(store, nil, nil, &CoordinatorConfig{
			WorkerCount: 1,
		}),
	}

	def, err := NewSaga("submit-auth-validation").
		AddStep(Step{
			Name:             "signed-step",
			Type:             StepTypeSync,
			ActionMethod:     http.MethodPost,
			ActionURL:        "http://example.com/action",
			CompensateMethod: http.MethodPost,
			CompensateURL:    "http://example.com/compensate",
			AuthAK:           "missing-ak",
		}).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	txID, err := engine.Submit(context.Background(), def)
	if err == nil {
		t.Fatalf("expected Submit() error, got nil with txID=%q", txID)
	}
	if store.createCalls != 0 {
		t.Fatalf("expected no transaction creation on validation failure, got %d", store.createCalls)
	}
}
