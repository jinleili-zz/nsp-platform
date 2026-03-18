package taskqueue

import (
	"context"
	"time"
)

// Priority defines task execution priority levels.
type Priority int

const (
	PriorityLow      Priority = 1
	PriorityNormal   Priority = 3
	PriorityHigh     Priority = 6
	PriorityCritical Priority = 9
)

// Task represents a task to be published to the message queue.
type Task struct {
	Type     string            // Task type identifier, e.g. "create_vrf_on_switch"
	Payload  []byte            // JSON-serialized payload
	Queue    string            // Target queue name (optional, can be computed by QueueRouter)
	Priority Priority          // Execution priority
	Metadata map[string]string // Extensible metadata
}

// TaskInfo is returned after a task is successfully published.
type TaskInfo struct {
	BrokerTaskID string // ID assigned by the underlying message queue
	Queue        string // The actual queue the task was placed into
}

// TaskPayload is the deserialized view of a task received by a worker handler.
type TaskPayload struct {
	TaskID     string // Step task ID (from workflow)
	TaskType   string // Task type identifier
	ResourceID string // Associated resource ID
	Params     []byte // Raw JSON parameters
}

// TaskResult is returned by a handler after processing.
type TaskResult struct {
	Data    interface{} // Result data (will be JSON-serialized)
	Message string      // Human-readable result message
}

// CallbackPayload carries the execution result from worker back to orchestrator.
type CallbackPayload struct {
	TaskID       string      `json:"task_id"`
	Status       string      `json:"status"` // "completed" or "failed"
	Result       interface{} `json:"result,omitempty"`
	ErrorMessage string      `json:"error_message,omitempty"`
}

// WorkflowStatus represents the lifecycle of a workflow.
type WorkflowStatus string

const (
	WorkflowStatusPending   WorkflowStatus = "pending"
	WorkflowStatusRunning   WorkflowStatus = "running"
	WorkflowStatusSucceeded WorkflowStatus = "succeeded"
	WorkflowStatusFailed    WorkflowStatus = "failed"
)

// StepStatus represents the lifecycle of a single step.
type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusQueued    StepStatus = "queued"
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
)

// Workflow represents an orchestration workflow persisted in the database.
type Workflow struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	ResourceType   string         `json:"resource_type"`
	ResourceID     string         `json:"resource_id"`
	Status         WorkflowStatus `json:"status"`
	TotalSteps     int            `json:"total_steps"`
	CompletedSteps int            `json:"completed_steps"`
	FailedSteps    int            `json:"failed_steps"`
	ErrorMessage   string         `json:"error_message,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// StepTask represents a single ordered step within a workflow.
type StepTask struct {
	ID           string     `json:"id"`
	WorkflowID   string     `json:"workflow_id"`
	StepOrder    int        `json:"step_order"`
	TaskType     string     `json:"task_type"`
	TaskName     string     `json:"task_name"`
	Params       string     `json:"params"`
	Status       StepStatus `json:"status"`
	Priority     Priority   `json:"priority"`
	QueueTag     string     `json:"queue_tag"`
	BrokerTaskID string     `json:"broker_task_id,omitempty"`
	Result       string     `json:"result,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	RetryCount   int        `json:"retry_count"`
	MaxRetries   int        `json:"max_retries"`
	CreatedAt    time.Time  `json:"created_at"`
	QueuedAt     *time.Time `json:"queued_at,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// StepStats holds aggregated statistics for steps in a workflow.
type StepStats struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Pending   int `json:"pending"`
}

// WorkflowDefinition describes a workflow to be submitted to the engine.
type WorkflowDefinition struct {
	Name         string
	ResourceType string
	ResourceID   string
	Metadata     map[string]string
	Steps        []StepDefinition
}

// StepDefinition describes a single step within a workflow definition.
type StepDefinition struct {
	TaskType   string
	TaskName   string
	Params     string   // JSON string
	QueueTag   string   // Routing tag (e.g. device type)
	Priority   Priority
	MaxRetries int
}

// WorkflowStatusResponse is the query result for a workflow's current state.
type WorkflowStatusResponse struct {
	Workflow *Workflow    `json:"workflow"`
	Steps    []*StepTask `json:"steps"`
	Stats    *StepStats  `json:"stats"`
}

// WorkflowHooks provides lifecycle callbacks that the Engine invokes
// at key points in the workflow state machine. Business code uses these
// hooks to synchronise external resources (e.g. vpc_resources table)
// with workflow/step state transitions managed by the Engine.
// All fields are optional — nil hooks are silently skipped.
type WorkflowHooks struct {
	// OnStepComplete is called after a step succeeds and tq_steps is updated,
	// before the next step is enqueued.
	OnStepComplete func(ctx context.Context, workflow *Workflow, step *StepTask) error
	// OnStepFailed is called when a step's retries are exhausted and the
	// workflow is about to be marked as failed.
	OnStepFailed func(ctx context.Context, workflow *Workflow, step *StepTask, errMsg string) error
	// OnWorkflowComplete is called after the workflow is atomically marked
	// as succeeded (all steps completed, zero failures).
	OnWorkflowComplete func(ctx context.Context, workflow *Workflow) error
	// OnWorkflowFailed is called after the workflow is marked as failed
	// (a step exhausted all retries).
	OnWorkflowFailed func(ctx context.Context, workflow *Workflow, errMsg string) error
}
