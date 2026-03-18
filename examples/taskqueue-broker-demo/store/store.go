// TaskQueue Broker Demo - Store Package
// This package contains shared types and database operations.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

const (
	RedisAddr     = "127.0.0.1:6379"
	PgDSN         = "postgres://admin:admin123@127.0.0.1:5432/taskqueue_broker?sslmode=disable"
	TaskQueue     = "broker_tasks"
	CallbackQueue = "broker_callbacks"
)

const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
)

// Task represents a task stored in PostgreSQL (custom implementation)
type Task struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Type         string     `json:"type"`
	Payload      string     `json:"payload"`
	Status       string     `json:"status"`
	Result       string     `json:"result,omitempty"`
	ErrorMsg     string     `json:"error_msg,omitempty"`
	RetryCount   int        `json:"retry_count"`
	MaxRetries   int        `json:"max_retries"`
	BrokerTaskID string     `json:"broker_task_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

// TaskStore manages task persistence in PostgreSQL
type TaskStore struct {
	db *sql.DB
}

// NewTaskStore creates a new TaskStore
func NewTaskStore(dsn string) (*TaskStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}
	return &TaskStore{db: db}, nil
}

// Migrate creates the tasks table
func (s *TaskStore) Migrate(ctx context.Context) error {
	query := `
		CREATE TABLE IF NOT EXISTS broker_tasks (
			id VARCHAR(64) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			type VARCHAR(128) NOT NULL,
			payload TEXT NOT NULL,
			status VARCHAR(32) NOT NULL DEFAULT 'pending',
			result TEXT,
			error_msg TEXT,
			retry_count INT NOT NULL DEFAULT 0,
			max_retries INT NOT NULL DEFAULT 3,
			broker_task_id VARCHAR(128),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			completed_at TIMESTAMPTZ
		);
		CREATE INDEX IF NOT EXISTS idx_broker_tasks_status ON broker_tasks(status);
	`
	_, err := s.db.ExecContext(ctx, query)
	return err
}

// Create inserts a new task
func (s *TaskStore) Create(ctx context.Context, task *Task) error {
	query := `
		INSERT INTO broker_tasks (id, name, type, payload, status, retry_count, max_retries, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := s.db.ExecContext(ctx, query,
		task.ID, task.Name, task.Type, task.Payload, task.Status,
		task.RetryCount, task.MaxRetries, task.CreatedAt, task.UpdatedAt)
	return err
}

// GetByID retrieves a task by ID
func (s *TaskStore) GetByID(ctx context.Context, id string) (*Task, error) {
	query := `
		SELECT id, name, type, payload, status, result, error_msg, retry_count, max_retries, broker_task_id, created_at, updated_at, completed_at
		FROM broker_tasks WHERE id = $1
	`
	row := s.db.QueryRowContext(ctx, query, id)

	var task Task
	var result, errorMsg, brokerTaskID sql.NullString
	var completedAt sql.NullTime

	err := row.Scan(&task.ID, &task.Name, &task.Type, &task.Payload, &task.Status,
		&result, &errorMsg, &task.RetryCount, &task.MaxRetries, &brokerTaskID,
		&task.CreatedAt, &task.UpdatedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if result.Valid {
		task.Result = result.String
	}
	if errorMsg.Valid {
		task.ErrorMsg = errorMsg.String
	}
	if brokerTaskID.Valid {
		task.BrokerTaskID = brokerTaskID.String
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}

	return &task, nil
}

// UpdateStatus updates task status
func (s *TaskStore) UpdateStatus(ctx context.Context, id, status, errorMsg string) error {
	query := `UPDATE broker_tasks SET status = $2, error_msg = $3, updated_at = NOW() WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id, status, errorMsg)
	return err
}

// UpdateResult updates task result when completed
func (s *TaskStore) UpdateResult(ctx context.Context, id, status, result string) error {
	query := `UPDATE broker_tasks SET status = $2, result = $3, updated_at = NOW(), completed_at = NOW() WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id, status, result)
	return err
}

// IncrementRetry increments retry count
func (s *TaskStore) IncrementRetry(ctx context.Context, id string) error {
	query := `UPDATE broker_tasks SET retry_count = retry_count + 1, updated_at = NOW() WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id)
	return err
}

// UpdateBrokerTaskID updates the broker task ID
func (s *TaskStore) UpdateBrokerTaskID(ctx context.Context, id, brokerTaskID string) error {
	query := `UPDATE broker_tasks SET broker_task_id = $2, updated_at = NOW() WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id, brokerTaskID)
	return err
}

// ListByStatus retrieves tasks by status
func (s *TaskStore) ListByStatus(ctx context.Context, status string) ([]*Task, error) {
	query := `
		SELECT id, name, type, payload, status, result, error_msg, retry_count, max_retries, broker_task_id, created_at, updated_at, completed_at
		FROM broker_tasks WHERE status = $1 ORDER BY created_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var task Task
		var result, errorMsg, brokerTaskID sql.NullString
		var completedAt sql.NullTime

		err := rows.Scan(&task.ID, &task.Name, &task.Type, &task.Payload, &task.Status,
			&result, &errorMsg, &task.RetryCount, &task.MaxRetries, &brokerTaskID,
			&task.CreatedAt, &task.UpdatedAt, &completedAt)
		if err != nil {
			return nil, err
		}

		if result.Valid {
			task.Result = result.String
		}
		if errorMsg.Valid {
			task.ErrorMsg = errorMsg.String
		}
		if brokerTaskID.Valid {
			task.BrokerTaskID = brokerTaskID.String
		}
		if completedAt.Valid {
			task.CompletedAt = &completedAt.Time
		}
		tasks = append(tasks, &task)
	}
	return tasks, nil
}

// Close closes the database connection
func (s *TaskStore) Close() error {
	return s.db.Close()
}
