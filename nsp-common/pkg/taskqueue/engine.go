package taskqueue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

// Config holds configuration for the Engine.
type Config struct {
	// DSN is the PostgreSQL connection string.
	DSN string
	// CallbackQueue is the queue name for receiving worker callbacks.
	CallbackQueue string
	// QueueRouter computes the target queue name for a step.
	// If nil, DefaultQueueRouter is used.
	QueueRouter QueueRouterFunc
	// Hooks provides optional lifecycle callbacks for workflow/step transitions.
	// If nil, no hooks are called.
	Hooks *WorkflowHooks
}

// QueueRouterFunc computes the target queue name for a given step.
// Parameters: queueTag (e.g. device type), priority.
type QueueRouterFunc func(queueTag string, priority Priority) string

// DefaultQueueRouter returns "tasks_{queueTag}" with priority suffix.
func DefaultQueueRouter(queueTag string, priority Priority) string {
	base := "tasks"
	if queueTag != "" {
		base = "tasks_" + queueTag
	}
	switch priority {
	case PriorityCritical:
		return base + "_critical"
	case PriorityHigh:
		return base + "_high"
	case PriorityLow:
		return base + "_low"
	default:
		return base
	}
}

// Engine is the main entry point for the taskqueue framework.
// It provides APIs for both the orchestrator side (submit/callback/query)
// and helpers for the worker side (CallbackSender).
type Engine struct {
	broker      Broker
	store       Store
	db          *sql.DB
	config      *Config
	queueRouter QueueRouterFunc
	hooks       *WorkflowHooks
}

