package taskqueue

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// PostgresStore implements Store using PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgresStore with the given database connection.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Migrate creates the required tables.
func (s *PostgresStore) Migrate(ctx context.Context) error {
	data, err := migrationFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		return fmt.Errorf("failed to read migration file: %w", err)
	}
	_, err = s.db.ExecContext(ctx, string(data))
	if err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}
	return nil
}

// CreateWorkflow inserts a new workflow record.
func (s *PostgresStore) CreateWorkflow(ctx context.Context, wf *Workflow) error {
	metadataJSON, err := json.Marshal(wf.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO tq_workflows (id, name, resource_type, resource_id, status, total_steps, completed_steps, failed_steps, error_message, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`
	_, err = s.db.ExecContext(ctx, query,
		wf.ID, wf.Name, wf.ResourceType, wf.ResourceID,
		string(wf.Status), wf.TotalSteps, wf.CompletedSteps, wf.FailedSteps,
		wf.ErrorMessage, metadataJSON, wf.CreatedAt, wf.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create workflow: %w", err)
	}
	return nil
}

// GetWorkflow retrieves a workflow by ID.
func (s *PostgresStore) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
	query := `
		SELECT id, name, resource_type, resource_id, status,
		       total_steps, completed_steps, failed_steps, error_message, metadata,
		       created_at, updated_at
		FROM tq_workflows WHERE id = $1
	`
	wf := &Workflow{}
	var status string
	var errorMsg sql.NullString
	var metadataJSON []byte

	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&wf.ID, &wf.Name, &wf.ResourceType, &wf.ResourceID, &status,
		&wf.TotalSteps, &wf.CompletedSteps, &wf.FailedSteps, &errorMsg, &metadataJSON,
		&wf.CreatedAt, &wf.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow: %w", err)
	}

	wf.Status = WorkflowStatus(status)
	if errorMsg.Valid {
		wf.ErrorMessage = errorMsg.String
	}
	if len(metadataJSON) > 0 {
		_ = json.Unmarshal(metadataJSON, &wf.Metadata)
	}
	return wf, nil
}

// UpdateWorkflowStatus updates workflow status and error message.
func (s *PostgresStore) UpdateWorkflowStatus(ctx context.Context, id string, status WorkflowStatus, errorMsg string) error {
	query := `UPDATE tq_workflows SET status = $2, error_message = $3, updated_at = $4 WHERE id = $1`
	result, err := s.db.ExecContext(ctx, query, id, string(status), errorMsg, time.Now())
	if err != nil {
		return fmt.Errorf("failed to update workflow status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("workflow not found: %s", id)
	}
	return nil
}

// IncrementCompletedSteps increments completed step count by 1.
func (s *PostgresStore) IncrementCompletedSteps(ctx context.Context, id string) error {
	query := `UPDATE tq_workflows SET completed_steps = completed_steps + 1, updated_at = $2 WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id, time.Now())
	return err
}

// IncrementFailedSteps increments failed step count by 1.
func (s *PostgresStore) IncrementFailedSteps(ctx context.Context, id string) error {
	query := `UPDATE tq_workflows SET failed_steps = failed_steps + 1, updated_at = $2 WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id, time.Now())
	return err
}

// TryCompleteWorkflow atomically marks workflow as succeeded if all steps are completed.
// Uses conditional UPDATE to avoid TOCTOU race condition.
func (s *PostgresStore) TryCompleteWorkflow(ctx context.Context, id string) (bool, error) {
	query := `
		UPDATE tq_workflows 
		SET status = 'succeeded', updated_at = $2 
		WHERE id = $1 
		  AND status = 'running'
		  AND completed_steps = total_steps 
		  AND failed_steps = 0
	`
	result, err := s.db.ExecContext(ctx, query, id, time.Now())
	if err != nil {
		return false, fmt.Errorf("failed to try complete workflow: %w", err)
	}
	rows, _ := result.RowsAffected()
	return rows > 0, nil
}

// BatchCreateSteps inserts multiple steps in a single transaction.
func (s *PostgresStore) BatchCreateSteps(ctx context.Context, steps []*StepTask) error {
	if len(steps) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	query := `
		INSERT INTO tq_steps (id, workflow_id, step_order, task_type, task_name, params, status, priority, queue_tag, retry_count, max_retries, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, step := range steps {
		// params column is JSONB: pass nil when empty
		var paramsVal interface{}
		if step.Params != "" {
			paramsVal = step.Params
		}
		_, err := stmt.ExecContext(ctx,
			step.ID, step.WorkflowID, step.StepOrder, step.TaskType, step.TaskName,
			paramsVal, string(step.Status), int(step.Priority), step.QueueTag,
			step.RetryCount, step.MaxRetries, step.CreatedAt, step.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("failed to create step %s: %w", step.ID, err)
		}
	}

	return tx.Commit()
}

// GetStep retrieves a single step by ID.
func (s *PostgresStore) GetStep(ctx context.Context, id string) (*StepTask, error) {
	query := `
		SELECT id, workflow_id, step_order, task_type, task_name, params, status, priority,
		       queue_tag, broker_task_id, result, error_message,
		       retry_count, max_retries, created_at, queued_at, started_at, completed_at, updated_at
		FROM tq_steps WHERE id = $1
	`
	return s.scanStepFrom(s.db.QueryRowContext(ctx, query, id))
}

// GetStepsByWorkflow retrieves all steps of a workflow ordered by step_order.
func (s *PostgresStore) GetStepsByWorkflow(ctx context.Context, workflowID string) ([]*StepTask, error) {
	query := `
		SELECT id, workflow_id, step_order, task_type, task_name, params, status, priority,
		       queue_tag, broker_task_id, result, error_message,
		       retry_count, max_retries, created_at, queued_at, started_at, completed_at, updated_at
		FROM tq_steps WHERE workflow_id = $1 ORDER BY step_order ASC
	`
	rows, err := s.db.QueryContext(ctx, query, workflowID)
	if err != nil {
		return nil, fmt.Errorf("failed to query steps: %w", err)
	}
	defer rows.Close()

	var steps []*StepTask
	for rows.Next() {
		step, err := s.scanStepFrom(rows)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

// GetNextPendingStep returns the first pending step of a workflow.
func (s *PostgresStore) GetNextPendingStep(ctx context.Context, workflowID string) (*StepTask, error) {
	query := `
		SELECT id, workflow_id, step_order, task_type, task_name, params, status, priority,
		       queue_tag, broker_task_id, result, error_message,
		       retry_count, max_retries, created_at, queued_at, started_at, completed_at, updated_at
		FROM tq_steps WHERE workflow_id = $1 AND status = 'pending'
		ORDER BY step_order ASC LIMIT 1
	`
	step, err := s.scanStepFrom(s.db.QueryRowContext(ctx, query, workflowID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return step, err
}

// UpdateStepStatus updates the status of a step and sets relevant timestamps.
func (s *PostgresStore) UpdateStepStatus(ctx context.Context, id string, status StepStatus) error {
	now := time.Now()
	var query string

	switch status {
	case StepStatusQueued:
		query = `UPDATE tq_steps SET status = $2, queued_at = $3, updated_at = $3 WHERE id = $1`
	case StepStatusRunning:
		query = `UPDATE tq_steps SET status = $2, started_at = $3, updated_at = $3 WHERE id = $1`
	case StepStatusCompleted, StepStatusFailed:
		query = `UPDATE tq_steps SET status = $2, completed_at = $3, updated_at = $3 WHERE id = $1`
	default:
		query = `UPDATE tq_steps SET status = $2, updated_at = $3 WHERE id = $1`
	}

	_, err := s.db.ExecContext(ctx, query, id, string(status), now)
	return err
}

// UpdateStepResult updates step status, result, and error message.
func (s *PostgresStore) UpdateStepResult(ctx context.Context, id string, status StepStatus, result string, errorMsg string) error {
	now := time.Now()
	// result column is JSONB: pass nil when empty to avoid invalid JSON error
	var resultVal interface{}
	if result != "" {
		resultVal = result
	}
	query := `UPDATE tq_steps SET status = $2, result = $3, error_message = $4, completed_at = $5, updated_at = $5 WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id, string(status), resultVal, errorMsg, now)
	return err
}

// UpdateStepBrokerID stores the broker-assigned task ID.
func (s *PostgresStore) UpdateStepBrokerID(ctx context.Context, id string, brokerTaskID string) error {
	query := `UPDATE tq_steps SET broker_task_id = $2, updated_at = $3 WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id, brokerTaskID, time.Now())
	return err
}

// IncrementStepRetryCount increments the retry count for a step.
func (s *PostgresStore) IncrementStepRetryCount(ctx context.Context, id string) error {
	query := `UPDATE tq_steps SET retry_count = retry_count + 1, updated_at = $2 WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id, time.Now())
	return err
}

// GetStepStats returns aggregated step statistics for a workflow.
func (s *PostgresStore) GetStepStats(ctx context.Context, workflowID string) (*StepStats, error) {
	query := `
		SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0) as completed,
			COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) as failed
		FROM tq_steps WHERE workflow_id = $1
	`
	stats := &StepStats{}
	err := s.db.QueryRowContext(ctx, query, workflowID).Scan(&stats.Total, &stats.Completed, &stats.Failed)
	if err != nil {
		return nil, fmt.Errorf("failed to get step stats: %w", err)
	}
	stats.Pending = stats.Total - stats.Completed - stats.Failed
	return stats, nil
}

// scanner is an interface that both *sql.Row and *sql.Rows implement.
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanStepFrom scans a single step from any scanner (Row or Rows).
func (s *PostgresStore) scanStepFrom(sc scanner) (*StepTask, error) {
	step := &StepTask{}
	var status string
	var priority int
	var queueTag, brokerTaskID, result, errorMsg sql.NullString
	var queuedAt, startedAt, completedAt sql.NullTime

	err := sc.Scan(
		&step.ID, &step.WorkflowID, &step.StepOrder, &step.TaskType, &step.TaskName,
		&step.Params, &status, &priority,
		&queueTag, &brokerTaskID, &result, &errorMsg,
		&step.RetryCount, &step.MaxRetries,
		&step.CreatedAt, &queuedAt, &startedAt, &completedAt, &step.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	step.Status = StepStatus(status)
	step.Priority = Priority(priority)
	if queueTag.Valid {
		step.QueueTag = queueTag.String
	}
	if brokerTaskID.Valid {
		step.BrokerTaskID = brokerTaskID.String
	}
	if result.Valid {
		step.Result = result.String
	}
	if errorMsg.Valid {
		step.ErrorMessage = errorMsg.String
	}
	if queuedAt.Valid {
		step.QueuedAt = &queuedAt.Time
	}
	if startedAt.Valid {
		step.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		step.CompletedAt = &completedAt.Time
	}
	return step, nil
}
