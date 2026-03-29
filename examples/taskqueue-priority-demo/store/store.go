package store

import (
	"context"
	"errors"
	"log"
	"os"
	"sort"
	"sync"
	"time"
)

// MustRedisAddr returns the Redis address from the REDIS_ADDR env var, or fatals if unset.
func MustRedisAddr() string {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		log.Fatal("REDIS_ADDR environment variable is required")
	}
	return addr
}

const (
	TaskQueuePrefix = "nsp:taskqueue"
	TaskQueueHigh   = "nsp:taskqueue:high"
	TaskQueueMiddle = "nsp:taskqueue:middle"
	TaskQueueLow    = "nsp:taskqueue:low"

	CallbackQueueOrder  = "nsp:taskqueue:callback:order"  // 订单类任务回调队列
	CallbackQueueNotify = "nsp:taskqueue:callback:notify" // 通知类任务回调队列

	DefaultQueue = TaskQueueMiddle
)

const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
)

// Task represents a task tracked by the priority demo.
type Task struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Type         string     `json:"type"`
	Queue        string     `json:"queue"`
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

// TaskStore keeps demo task state in memory so the example has no database dependency.
type TaskStore struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

// NewTaskStore creates a new in-memory task store.
func NewTaskStore() (*TaskStore, error) {
	return &TaskStore{tasks: make(map[string]*Task)}, nil
}

// Migrate is a no-op for the in-memory demo store.
func (s *TaskStore) Migrate(context.Context) error {
	return nil
}

// Create inserts a new task.
func (s *TaskStore) Create(_ context.Context, task *Task) error {
	if task == nil {
		return errors.New("task is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.ID] = cloneTask(task)
	return nil
}

// GetByID retrieves a task by ID.
func (s *TaskStore) GetByID(_ context.Context, id string) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[id]
	if !ok {
		return nil, nil
	}
	return cloneTask(task), nil
}

// UpdateStatus updates task status.
func (s *TaskStore) UpdateStatus(_ context.Context, id, status, errorMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		return nil
	}
	task.Status = status
	task.ErrorMsg = errorMsg
	task.UpdatedAt = time.Now()
	return nil
}

// UpdateResult updates task result when completed.
func (s *TaskStore) UpdateResult(_ context.Context, id, status, result string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		return nil
	}
	now := time.Now()
	task.Status = status
	task.Result = result
	task.UpdatedAt = now
	task.CompletedAt = &now
	return nil
}

// IncrementRetry increments retry count.
func (s *TaskStore) IncrementRetry(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		return nil
	}
	task.RetryCount++
	task.UpdatedAt = time.Now()
	return nil
}

// UpdateBrokerTaskID updates the broker task ID.
func (s *TaskStore) UpdateBrokerTaskID(_ context.Context, id, brokerTaskID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[id]
	if !ok {
		return nil
	}
	task.BrokerTaskID = brokerTaskID
	task.UpdatedAt = time.Now()
	return nil
}

// ListByStatus retrieves tasks by status.
func (s *TaskStore) ListByStatus(_ context.Context, status string) ([]*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var tasks []*Task
	for _, task := range s.tasks {
		if task.Status == status {
			tasks = append(tasks, cloneTask(task))
		}
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})
	return tasks, nil
}

// Close closes the store.
func (s *TaskStore) Close() error {
	return nil
}

func cloneTask(task *Task) *Task {
	if task == nil {
		return nil
	}

	cloned := *task
	if task.CompletedAt != nil {
		completedAt := *task.CompletedAt
		cloned.CompletedAt = &completedAt
	}
	return &cloned
}
