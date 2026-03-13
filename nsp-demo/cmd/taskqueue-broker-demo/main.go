// TaskQueue Broker Demo
// This example demonstrates a custom task queue implementation using broker only,
// without relying on Engine's workflow orchestration. The task state is managed
// in PostgreSQL while message delivery uses asynq broker.
//
// Prerequisites:
//   - PostgreSQL running on localhost:5432
//   - Redis running on localhost:6379
//   - Database: CREATE DATABASE taskqueue_broker;

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	_ "github.com/lib/pq"

	"github.com/paic/nsp-common/pkg/taskqueue"
	"github.com/paic/nsp-common/pkg/taskqueue/asynqbroker"
)

const (
	redisAddr  = "127.0.0.1:6379"
	pgDSN      = "postgres://admin:admin123@127.0.0.1:5432/taskqueue_broker?sslmode=disable"
	taskQueue  = "broker_tasks"
)

// Task represents a task stored in PostgreSQL (custom implementation)
type Task struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Type        string     `json:"type"`
	Payload     string     `json:"payload"`
	Status      string     `json:"status"`
	Result      string     `json:"result,omitempty"`
	ErrorMsg    string     `json:"error_msg,omitempty"`
	RetryCount  int        `json:"retry_count"`
	MaxRetries  int        `json:"max_retries"`
	BrokerTaskID string   `json:"broker_task_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
)

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

// TaskManager manages task lifecycle using broker for message delivery
type TaskManager struct {
	store  *TaskStore
	broker *asynqbroker.Broker
}

// NewTaskManager creates a new TaskManager
func NewTaskManager(store *TaskStore, broker *asynqbroker.Broker) *TaskManager {
	return &TaskManager{
		store:  store,
		broker: broker,
	}
}

// SubmitTask submits a new task: stores to DB + publishes to broker
func (m *TaskManager) SubmitTask(ctx context.Context, name, taskType, payload string, maxRetries int) (string, error) {
	taskID := uuid.New().String()
	now := time.Now()

	task := &Task{
		ID:         taskID,
		Name:       name,
		Type:       taskType,
		Payload:    payload,
		Status:     TaskStatusPending,
		MaxRetries: maxRetries,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Store to database first
	if err := m.store.Create(ctx, task); err != nil {
		return "", fmt.Errorf("failed to create task: %w", err)
	}

	// Publish to broker - use same format as workflow demo for worker compatibility
	taskPayload := map[string]interface{}{
		"task_id":     taskID,
		"resource_id": "",
		"task_params": payload,
	}
	payloadData, _ := json.Marshal(taskPayload)

	asynqTask := &taskqueue.Task{
		Type:    taskType,
		Payload: payloadData,
		Queue:   taskQueue,
	}

	info, err := m.broker.Publish(ctx, asynqTask)
	if err != nil {
		// Mark as failed if publish fails
		m.store.UpdateStatus(ctx, taskID, TaskStatusFailed, err.Error())
		return "", fmt.Errorf("failed to publish task: %w", err)
	}

	// Update broker task ID
	m.store.UpdateBrokerTaskID(ctx, taskID, info.BrokerTaskID)
	m.store.UpdateStatus(ctx, taskID, TaskStatusRunning, "")

	log.Printf("[Manager] Task submitted: id=%s, type=%s, broker_id=%s", taskID, taskType, info.BrokerTaskID)
	return taskID, nil
}

// HandleCallback processes callback from worker (custom implementation)
func (m *TaskManager) HandleCallback(ctx context.Context, cb *taskqueue.CallbackPayload) error {
	log.Printf("[Manager] Callback received: task_id=%s, status=%s", cb.TaskID, cb.Status)

	task, err := m.store.GetByID(ctx, cb.TaskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %w", err)
	}
	if task == nil {
		return fmt.Errorf("task not found: %s", cb.TaskID)
	}

	switch cb.Status {
	case "completed":
		resultJSON, _ := json.Marshal(cb.Result)
		return m.store.UpdateResult(ctx, cb.TaskID, TaskStatusCompleted, string(resultJSON))

	case "failed":
		// Check if we should retry
		if task.RetryCount < task.MaxRetries {
			// Increment retry and re-enqueue
			m.store.IncrementRetry(ctx, cb.TaskID)
			m.store.UpdateStatus(ctx, cb.TaskID, TaskStatusPending, cb.ErrorMessage)

			// Re-publish to broker for retry
			taskPayload := map[string]interface{}{
				"task_id":  task.ID,
				"task_name": task.Name,
				"payload":  task.Payload,
			}
			payloadData, _ := json.Marshal(taskPayload)

			asynqTask := &taskqueue.Task{
				Type:    task.Type,
				Payload: payloadData,
				Queue:   taskQueue,
			}

			info, err := m.broker.Publish(ctx, asynqTask)
			if err != nil {
				m.store.UpdateStatus(ctx, cb.TaskID, TaskStatusFailed, err.Error())
				return fmt.Errorf("failed to re-publish task: %w", err)
			}

			m.store.UpdateBrokerTaskID(ctx, cb.TaskID, info.BrokerTaskID)
			m.store.UpdateStatus(ctx, cb.TaskID, TaskStatusRunning, "")
			log.Printf("[Manager] Task re-queued for retry: id=%s, retry=%d/%d", cb.TaskID, task.RetryCount+1, task.MaxRetries)
			return nil
		}

		// No more retries
		return m.store.UpdateStatus(ctx, cb.TaskID, TaskStatusFailed, cb.ErrorMessage)

	default:
		return fmt.Errorf("unknown callback status: %s", cb.Status)
	}
}

// QueryTask returns task status
func (m *TaskManager) QueryTask(ctx context.Context, taskID string) (*Task, error) {
	return m.store.GetByID(ctx, taskID)
}

// ListTasks returns all tasks with given status
func (m *TaskManager) ListTasks(ctx context.Context, status string) ([]*Task, error) {
	return m.store.ListByStatus(ctx, status)
}

// CallbackSender wraps broker for sending callbacks
type CallbackSender struct {
	broker *asynqbroker.Broker
	queue  string
}

// NewCallbackSender creates a new CallbackSender
func NewCallbackSender(broker *asynqbroker.Broker, queue string) *CallbackSender {
	return &CallbackSender{broker: broker, queue: queue}
}

// Success sends a success callback
func (s *CallbackSender) Success(ctx context.Context, taskID string, result interface{}) error {
	return s.send(ctx, taskID, "completed", result, "")
}

// Fail sends a failure callback
func (s *CallbackSender) Fail(ctx context.Context, taskID string, errMsg string) error {
	return s.send(ctx, taskID, "failed", nil, errMsg)
}

func (s *CallbackSender) send(ctx context.Context, taskID, status string, result interface{}, errorMsg string) error {
	cb := &taskqueue.CallbackPayload{
		TaskID:       taskID,
		Status:       status,
		Result:       result,
		ErrorMessage: errorMsg,
	}
	data, _ := json.Marshal(cb)

	task := &taskqueue.Task{
		Type:    "broker_task_callback",
		Payload: data,
		Queue:   s.queue,
	}

	_, err := s.broker.Publish(ctx, task)
	if err != nil {
		return fmt.Errorf("failed to publish callback: %w", err)
	}

	log.Printf("[Callback] Sent: task_id=%s, status=%s", taskID, status)
	return nil
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("========================================")
	log.Println("TaskQueue Broker Demo (Custom Implementation)")
	log.Println("========================================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ========================================
	// Step 1: Setup Broker
	// ========================================
	redisOpt := asynq.RedisClientOpt{Addr: redisAddr}
	broker := asynqbroker.NewBroker(redisOpt)
	defer broker.Close()
	log.Println("[Setup] Broker created")

	// ========================================
	// Step 2: Setup Task Store (PostgreSQL)
	// ========================================
	store, err := NewTaskStore(pgDSN)
	if err != nil {
		log.Fatalf("[Setup] Failed to connect to database: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		log.Fatalf("[Setup] Failed to migrate: %v", err)
	}
	log.Println("[Setup] Database migrated")

	// ========================================
	// Step 3: Setup Task Manager
	// ========================================
	manager := NewTaskManager(store, broker)
	callbackQueue := "broker_callbacks"
	callbackSender := NewCallbackSender(broker, callbackQueue)

	// ========================================
	// Step 4: Setup Consumers
	// ========================================

	// Worker consumer - handles actual tasks
	workerConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: 5,
		Queues:       map[string]int{taskQueue: 10},
	})

	// Register task handlers
	workerConsumer.Handle("send_email", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)

		log.Printf("[Worker] Sending email to: %v (task_id=%s)", params["email"], payload.TaskID)
		time.Sleep(500 * time.Millisecond)

		result := map[string]interface{}{
			"message": "Email sent successfully",
			"email":   params["email"],
		}

		if err := callbackSender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Worker] Email sent to: %v", params["email"])
		return &taskqueue.TaskResult{Data: result}, nil
	})

	workerConsumer.Handle("create_record", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		var params map[string]interface{}
		json.Unmarshal(payload.Params, &params)

		log.Printf("[Worker] Creating record: %v (task_id=%s)", params["record_type"], payload.TaskID)
		time.Sleep(300 * time.Millisecond)

		result := map[string]interface{}{
			"message":   "Record created",
			"record_id": "REC-12345",
		}

		if err := callbackSender.Success(ctx, payload.TaskID, result); err != nil {
			return nil, err
		}
		log.Printf("[Worker] Record created: %v", params["record_type"])
		return &taskqueue.TaskResult{Data: result}, nil
	})

	// Handler for always_fail task (to test retry logic)
	workerConsumer.Handle("always_fail", func(ctx context.Context, payload *taskqueue.TaskPayload) (*taskqueue.TaskResult, error) {
		log.Printf("[Worker] Always fail task executed (task_id=%s)", payload.TaskID)
		// Always return error to trigger retry
		if err := callbackSender.Fail(ctx, payload.TaskID, "Simulated failure for retry test"); err != nil {
			return nil, err
		}
		return &taskqueue.TaskResult{Data: nil}, nil
	})

	// Callback consumer - handles callbacks
	callbackConsumer := asynqbroker.NewConsumer(redisOpt, asynqbroker.ConsumerConfig{
		Concurrency: 2,
		Queues:      map[string]int{callbackQueue: 10},
	})

	callbackConsumer.HandleRaw("broker_task_callback", func(ctx context.Context, t *asynq.Task) error {
		var cb taskqueue.CallbackPayload
		if err := json.Unmarshal(t.Payload(), &cb); err != nil {
			return fmt.Errorf("failed to unmarshal callback: %w", err)
		}
		return manager.HandleCallback(ctx, &cb)
	})

	// Start consumers
	go workerConsumer.Start(ctx)
	go callbackConsumer.Start(ctx)
	log.Println("[Setup] Consumers started")

	time.Sleep(2 * time.Second)

	// ========================================
	// Step 5: Submit Tasks (Custom Implementation)
	// ========================================
	log.Println("========================================")
	log.Println("[Demo] Submitting tasks (broker-only mode)")
	log.Println("========================================")

	// Submit task 1: create_record
	recordParams, _ := json.Marshal(map[string]interface{}{
		"record_type": "user_registration",
		"user_id":     "U-001",
	})
	taskID1, err := manager.SubmitTask(ctx, "Create User Record", "create_record", string(recordParams), 3)
	if err != nil {
		log.Fatalf("[Demo] Failed to submit task 1: %v", err)
	}
	log.Printf("[Demo] Task 1 submitted: id=%s", taskID1)

	// Submit task 2: send_email
	emailParams, _ := json.Marshal(map[string]interface{}{
		"email":   "user@example.com",
		"subject": "Welcome!",
	})
	taskID2, err := manager.SubmitTask(ctx, "Send Welcome Email", "send_email", string(emailParams), 3)
	if err != nil {
		log.Fatalf("[Demo] Failed to submit task 2: %v", err)
	}
	log.Printf("[Demo] Task 2 submitted: id=%s", taskID2)

	// ========================================
	// Step 6: Poll for Completion
	// ========================================
	log.Println("[Demo] Polling task status...")

	allCompleted := false
	for i := 0; i < 20; i++ {
		time.Sleep(1 * time.Second)

		task1, _ := manager.QueryTask(ctx, taskID1)
		task2, _ := manager.QueryTask(ctx, taskID2)

		log.Printf("[Demo] Task1 (%s): %s", task1.Type, task1.Status)
		log.Printf("[Demo] Task2 (%s): %s", task2.Type, task2.Status)

		if task1.Status == TaskStatusCompleted && task2.Status == TaskStatusCompleted {
			allCompleted = true
			break
		}

		if task1.Status == TaskStatusFailed || task2.Status == TaskStatusFailed {
			log.Printf("[Demo] Task failed!")
			break
		}
	}

	if allCompleted {
		log.Println("========================================")
		log.Println("[Demo] ✅ All Tasks SUCCEEDED!")
		log.Println("========================================")
	}

	// ========================================
	// Step 7: Demonstrate Retry Logic
	// ========================================
	log.Println("========================================")
	log.Println("[Demo] Testing retry logic")
	log.Println("========================================")

	// Submit a task that will fail
	failParams, _ := json.Marshal(map[string]interface{}{
		"fail": true,
	})
	failTaskID, err := manager.SubmitTask(ctx, "Failing Task", "always_fail", string(failParams), 2)
	if err != nil {
		log.Printf("[Demo] Failed to submit failing task: %v", err)
	} else {
		log.Printf("[Demo] Failing task submitted: id=%s", failTaskID)

		// Wait for retries
		time.Sleep(8 * time.Second)

		failTask, _ := manager.QueryTask(ctx, failTaskID)
		log.Printf("[Demo] Failing task final status: %s (retries=%d/%d)",
			failTask.Status, failTask.RetryCount, failTask.MaxRetries)
	}

	// ========================================
	// Step 8: Graceful Shutdown
	// ========================================
	log.Println("[Demo] Press Ctrl+C to exit...")
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-quit:
		log.Println("[Demo] Shutdown signal received")
	case <-time.After(5 * time.Second):
		log.Println("[Demo] Auto-exit after 5 seconds")
	}

	workerConsumer.Stop()
	callbackConsumer.Stop()
	cancel()

	log.Println("[Demo] Done.")
}
