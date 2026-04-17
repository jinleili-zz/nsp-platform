// File: executor.go
// Package saga - HTTP executor for SAGA step actions and compensations

package saga

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jinleili-zz/nsp-platform/auth"
	"github.com/jinleili-zz/nsp-platform/logger"
	"github.com/jinleili-zz/nsp-platform/trace"
)

// Executor errors
var (
	// ErrStepRetryable indicates the step failed but can be retried.
	ErrStepRetryable = errors.New("step failed, retry possible")
	// ErrStepFatal indicates the step failed and cannot be retried.
	ErrStepFatal = errors.New("step failed, no more retries")
	// ErrCompensationFailed indicates compensation failed.
	ErrCompensationFailed = errors.New("compensation failed")
	// ErrSigningFailed indicates request signing failed.
	ErrSigningFailed = errors.New("request signing failed")
)

// ExecutorConfig holds configuration for the Executor.
type ExecutorConfig struct {
	// HTTPTimeout is the timeout for a single HTTP request (default: 30s).
	HTTPTimeout time.Duration
	// HTTPClient is the optional custom HTTP client used for all outbound requests.
	// When non-nil, HTTPTimeout is ignored.
	HTTPClient *http.Client
	// Logger is the optional module runtime logger. Defaults to logger.Platform().
	Logger logger.Logger
}

// DefaultExecutorConfig returns the default executor configuration.
func DefaultExecutorConfig() *ExecutorConfig {
	return &ExecutorConfig{
		HTTPTimeout: 30 * time.Second,
	}
}

// Executor handles HTTP calls for SAGA step execution and compensation.
type Executor struct {
	client    *http.Client
	store     Store
	config    *ExecutorConfig
	credStore auth.CredentialStore
	log       logger.Logger
}

// NewExecutor creates a new Executor with the given store and configuration.
func NewExecutor(store Store, cfg *ExecutorConfig, credStore auth.CredentialStore) *Executor {
	if cfg == nil {
		cfg = DefaultExecutorConfig()
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: cfg.HTTPTimeout,
		}
	}

	return &Executor{
		client:    client,
		store:     store,
		config:    cfg,
		credStore: credStore,
		log:       resolveSagaLogger(cfg.Logger),
	}
}

func (e *Executor) updateStepStatus(ctx context.Context, stepID string, status StepStatus, lastError string) error {
	writeCtx, cancel := durableStoreContext(ctx)
	defer cancel()
	return e.store.UpdateStepStatus(writeCtx, stepID, status, lastError)
}

func (e *Executor) updateStepResponse(ctx context.Context, stepID string, response map[string]any) error {
	writeCtx, cancel := durableStoreContext(ctx)
	defer cancel()
	return e.store.UpdateStepResponse(writeCtx, stepID, response)
}

func (e *Executor) incrementStepRetry(ctx context.Context, stepID string) error {
	writeCtx, cancel := durableStoreContext(ctx)
	defer cancel()
	return e.store.IncrementStepRetry(writeCtx, stepID)
}

