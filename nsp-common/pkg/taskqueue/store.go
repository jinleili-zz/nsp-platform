package taskqueue

import "context"

// Store defines the persistence interface for workflow and step state.
// The default implementation uses PostgreSQL.
type Store interface {
	// --- Schema ---
	// Migrate creates the required database tables if they don't exist.
	Migrate(ctx context.Context) error

	// --- Workflow ---
	CreateWorkflow(ctx context.Context, wf *Workflow) error
	GetWorkflow(ctx context.Context, id string) (*Workflow, error)
	// GetWorkflowsByResourceID returns all workflows for a given resource, ordered by created_at DESC.
	// This is needed because business APIs query by resource ID (e.g. vpc_id), not workflow UUID.
	GetWorkflowsByResourceID(ctx context.Context, resourceType, resourceID string) ([]*Workflow, error)
	UpdateWorkflowStatus(ctx context.Context, id string, status WorkflowStatus, errorMsg string) error
	IncrementCompletedSteps(ctx context.Context, id string) error
	IncrementFailedSteps(ctx context.Context, id string) error
	// TryCompleteWorkflow atomically marks workflow as succeeded if all steps are completed.
	// Returns true if workflow was marked as succeeded, false if conditions not met.
	TryCompleteWorkflow(ctx context.Context, id string) (bool, error)

	// --- Step ---
	BatchCreateSteps(ctx context.Context, steps []*StepTask) error
	GetStep(ctx context.Context, id string) (*StepTask, error)
	GetStepsByWorkflow(ctx context.Context, workflowID string) ([]*StepTask, error)
	GetNextPendingStep(ctx context.Context, workflowID string) (*StepTask, error)
	UpdateStepStatus(ctx context.Context, id string, status StepStatus) error
	UpdateStepResult(ctx context.Context, id string, status StepStatus, result string, errorMsg string) error
	UpdateStepBrokerID(ctx context.Context, id string, brokerTaskID string) error
	GetStepStats(ctx context.Context, workflowID string) (*StepStats, error)
	IncrementStepRetryCount(ctx context.Context, id string) error
}
