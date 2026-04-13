package saga

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newJSONResponse(statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestNewExecutorUsesInjectedHTTPClient(t *testing.T) {
	customClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return newJSONResponse(http.StatusOK, `{"code":"0","ok":true}`), nil
		}),
	}

	executor := NewExecutor(newRegressionStore(), &ExecutorConfig{
		HTTPTimeout: time.Second,
		HTTPClient:  customClient,
	}, nil)

	if executor.client != customClient {
		t.Fatal("expected executor to use injected HTTP client")
	}
	if executor.client.Timeout != 5*time.Second {
		t.Fatalf("expected injected client timeout to be preserved, got %v", executor.client.Timeout)
	}
}

func TestNewExecutorCreatesDefaultHTTPClientWhenHTTPClientNil(t *testing.T) {
	executor := NewExecutor(newRegressionStore(), &ExecutorConfig{
		HTTPTimeout: 2 * time.Second,
	}, nil)

	if executor.client == nil {
		t.Fatal("expected default HTTP client to be created")
	}
	if executor.client.Timeout != 2*time.Second {
		t.Fatalf("expected default client timeout 2s, got %v", executor.client.Timeout)
	}
}

func TestExecuteStepUsesInjectedHTTPClient(t *testing.T) {
	store := newRegressionStore()
	customClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/action" {
				t.Fatalf("expected /action request, got %s", r.URL.Path)
			}
			return newJSONResponse(http.StatusOK, `{"code":"0","ok":true}`), nil
		}),
	}
	executor := NewExecutor(store, &ExecutorConfig{
		HTTPTimeout: time.Second,
		HTTPClient:  customClient,
	}, nil)

	tx := &Transaction{ID: "tx-http-client-sync", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-http-client-sync",
		TransactionID:    tx.ID,
		Name:             "sync-step",
		Type:             StepTypeSync,
		Status:           StepStatusPending,
		ActionMethod:     http.MethodPost,
		ActionURL:        "http://unit.test/action",
		ActionPayload:    map[string]any{"hello": "world"},
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://unit.test/compensate",
		MaxRetry:         1,
	}
	store.put(tx, step)

	if err := executor.ExecuteStep(context.Background(), tx, step, []*Step{step}); err != nil {
		t.Fatalf("ExecuteStep() error = %v", err)
	}
	if executor.client != customClient {
		t.Fatal("expected ExecuteStep to use injected HTTP client")
	}
}

func TestExecuteAsyncStepUsesInjectedHTTPClient(t *testing.T) {
	store := newRegressionStore()
	customClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/async-action" {
				t.Fatalf("expected /async-action request, got %s", r.URL.Path)
			}
			return newJSONResponse(http.StatusOK, `{"code":"0","task_id":"task-1"}`), nil
		}),
	}
	executor := NewExecutor(store, &ExecutorConfig{
		HTTPTimeout: time.Second,
		HTTPClient:  customClient,
	}, nil)

	tx := &Transaction{ID: "tx-http-client-async", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-http-client-async",
		TransactionID:    tx.ID,
		Name:             "async-step",
		Type:             StepTypeAsync,
		Status:           StepStatusPending,
		ActionMethod:     http.MethodPost,
		ActionURL:        "http://unit.test/async-action",
		ActionPayload:    map[string]any{"hello": "world"},
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

	if err := executor.ExecuteAsyncStep(context.Background(), tx, step, []*Step{step}); err != nil {
		t.Fatalf("ExecuteAsyncStep() error = %v", err)
	}
	if executor.client != customClient {
		t.Fatal("expected ExecuteAsyncStep to use injected HTTP client")
	}
	if store.pollTasks[step.ID] == nil {
		t.Fatal("expected poll task to be created")
	}
}

func TestCompensateStepUsesInjectedHTTPClient(t *testing.T) {
	store := newRegressionStore()
	customClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/compensate" {
				t.Fatalf("expected /compensate request, got %s", r.URL.Path)
			}
			return newJSONResponse(http.StatusOK, `{"code":"0","ok":true}`), nil
		}),
	}
	executor := NewExecutor(store, &ExecutorConfig{
		HTTPTimeout: time.Second,
		HTTPClient:  customClient,
	}, nil)

	tx := &Transaction{ID: "tx-http-client-comp", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-http-client-comp",
		TransactionID:    tx.ID,
		Name:             "comp-step",
		Type:             StepTypeSync,
		Status:           StepStatusSucceeded,
		CompensateMethod: http.MethodPost,
		CompensateURL:    "http://unit.test/compensate",
		MaxRetry:         1,
	}
	store.put(tx, step)

	if err := executor.CompensateStep(context.Background(), tx, step, []*Step{step}); err != nil {
		t.Fatalf("CompensateStep() error = %v", err)
	}
	if executor.client != customClient {
		t.Fatal("expected CompensateStep to use injected HTTP client")
	}
}

func TestPollUsesInjectedHTTPClient(t *testing.T) {
	store := newRegressionStore()
	customClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/poll" {
				t.Fatalf("expected /poll request, got %s", r.URL.Path)
			}
			return newJSONResponse(http.StatusOK, `{"code":"0","status":"success"}`), nil
		}),
	}
	executor := NewExecutor(store, &ExecutorConfig{
		HTTPTimeout: time.Second,
		HTTPClient:  customClient,
	}, nil)

	tx := &Transaction{ID: "tx-http-client-poll", Payload: map[string]any{}}
	step := &Step{
		ID:               "step-http-client-poll",
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

	resp, err := executor.Poll(context.Background(), tx, step, []*Step{step})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if executor.client != customClient {
		t.Fatal("expected Poll to use injected HTTP client")
	}
	if got := resp["status"]; got != "success" {
		t.Fatalf("expected poll status success, got %v", got)
	}
}