// ExecuteStep executes a synchronous step's forward action.
// Returns nil on success, ErrStepRetryable if retry is possible, ErrStepFatal if no more retries.
func (e *Executor) ExecuteStep(ctx context.Context, tx *Transaction, step *Step, allSteps []*Step) error {
	// Update step status to running
	if err := e.store.UpdateStepStatus(ctx, step.ID, StepStatusRunning, ""); err != nil {
		return fmt.Errorf("failed to update step status to running: %w", err)
	}

	// Build template data for rendering
	templateData := BuildTemplateData(tx, allSteps, step)

	// Render action URL
	renderedURL, err := RenderURL(step.ActionURL, templateData)
	if err != nil {
		e.store.UpdateStepStatus(ctx, step.ID, StepStatusFailed, fmt.Sprintf("failed to render URL: %v", err))
		return fmt.Errorf("failed to render action URL: %w", err)
	}

	// Render action payload
	var bodyBytes []byte
	if step.ActionPayload != nil {
		bodyBytes, err = RenderPayloadJSON(step.ActionPayload, templateData)
		if err != nil {
			e.store.UpdateStepStatus(ctx, step.ID, StepStatusFailed, fmt.Sprintf("failed to render payload: %v", err))
			return fmt.Errorf("failed to render action payload: %w", err)
		}
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, step.ActionMethod, renderedURL, bytes.NewReader(bodyBytes))
	if err != nil {
		e.store.UpdateStepStatus(ctx, step.ID, StepStatusFailed, fmt.Sprintf("failed to create request: %v", err))
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Saga-Transaction-Id", tx.ID)
	req.Header.Set("X-Idempotency-Key", step.ID)

	// Inject trace context for distributed tracing
	// First try from context, then fall back to transaction payload
	tc, ok := trace.TraceFromContext(ctx)
	if !ok || tc == nil {
		// Try to extract trace from transaction payload
		tc = extractTraceFromPayload(tx.Payload)
	}
	if tc != nil {
		trace.Inject(req, tc)
	}

	if err := e.signRequestIfNeeded(ctx, step, req); err != nil {
		if updateErr := e.updateStepStatus(ctx, step.ID, StepStatusFailed, err.Error()); updateErr != nil {
			return fmt.Errorf("failed to update step status after signing error: %w", updateErr)
		}
		step.Status = StepStatusFailed
		step.LastError = err.Error()
		return fmt.Errorf("%w: %v", ErrStepFatal, err)
	}

	// Execute HTTP request
	resp, err := e.client.Do(req)
	if err != nil {
		return e.handleHTTPError(ctx, step, err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return e.handleHTTPError(ctx, step, fmt.Errorf("failed to read response: %w", err))
	}

	// Check response status
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Success - parse and store response
		var response map[string]any
		if len(respBody) > 0 {
			if err := json.Unmarshal(respBody, &response); err != nil {
				// Try to store raw response
				response = map[string]any{"raw": string(respBody)}
			}
		}

		if err := e.updateStepResponse(ctx, step.ID, response); err != nil {
			return fmt.Errorf("failed to update step response: %w", err)
		}

		if err := e.updateStepStatus(ctx, step.ID, StepStatusSucceeded, ""); err != nil {
			return fmt.Errorf("failed to update step status to succeeded: %w", err)
		}

		// Update step in memory for subsequent template rendering
		step.ActionResponse = response
		step.Status = StepStatusSucceeded

		return nil
	}

	// Non-2xx response - handle as error
	errMsg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody))
	return e.handleHTTPError(ctx, step, errors.New(errMsg))
}

// ExecuteAsyncStep executes an asynchronous step's forward action and sets up polling.
// Returns nil on successful submission (step enters polling state).
func (e *Executor) ExecuteAsyncStep(ctx context.Context, tx *Transaction, step *Step, allSteps []*Step) error {
	// Update step status to running
	if err := e.store.UpdateStepStatus(ctx, step.ID, StepStatusRunning, ""); err != nil {
		return fmt.Errorf("failed to update step status to running: %w", err)
	}

	// Build template data for rendering
	templateData := BuildTemplateData(tx, allSteps, step)

	// Render action URL
	renderedURL, err := RenderURL(step.ActionURL, templateData)
	if err != nil {
		e.store.UpdateStepStatus(ctx, step.ID, StepStatusFailed, fmt.Sprintf("failed to render URL: %v", err))
		return fmt.Errorf("failed to render action URL: %w", err)
	}

	// Render action payload
	var bodyBytes []byte
	if step.ActionPayload != nil {
		bodyBytes, err = RenderPayloadJSON(step.ActionPayload, templateData)
		if err != nil {
			e.store.UpdateStepStatus(ctx, step.ID, StepStatusFailed, fmt.Sprintf("failed to render payload: %v", err))
			return fmt.Errorf("failed to render action payload: %w", err)
		}
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, step.ActionMethod, renderedURL, bytes.NewReader(bodyBytes))
	if err != nil {
		e.store.UpdateStepStatus(ctx, step.ID, StepStatusFailed, fmt.Sprintf("failed to create request: %v", err))
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Saga-Transaction-Id", tx.ID)
	req.Header.Set("X-Idempotency-Key", step.ID)

	// Inject trace context for distributed tracing
	// First try from context, then fall back to transaction payload
	tc, ok := trace.TraceFromContext(ctx)
	if !ok || tc == nil {
		// Try to extract trace from transaction payload
		tc = extractTraceFromPayload(tx.Payload)
	}
	if tc != nil {
		trace.Inject(req, tc)
	}

	if err := e.signRequestIfNeeded(ctx, step, req); err != nil {
		if updateErr := e.updateStepStatus(ctx, step.ID, StepStatusFailed, err.Error()); updateErr != nil {
			return fmt.Errorf("failed to update step status after signing error: %w", updateErr)
		}
		step.Status = StepStatusFailed
		step.LastError = err.Error()
		return fmt.Errorf("%w: %v", ErrStepFatal, err)
	}

	// Execute HTTP request
	resp, err := e.client.Do(req)
	if err != nil {
		return e.handleHTTPError(ctx, step, err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return e.handleHTTPError(ctx, step, fmt.Errorf("failed to read response: %w", err))
	}

	// Check response status
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Success - parse and store response
		var response map[string]any
		if len(respBody) > 0 {
			if err := json.Unmarshal(respBody, &response); err != nil {
				response = map[string]any{"raw": string(respBody)}
			}
		}

		if err := e.updateStepResponse(ctx, step.ID, response); err != nil {
			return fmt.Errorf("failed to update step response: %w", err)
		}

		// Update step in memory
		step.ActionResponse = response

		// Set up polling
		nextPollAt := time.Now().Add(time.Duration(step.PollIntervalSec) * time.Second)
		pollTask := &PollTask{
			StepID:        step.ID,
			TransactionID: tx.ID,
			NextPollAt:    nextPollAt,
		}

		if err := e.store.CreatePollTask(ctx, pollTask); err != nil {
			return fmt.Errorf("failed to create poll task: %w", err)
		}

		// Update step status to polling
		if err := e.updateStepStatus(ctx, step.ID, StepStatusPolling, ""); err != nil {
			return fmt.Errorf("failed to update step status to polling: %w", err)
		}

		step.Status = StepStatusPolling
		return nil
	}

	// Non-2xx response - handle as error
	errMsg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody))
	return e.handleHTTPError(ctx, step, errors.New(errMsg))
}

