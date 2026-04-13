package saga

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestIsStepHTTPSuccess(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       []byte
		wantMap    bool
		wantOK     bool
		wantErr    bool
	}{
		{
			name:       "http 200 with string code zero",
			statusCode: http.StatusOK,
			body:       []byte(`{"code":"0","data":{"id":"123"}}`),
			wantMap:    true,
			wantOK:     true,
		},
		{
			name:       "http 200 with numeric code zero",
			statusCode: http.StatusOK,
			body:       []byte(`{"code":0,"data":{}}`),
			wantMap:    true,
			wantOK:     true,
		},
		{
			name:       "http 200 with business failure code",
			statusCode: http.StatusOK,
			body:       []byte(`{"code":"1","message":"not found"}`),
			wantMap:    true,
			wantOK:     false,
		},
		{
			name:       "http 200 with empty body",
			statusCode: http.StatusOK,
			body:       nil,
			wantMap:    false,
			wantOK:     false,
		},
		{
			name:       "http 200 with non json body",
			statusCode: http.StatusOK,
			body:       []byte(`not json`),
			wantMap:    false,
			wantOK:     false,
			wantErr:    true,
		},
		{
			name:       "http 200 with missing code field",
			statusCode: http.StatusOK,
			body:       []byte(`{"data":"ok"}`),
			wantMap:    true,
			wantOK:     false,
		},
		{
			name:       "http 500 with code zero",
			statusCode: http.StatusInternalServerError,
			body:       []byte(`{"code":"0"}`),
			wantMap:    false,
			wantOK:     false,
		},
		{
			name:       "http 404",
			statusCode: http.StatusNotFound,
			body:       []byte(`{"code":"1","message":"not found"}`),
			wantMap:    false,
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := IsStepHTTPSuccess(tt.statusCode, tt.body)
			if (err != nil) != tt.wantErr {
				t.Fatalf("IsStepHTTPSuccess() error = %v, wantErr %v", err, tt.wantErr)
			}
			if ok != tt.wantOK {
				t.Fatalf("IsStepHTTPSuccess() ok = %v, want %v", ok, tt.wantOK)
			}
			if (got != nil) != tt.wantMap {
				t.Fatalf("IsStepHTTPSuccess() map present = %v, want %v", got != nil, tt.wantMap)
			}
		})
	}
}

func TestExecuteStepTreatsBusinessFailureAsFailure(t *testing.T) {
	store := newRegressionStore()
	executor := newExecutorWithResponder(store, func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusOK, `{"code":"1","message":"failed"}`), nil
	})

	tx := &Transaction{ID: "tx-sync-business-fail", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-sync-business-fail",
		TransactionID:    tx.ID,
		Name:             "sync-step",
		Type:             StepTypeSync,
		Status:           StepStatusPending,
		ActionMethod:     http.MethodPost,
		ActionURL:        "http://unit.test/action",
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://unit.test/compensate",
		MaxRetry:         1,
	}
	store.put(tx, step)

	err := executor.ExecuteStep(context.Background(), tx, step, []*Step{step})
	if !errors.Is(err, ErrStepFatal) {
		t.Fatalf("expected ErrStepFatal, got %v", err)
	}
	if store.steps[step.ID].Status != StepStatusFailed {
		t.Fatalf("expected stored step status failed, got %q", store.steps[step.ID].Status)
	}
}

func TestExecuteAsyncStepTreatsBusinessFailureAsFailure(t *testing.T) {
	store := newRegressionStore()
	executor := newExecutorWithResponder(store, func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusOK, `{"code":"1","message":"rejected"}`), nil
	})

	tx := &Transaction{ID: "tx-async-business-fail", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-async-business-fail",
		TransactionID:    tx.ID,
		Name:             "async-step",
		Type:             StepTypeAsync,
		Status:           StepStatusPending,
		ActionMethod:     http.MethodPost,
		ActionURL:        "http://unit.test/async-action",
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://unit.test/compensate",
		PollURL:          "http://unit.test/poll",
		PollMethod:       http.MethodGet,
		PollIntervalSec:  1,
		PollMaxTimes:     3,
		PollSuccessPath:  "$.status",
		PollSuccessValue: "success",
		PollFailurePath:  "$.status",
		PollFailureValue: "failed",
		MaxRetry:         1,
	}
	store.put(tx, step)

	err := executor.ExecuteAsyncStep(context.Background(), tx, step, []*Step{step})
	if !errors.Is(err, ErrStepFatal) {
		t.Fatalf("expected ErrStepFatal, got %v", err)
	}
	if store.pollTasks[step.ID] != nil {
		t.Fatal("expected no poll task to be created")
	}
}

func TestCompensateStepTreatsBusinessFailureAsFailure(t *testing.T) {
	store := newRegressionStore()
	executor := newExecutorWithResponder(store, func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusOK, `{"code":"1","message":"failed"}`), nil
	})

	tx := &Transaction{ID: "tx-comp-business-fail", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-comp-business-fail",
		TransactionID:    tx.ID,
		Name:             "comp-step",
		Type:             StepTypeSync,
		Status:           StepStatusSucceeded,
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://unit.test/compensate",
		MaxRetry:         1,
	}
	store.put(tx, step)

	err := executor.CompensateStep(context.Background(), tx, step, []*Step{step})
	if !errors.Is(err, ErrCompensationFailed) {
		t.Fatalf("expected ErrCompensationFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), `HTTP 200: {"code":"1","message":"failed"}`) {
		t.Fatalf("expected business failure response in error, got %v", err)
	}
}

func TestPollTreatsBusinessFailureAsFailure(t *testing.T) {
	store := newRegressionStore()
	executor := newExecutorWithResponder(store, func(r *http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusOK, `{"code":"1","message":"processing"}`), nil
	})

	tx := &Transaction{ID: "tx-poll-business-fail", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-poll-business-fail",
		TransactionID:    tx.ID,
		Name:             "poll-step",
		Type:             StepTypeAsync,
		Status:           StepStatusPolling,
		PollURL:          "http://unit.test/poll",
		PollMethod:       http.MethodGet,
		PollSuccessPath:  "$.status",
		PollSuccessValue: "success",
		PollFailurePath:  "$.status",
		PollFailureValue: "failed",
	}
	store.put(tx, step)

	_, err := executor.Poll(context.Background(), tx, step, []*Step{step})
	if err == nil {
		t.Fatal("expected poll failure, got nil")
	}
	if !strings.Contains(err.Error(), "poll response was not successful") {
		t.Fatalf("expected poll unsuccessful error, got %v", err)
	}
}
