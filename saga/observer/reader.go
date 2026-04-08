// Package observer provides read-only SAGA observation queries and view models.
package observer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultLimit is the default maximum number of rows returned by list commands.
const DefaultLimit = 100
const maxLimit = 10000

// ListFilter controls transaction list queries.
type ListFilter struct {
	// Status filters by persisted transaction status. Empty means all statuses.
	Status string
	// Limit bounds the number of returned rows.
	Limit int
}

// ListResult contains transaction summaries plus truncation metadata.
type ListResult struct {
	// Transactions are the returned summaries.
	Transactions []TransactionSummary
	// Truncated reports whether rows were omitted because of the effective limit.
	Truncated bool
	// Limit is the effective limit used by the query.
	Limit int
}

// TransactionSummary is the observer-facing summary for a SAGA transaction.
type TransactionSummary struct {
	// ID is the persisted transaction identifier.
	ID string
	// Status is the persisted transaction status.
	Status string
	// CurrentStep is the current persisted step index.
	CurrentStep int
	// CreatedAt is when the transaction row was created.
	CreatedAt time.Time
	// UpdatedAt is when the transaction row was last updated.
	UpdatedAt time.Time
	// FinishedAt is when the transaction reached a terminal state.
	FinishedAt *time.Time
	// TimeoutAt is the persisted timeout deadline when configured.
	TimeoutAt *time.Time
	// LastError stores the latest persisted error summary.
	LastError string
	// TraceID is extracted from payload._trace_id when available.
	TraceID string
	// LockedBy is the instance currently holding the transaction lease.
	LockedBy string
	// LockedUntil is the lease expiration time.
	LockedUntil *time.Time
}

// TransactionDetail is the full observer-facing transaction view.
type TransactionDetail struct {
	// Summary contains the transaction summary fields.
	Summary TransactionSummary
	// SpanID is extracted from payload._span_id when available.
	SpanID string
	// Steps are ordered by step index.
	Steps []StepDetail
}

// StepDetail is the observer-facing view for a persisted saga step.
type StepDetail struct {
	// ID is the persisted step identifier.
	ID string
	// Index is the step order within the transaction.
	Index int
	// Name is the configured step name.
	Name string
	// Type is the persisted step type.
	Type string
	// Status is the current persisted step status.
	Status string
	// RetryCount is the persisted retry counter.
	RetryCount int
	// PollCount is the persisted poll counter.
	PollCount int
	// PollMaxTimes is the configured maximum number of poll attempts.
	PollMaxTimes int
	// StartedAt is when execution first entered running state.
	StartedAt *time.Time
	// FinishedAt is when the step entered a terminal state.
	FinishedAt *time.Time
	// LastError stores the step-level error summary.
	LastError string
	// ActionResponse contains the persisted action response payload.
	ActionResponse json.RawMessage
	// PollTask describes the current poll task when one exists.
	PollTask *PollTaskDetail
}

// PollTaskDetail is the observer-facing view of a persisted poll task.
type PollTaskDetail struct {
	// NextPollAt is the next scheduled poll time.
	NextPollAt *time.Time
	// LockedBy is the instance currently holding the poll task lease.
	LockedBy string
	// LockedUntil is the poll task lease expiration.
	LockedUntil *time.Time
}

// Reader executes read-only observer queries against PostgreSQL.
type Reader struct {
	db *sql.DB
}

// NewReader creates a new read-only observer query reader.
func NewReader(db *sql.DB) *Reader {
	return &Reader{db: db}
}