// CompensateStep executes a step's compensation action.
// Returns nil on success, ErrCompensationFailed if compensation fails after retries.
func (e *Executor) CompensateStep(ctx context.Context, tx *Transaction, step *Step, allSteps []*Step) error {
	// Update step status to compensating
	if err := e.store.UpdateStepStatus(ctx, step.ID, StepStatusCompensating, ""); err != nil {
		return fmt.Errorf("failed to update step status to compensating: %w", err)
	}

	// Build template data for rendering (include all steps' responses)
	templateData := BuildTemplateData(tx, allSteps, step)

	// Render compensate URL
	renderedURL, err := RenderURL(step.CompensateURL, templateData)
	if err != nil {
		errMsg := fmt.Sprintf("failed to render compensate URL: %v", err)
		e.store.UpdateStepStatus(ctx, step.ID, StepStatusFailed, errMsg)
		return fmt.Errorf("%s: %w", errMsg, ErrCompensationFailed)
	}

	// Render compensate payload
	var bodyBytes []byte
	if step.CompensatePayload != nil {
		bodyBytes, err = RenderPayloadJSON(step.CompensatePayload, templateData)
		if err != nil {
			errMsg := fmt.Sprintf("failed to render compensate payload: %v", err)
			e.store.UpdateStepStatus(ctx, step.ID, StepStatusFailed, errMsg)
			return fmt.Errorf("%s: %w", errMsg, ErrCompensationFailed)
		}
	}

	// Retry compensation with exponential backoff
	maxRetries := step.MaxRetry
	if maxRetries == 0 {
		maxRetries = 3
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s, 8s...
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		// Create HTTP request
		req, err := http.NewRequestWithContext(ctx, step.CompensateMethod, renderedURL, bytes.NewReader(bodyBytes))
		if err != nil {
			lastErr = err
			continue
		}

		// Set headers
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Saga-Transaction-Id", tx.ID)
		req.Header.Set("X-Idempotency-Key", step.ID+"-compensate")

		// Inject trace context for distributed tracing
		tc, ok := trace.TraceFromContext(ctx)
		if !ok || tc == nil {
			tc = extractTraceFromPayload(tx.Payload)
		}
		if tc != nil {
			trace.Inject(req, tc)
		}

		if err := e.signRequestIfNeeded(ctx, step, req); err != nil {
			lastErr = err
			break
		}

		// Execute HTTP request
		resp, err := e.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}

		// Check response status
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Compensation successful
			if err := e.updateStepStatus(ctx, step.ID, StepStatusCompensated, ""); err != nil {
				return fmt.Errorf("failed to update step status to compensated: %w", err)
			}
			step.Status = StepStatusCompensated
			return nil
		}

		lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// All retries exhausted
	errMsg := fmt.Sprintf("compensation failed after %d retries: %v", maxRetries, lastErr)
	_ = e.updateStepStatus(ctx, step.ID, StepStatusFailed, errMsg)
	if lastErr != nil {
		return fmt.Errorf("%s: %w: %w", errMsg, ErrCompensationFailed, lastErr)
	}
	return fmt.Errorf("%s: %w", errMsg, ErrCompensationFailed)
}