// NewEngine creates a new Engine.
// The caller provides a Broker implementation (e.g. asynqbroker.NewBroker).
func NewEngine(cfg *Config, broker Broker) (*Engine, error) {
	if cfg.DSN == "" {
		return nil, fmt.Errorf("DSN is required")
	}

	db, err := sql.Open("postgres", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	store := NewPostgresStore(db)

	router := cfg.QueueRouter
	if router == nil {
		router = DefaultQueueRouter
	}

	return &Engine{
		broker:      broker,
		store:       store,
		db:          db,
		config:      cfg,
		queueRouter: router,
		hooks:       cfg.Hooks,
	}, nil
}

// NewEngineWithStore creates an Engine with an externally provided Store.
// Useful for testing or when the caller manages the DB connection.
func NewEngineWithStore(cfg *Config, broker Broker, store Store) *Engine {
	router := cfg.QueueRouter
	if router == nil {
		router = DefaultQueueRouter
	}
	return &Engine{
		broker:      broker,
		store:       store,
		config:      cfg,
		queueRouter: router,
		hooks:       cfg.Hooks,
	}
}

// Migrate runs database migrations.
func (e *Engine) Migrate(ctx context.Context) error {
	return e.store.Migrate(ctx)
}

// Store returns the underlying Store for direct access.
func (e *Engine) Store() Store {
	return e.store
}

// Stop releases engine resources.
func (e *Engine) Stop() error {
	if e.db != nil {
		return e.db.Close()
	}
	return nil
}

// SubmitWorkflow creates a workflow and enqueues the first step.
func (e *Engine) SubmitWorkflow(ctx context.Context, def *WorkflowDefinition) (string, error) {
	if def == nil || len(def.Steps) == 0 {
		return "", fmt.Errorf("workflow definition must have at least one step")
	}

	workflowID := uuid.New().String()
	now := time.Now()

	wf := &Workflow{
		ID:           workflowID,
		Name:         def.Name,
		ResourceType: def.ResourceType,
		ResourceID:   def.ResourceID,
		Status:       WorkflowStatusPending,
		TotalSteps:   len(def.Steps),
		Metadata:     def.Metadata,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := e.store.CreateWorkflow(ctx, wf); err != nil {
		return "", fmt.Errorf("failed to create workflow: %w", err)
	}

	steps := make([]*StepTask, len(def.Steps))
	for i, sd := range def.Steps {
		priority := sd.Priority
		if priority == 0 {
			priority = PriorityNormal
		}
		maxRetries := sd.MaxRetries
		if maxRetries == 0 {
			maxRetries = 3
		}
		steps[i] = &StepTask{
			ID:         uuid.New().String(),
			WorkflowID: workflowID,
			StepOrder:  i + 1,
			TaskType:   sd.TaskType,
			TaskName:   sd.TaskName,
			Params:     sd.Params,
			Status:     StepStatusPending,
			Priority:   priority,
			QueueTag:   sd.QueueTag,
			MaxRetries: maxRetries,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
	}

	if err := e.store.BatchCreateSteps(ctx, steps); err != nil {
		return "", fmt.Errorf("failed to create steps: %w", err)
	}

	if err := e.store.UpdateWorkflowStatus(ctx, workflowID, WorkflowStatusRunning, ""); err != nil {
		return "", fmt.Errorf("failed to update workflow status: %w", err)
	}

	if err := e.enqueueStep(ctx, steps[0]); err != nil {
		return "", fmt.Errorf("failed to enqueue first step: %w", err)
	}

	log.Printf("[taskqueue] workflow submitted: id=%s, name=%s, steps=%d", workflowID, def.Name, len(def.Steps))
	return workflowID, nil
}

// HandleCallback processes a callback from a worker.
// It drives the workflow state machine: success -> enqueue next step, failure -> mark failed.
func (e *Engine) HandleCallback(ctx context.Context, cb *CallbackPayload) error {
	if cb == nil {
		return fmt.Errorf("callback payload is nil")
	}

	log.Printf("[taskqueue] callback received: task_id=%s, status=%s", cb.TaskID, cb.Status)

	step, err := e.store.GetStep(ctx, cb.TaskID)
	if err != nil {
		return fmt.Errorf("failed to get step: %w", err)
	}
	if step == nil {
		return fmt.Errorf("step not found: %s", cb.TaskID)
	}

	resultJSON := ""
	if cb.Result != nil {
		data, _ := json.Marshal(cb.Result)
		resultJSON = string(data)
	}

	status := StepStatus(cb.Status)
	if err := e.store.UpdateStepResult(ctx, cb.TaskID, status, resultJSON, cb.ErrorMessage); err != nil {
		return fmt.Errorf("failed to update step result: %w", err)
	}

	switch status {
	case StepStatusCompleted:
		return e.handleStepSuccess(ctx, step)
	case StepStatusFailed:
		return e.handleStepFailure(ctx, step, cb.ErrorMessage)
	default:
		return fmt.Errorf("unexpected callback status: %s", cb.Status)
	}
}

// QueryWorkflow returns the current state of a workflow.
func (e *Engine) QueryWorkflow(ctx context.Context, workflowID string) (*WorkflowStatusResponse, error) {
	wf, err := e.store.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow: %w", err)
	}
	if wf == nil {
		return nil, nil
	}

	steps, err := e.store.GetStepsByWorkflow(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to get steps: %w", err)
	}

	stats, err := e.store.GetStepStats(ctx, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	return &WorkflowStatusResponse{
		Workflow: wf,
		Steps:   steps,
		Stats:   stats,
	}, nil
}

// RetryStep re-enqueues a failed step and resets the workflow to running.
func (e *Engine) RetryStep(ctx context.Context, stepID string) error {
	step, err := e.store.GetStep(ctx, stepID)
	if err != nil {
		return fmt.Errorf("failed to get step: %w", err)
	}
	if step == nil {
		return fmt.Errorf("step not found: %s", stepID)
	}
	if step.Status != StepStatusFailed {
		return fmt.Errorf("step status is %s, only failed steps can be retried", step.Status)
	}

	// Reset workflow status to running so the state machine can proceed
	if err := e.store.UpdateWorkflowStatus(ctx, step.WorkflowID, WorkflowStatusRunning, ""); err != nil {
		return fmt.Errorf("failed to reset workflow status: %w", err)
	}

	if err := e.store.UpdateStepStatus(ctx, stepID, StepStatusPending); err != nil {
		return fmt.Errorf("failed to reset step status: %w", err)
	}

	if err := e.enqueueStep(ctx, step); err != nil {
		return fmt.Errorf("failed to re-enqueue step: %w", err)
	}

	log.Printf("[taskqueue] step retried: id=%s, type=%s", stepID, step.TaskType)
	return nil
}

// NewCallbackSender creates a CallbackSender for use by workers.
func (e *Engine) NewCallbackSender() *CallbackSender {
	return &CallbackSender{
		broker:        e.broker,
		callbackQueue: e.config.CallbackQueue,
	}
}

// enqueueStep publishes a step to the message queue.
func (e *Engine) enqueueStep(ctx context.Context, step *StepTask) error {
	// Fetch workflow to get the business resource ID
	wf, err := e.store.GetWorkflow(ctx, step.WorkflowID)
	if err != nil {
		return fmt.Errorf("failed to get workflow for resource_id: %w", err)
	}
	if wf == nil {
		return fmt.Errorf("workflow not found: %s", step.WorkflowID)
	}

	payload := map[string]interface{}{
		"task_id":     step.ID,
		"resource_id": wf.ResourceID, // Use business resource ID, not workflow UUID
		"task_params": step.Params,
	}
	payloadData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal step payload: %w", err)
	}

	queueName := e.queueRouter(step.QueueTag, step.Priority)

	task := &Task{
		Type:     step.TaskType,
		Payload:  payloadData,
		Queue:    queueName,
		Priority: step.Priority,
	}

	info, err := e.broker.Publish(ctx, task)
	if err != nil {
		return fmt.Errorf("failed to publish task: %w", err)
	}

	if err := e.store.UpdateStepBrokerID(ctx, step.ID, info.BrokerTaskID); err != nil {
		return fmt.Errorf("failed to update broker task id: %w", err)
	}

	if err := e.store.UpdateStepStatus(ctx, step.ID, StepStatusQueued); err != nil {
		return fmt.Errorf("failed to update step status: %w", err)
	}

	log.Printf("[taskqueue] step enqueued: id=%s, type=%s, queue=%s, broker_id=%s",
		step.ID, step.TaskType, queueName, info.BrokerTaskID)
	return nil
}

func (e *Engine) handleStepSuccess(ctx context.Context, step *StepTask) error {
	if err := e.store.IncrementCompletedSteps(ctx, step.WorkflowID); err != nil {
		return fmt.Errorf("failed to increment completed steps: %w", err)
	}

	// Invoke OnStepComplete hook
	if e.hooks != nil && e.hooks.OnStepComplete != nil {
		wf, err := e.store.GetWorkflow(ctx, step.WorkflowID)
		if err != nil {
			return fmt.Errorf("failed to get workflow for hook: %w", err)
		}
		if err := e.hooks.OnStepComplete(ctx, wf, step); err != nil {
			return fmt.Errorf("OnStepComplete hook failed: %w", err)
		}
	}

	nextStep, err := e.store.GetNextPendingStep(ctx, step.WorkflowID)
	if err != nil {
		return fmt.Errorf("failed to get next pending step: %w", err)
	}

	if nextStep != nil {
		if err := e.enqueueStep(ctx, nextStep); err != nil {
			return fmt.Errorf("failed to enqueue next step: %w", err)
		}
	} else {
		if err := e.checkAndCompleteWorkflow(ctx, step.WorkflowID); err != nil {
			return fmt.Errorf("failed to check workflow completion: %w", err)
		}
	}
	return nil
}

func (e *Engine) handleStepFailure(ctx context.Context, step *StepTask, errorMsg string) error {
	// Check if we should retry
	if step.RetryCount < step.MaxRetries {
		// Increment retry count
		if err := e.store.IncrementStepRetryCount(ctx, step.ID); err != nil {
			return fmt.Errorf("failed to increment retry count: %w", err)
		}
		step.RetryCount++

		// Reset step status and re-enqueue
		if err := e.store.UpdateStepStatus(ctx, step.ID, StepStatusPending); err != nil {
			return fmt.Errorf("failed to reset step status for retry: %w", err)
		}

		if err := e.enqueueStep(ctx, step); err != nil {
			return fmt.Errorf("failed to re-enqueue step for retry: %w", err)
		}

		log.Printf("[taskqueue] step retry scheduled: id=%s, retry=%d/%d, error=%s",
			step.ID, step.RetryCount, step.MaxRetries, errorMsg)
		return nil
	}

	// No more retries, mark workflow as failed
	if err := e.store.IncrementFailedSteps(ctx, step.WorkflowID); err != nil {
		return fmt.Errorf("failed to increment failed steps: %w", err)
	}

	if err := e.store.UpdateWorkflowStatus(ctx, step.WorkflowID, WorkflowStatusFailed, errorMsg); err != nil {
		return fmt.Errorf("failed to update workflow status: %w", err)
	}

	// Invoke OnStepFailed and OnWorkflowFailed hooks
	if e.hooks != nil {
		wf, err := e.store.GetWorkflow(ctx, step.WorkflowID)
		if err != nil {
			return fmt.Errorf("failed to get workflow for hook: %w", err)
		}
		if e.hooks.OnStepFailed != nil {
			if err := e.hooks.OnStepFailed(ctx, wf, step, errorMsg); err != nil {
				return fmt.Errorf("OnStepFailed hook failed: %w", err)
			}
		}
		if e.hooks.OnWorkflowFailed != nil {
			if err := e.hooks.OnWorkflowFailed(ctx, wf, errorMsg); err != nil {
				return fmt.Errorf("OnWorkflowFailed hook failed: %w", err)
			}
		}
	}

	log.Printf("[taskqueue] workflow failed: id=%s, step=%s, retries_exhausted=%d, error=%s",
		step.WorkflowID, step.TaskName, step.MaxRetries, errorMsg)
	return nil
}

func (e *Engine) checkAndCompleteWorkflow(ctx context.Context, workflowID string) error {
	// Use atomic conditional update to avoid TOCTOU race condition
	completed, err := e.store.TryCompleteWorkflow(ctx, workflowID)
	if err != nil {
		return fmt.Errorf("failed to complete workflow: %w", err)
	}
	if completed {
		log.Printf("[taskqueue] workflow succeeded: id=%s", workflowID)

		// Invoke OnWorkflowComplete hook
		if e.hooks != nil && e.hooks.OnWorkflowComplete != nil {
			wf, err := e.store.GetWorkflow(ctx, workflowID)
			if err != nil {
				return fmt.Errorf("failed to get workflow for hook: %w", err)
			}
			if err := e.hooks.OnWorkflowComplete(ctx, wf); err != nil {
				return fmt.Errorf("OnWorkflowComplete hook failed: %w", err)
			}
		}
	}
	return nil
}

// CallbackSender is used by workers to send execution results back to the orchestrator.
type CallbackSender struct {
	broker        Broker
	callbackQueue string
}

// NewCallbackSenderFromBroker creates a CallbackSender from a broker and queue name directly.
func NewCallbackSenderFromBroker(broker Broker, callbackQueue string) *CallbackSender {
	return &CallbackSender{
		broker:        broker,
		callbackQueue: callbackQueue,
	}
}

// Success sends a successful callback.
func (s *CallbackSender) Success(ctx context.Context, taskID string, result interface{}) error {
	return s.send(ctx, taskID, "completed", result, "")
}

// Fail sends a failure callback.
func (s *CallbackSender) Fail(ctx context.Context, taskID string, errorMsg string) error {
	return s.send(ctx, taskID, "failed", nil, errorMsg)
}

func (s *CallbackSender) send(ctx context.Context, taskID, status string, result interface{}, errorMsg string) error {
	cb := &CallbackPayload{
		TaskID:       taskID,
		Status:       status,
		Result:       result,
		ErrorMessage: errorMsg,
	}
	data, err := json.Marshal(cb)
	if err != nil {
		return fmt.Errorf("failed to marshal callback: %w", err)
	}

	task := &Task{
		Type:    "task_callback",
		Payload: data,
		Queue:   s.callbackQueue,
	}

	info, err := s.broker.Publish(ctx, task)
	if err != nil {
		return fmt.Errorf("failed to publish callback: %w", err)
	}

	log.Printf("[taskqueue] callback sent: task_id=%s, status=%s, queue=%s, broker_id=%s",
		taskID, status, s.callbackQueue, info.BrokerTaskID)
	return nil
}