// ListTransactions returns transaction summaries ordered by creation time descending.
func (r *Reader) ListTransactions(ctx context.Context, filter ListFilter) (*ListResult, error) {
	limit := normalizeLimit(filter.Limit)
	status := strings.TrimSpace(filter.Status)

	query := `
		SELECT id, status, current_step, created_at, updated_at, finished_at, timeout_at, last_error,
		       payload->>'_trace_id' AS trace_id, locked_by, locked_until
		FROM saga_transactions
		WHERE ($1 = '' OR status = $1)
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := r.db.QueryContext(ctx, query, status, limit+1)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()

	result := &ListResult{Limit: limit}
	for rows.Next() {
		tx, err := scanTransactionSummary(rows)
		if err != nil {
			return nil, err
		}
		result.Transactions = append(result.Transactions, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list transactions rows: %w", err)
	}

	if len(result.Transactions) > limit {
		result.Truncated = true
		result.Transactions = result.Transactions[:limit]
	}

	return result, nil
}

// ListFailedTransactions returns failed transactions ordered by failure recency.
func (r *Reader) ListFailedTransactions(ctx context.Context, limit int) (*ListResult, error) {
	effectiveLimit := normalizeLimit(limit)

	query := `
		SELECT id, status, current_step, created_at, updated_at, finished_at, timeout_at, last_error,
		       payload->>'_trace_id' AS trace_id, locked_by, locked_until
		FROM saga_transactions
		WHERE status = 'failed'
		ORDER BY COALESCE(finished_at, updated_at) DESC
		LIMIT $1
	`

	rows, err := r.db.QueryContext(ctx, query, effectiveLimit+1)
	if err != nil {
		return nil, fmt.Errorf("list failed transactions: %w", err)
	}
	defer rows.Close()

	result := &ListResult{Limit: effectiveLimit}
	for rows.Next() {
		tx, err := scanTransactionSummary(rows)
		if err != nil {
			return nil, err
		}
		result.Transactions = append(result.Transactions, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list failed transactions rows: %w", err)
	}

	if len(result.Transactions) > effectiveLimit {
		result.Truncated = true
		result.Transactions = result.Transactions[:effectiveLimit]
	}

	return result, nil
}

// GetTransactionDetail returns a single transaction detail plus ordered steps.
func (r *Reader) GetTransactionDetail(ctx context.Context, txID string) (*TransactionDetail, error) {
	readTx, err := r.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return nil, fmt.Errorf("begin transaction detail snapshot: %w", err)
	}
	defer readTx.Rollback()

	txQuery := `
		SELECT id, status, current_step, created_at, updated_at, finished_at, timeout_at, last_error,
		       payload->>'_trace_id' AS trace_id, locked_by, locked_until, payload->>'_span_id' AS span_id
		FROM saga_transactions
		WHERE id = $1
	`

	var detail TransactionDetail
	row := readTx.QueryRowContext(ctx, txQuery, txID)
	spanID, err := scanTransactionDetailRow(row, &detail.Summary)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	detail.SpanID = spanID

	stepQuery := `
		SELECT s.id, s.step_index, s.name, s.step_type, s.status, s.retry_count, s.poll_count, s.poll_max_times,
		       s.started_at, s.finished_at, s.last_error, s.action_response,
		       p.step_id IS NOT NULL AS has_poll_task, p.next_poll_at, p.locked_by, p.locked_until
		FROM saga_steps s
		LEFT JOIN saga_poll_tasks p ON p.step_id = s.id
		WHERE s.transaction_id = $1
		ORDER BY s.step_index
	`

	rows, err := readTx.QueryContext(ctx, stepQuery, txID)
	if err != nil {
		return nil, fmt.Errorf("get transaction steps: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		step, err := scanStepDetail(rows)
		if err != nil {
			return nil, err
		}
		detail.Steps = append(detail.Steps, step)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get transaction steps rows: %w", err)
	}

	if err := readTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction detail snapshot: %w", err)
	}

	return &detail, nil
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func scanTransactionSummary(scanner interface {
	Scan(dest ...any) error
}) (TransactionSummary, error) {
	var tx TransactionSummary
	var finishedAt, timeoutAt, lockedUntil sql.NullTime
	var lastError, traceID, lockedBy sql.NullString

	if err := scanner.Scan(
		&tx.ID,
		&tx.Status,
		&tx.CurrentStep,
		&tx.CreatedAt,
		&tx.UpdatedAt,
		&finishedAt,
		&timeoutAt,
		&lastError,
		&traceID,
		&lockedBy,
		&lockedUntil,
	); err != nil {
		return TransactionSummary{}, fmt.Errorf("scan transaction summary: %w", err)
	}

	tx.FinishedAt = nullTimePtr(finishedAt)
	tx.TimeoutAt = nullTimePtr(timeoutAt)
	tx.LockedUntil = nullTimePtr(lockedUntil)
	tx.LastError = lastError.String
	tx.TraceID = traceID.String
	tx.LockedBy = lockedBy.String

	return tx, nil
}

func scanTransactionDetailRow(row *sql.Row, summary *TransactionSummary) (string, error) {
	var finishedAt, timeoutAt, lockedUntil sql.NullTime
	var lastError, traceID, lockedBy, spanID sql.NullString

	if err := row.Scan(
		&summary.ID,
		&summary.Status,
		&summary.CurrentStep,
		&summary.CreatedAt,
		&summary.UpdatedAt,
		&finishedAt,
		&timeoutAt,
		&lastError,
		&traceID,
		&lockedBy,
		&lockedUntil,
		&spanID,
	); err != nil {
		return "", fmt.Errorf("scan transaction detail: %w", err)
	}

	summary.FinishedAt = nullTimePtr(finishedAt)
	summary.TimeoutAt = nullTimePtr(timeoutAt)
	summary.LockedUntil = nullTimePtr(lockedUntil)
	summary.LastError = lastError.String
	summary.TraceID = traceID.String
	summary.LockedBy = lockedBy.String

	return spanID.String, nil
}

func scanStepDetail(scanner interface {
	Scan(dest ...any) error
}) (StepDetail, error) {
	var step StepDetail
	var startedAt, finishedAt, pollLockedUntil sql.NullTime
	var lastError, pollLockedBy sql.NullString
	var response []byte
	var hasPollTask bool
	var pollNextAt sql.NullTime

	if err := scanner.Scan(
		&step.ID,
		&step.Index,
		&step.Name,
		&step.Type,
		&step.Status,
		&step.RetryCount,
		&step.PollCount,
		&step.PollMaxTimes,
		&startedAt,
		&finishedAt,
		&lastError,
		&response,
		&hasPollTask,
		&pollNextAt,
		&pollLockedBy,
		&pollLockedUntil,
	); err != nil {
		return StepDetail{}, fmt.Errorf("scan step detail: %w", err)
	}

	step.StartedAt = nullTimePtr(startedAt)
	step.FinishedAt = nullTimePtr(finishedAt)
	step.LastError = lastError.String
	step.ActionResponse = json.RawMessage(response)
	if hasPollTask {
		step.PollTask = &PollTaskDetail{
			NextPollAt:  nullTimePtr(pollNextAt),
			LockedBy:    pollLockedBy.String,
			LockedUntil: nullTimePtr(pollLockedUntil),
		}
	}

	return step, nil
}

func nullTimePtr(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}