// handleHTTPError handles HTTP errors and determines if retry is possible.
func (e *Executor) handleHTTPError(ctx context.Context, step *Step, err error) error {
	if transactionExecutionTimedOut(ctx) {
		timeoutErr := fmt.Errorf("%w: %v", errTransactionExecutionTimeout, err)
		if updateErr := e.updateStepStatus(ctx, step.ID, StepStatusFailed, timeoutErr.Error()); updateErr != nil {
			return fmt.Errorf("failed to update step status after transaction timeout: %w", updateErr)
		}
		step.Status = StepStatusFailed
		step.LastError = timeoutErr.Error()
		return timeoutErr
	}

	// Increment retry count
	if incrementErr := e.incrementStepRetry(ctx, step.ID); incrementErr != nil {
		e.log.ErrorContext(ctx, "failed to increment step retry count",
			appendStepLogFields([]any{
				logger.FieldError, incrementErr,
			}, step)...,
		)
	}
	step.RetryCount++

	// Check if more retries are possible
	if step.RetryCount < step.MaxRetry {
		_ = e.updateStepStatus(ctx, step.ID, StepStatusPending, err.Error())
		return fmt.Errorf("%w: %v", ErrStepRetryable, err)
	}

	// No more retries
	_ = e.updateStepStatus(ctx, step.ID, StepStatusFailed, err.Error())
	step.Status = StepStatusFailed
	step.LastError = err.Error()
	return fmt.Errorf("%w: %v", ErrStepFatal, err)
}

// Poll executes a poll request for an async step.
// Returns the parsed response body.
func (e *Executor) Poll(ctx context.Context, tx *Transaction, step *Step, allSteps []*Step) (map[string]any, error) {
	// Build template data for rendering
	templateData := BuildTemplateData(tx, allSteps, step)

	// Render poll URL
	renderedURL, err := RenderURL(step.PollURL, templateData)
	if err != nil {
		return nil, fmt.Errorf("failed to render poll URL: %w", err)
	}

	// Create HTTP request
	method := step.PollMethod
	if method == "" {
		method = "GET"
	}

	req, err := http.NewRequestWithContext(ctx, method, renderedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create poll request: %w", err)
	}

	// Set headers
	req.Header.Set("X-Saga-Transaction-Id", tx.ID)

	// Inject trace context for distributed tracing
	tc, ok := trace.TraceFromContext(ctx)
	if !ok || tc == nil {
		tc = extractTraceFromPayload(tx.Payload)
	}
	if tc != nil {
		trace.Inject(req, tc)
	}

	if err := e.signRequestIfNeeded(ctx, step, req); err != nil {
		return nil, err
	}

	// Execute HTTP request
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("poll request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read poll response: %w", err)
	}

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("poll returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var response map[string]any
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &response); err != nil {
			return nil, fmt.Errorf("failed to parse poll response: %w", err)
		}
	}

	return response, nil
}

// extractTraceFromPayload extracts trace context from transaction payload
// This is used when the original request context is not available (e.g., background workers)
func extractTraceFromPayload(payload map[string]any) *trace.TraceContext {
	if payload == nil {
		return nil
	}

	traceID, ok := payload["_trace_id"].(string)
	if !ok || traceID == "" {
		return nil
	}

	spanID, _ := payload["_span_id"].(string)

	tc := &trace.TraceContext{
		TraceID:      traceID,
		SpanId:       trace.NewSpanId(), // Generate new span for this operation
		ParentSpanId: spanID,            // Parent is the original request span
		Sampled:      true,
	}

	return tc
}

func (e *Executor) signRequestIfNeeded(ctx context.Context, step *Step, req *http.Request) error {
	if step == nil || step.AuthAK == "" || e.credStore == nil {
		return nil
	}

	cred, err := e.credStore.GetByAK(ctx, step.AuthAK)
	if err != nil {
		return fmt.Errorf("%w: credential lookup failed for AK %q: %v", ErrSigningFailed, step.AuthAK, err)
	}
	if cred == nil || !cred.Enabled {
		return fmt.Errorf("%w: credential not found or disabled for AK %q", ErrSigningFailed, step.AuthAK)
	}

	if err := auth.NewSigner(cred.AccessKey, cred.SecretKey).Sign(req); err != nil {
		return fmt.Errorf("%w: %v", ErrSigningFailed, err)
	}

	return nil
}

// IsSigningError reports whether err was caused by request signing failure.
func IsSigningError(err error) bool {
	return errors.Is(err, ErrSigningFailed)
}
