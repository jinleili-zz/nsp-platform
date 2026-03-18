package taskqueue

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- in-memory mock Store for unit tests ---

type mockStore struct {
	mu        sync.Mutex
	workflows map[string]*Workflow
	steps     map[string]*StepTask
}

func newMockStore() *mockStore {
	return &mockStore{
		workflows: make(map[string]*Workflow),
		steps:     make(map[string]*StepTask),
	}
}

func (m *mockStore) Migrate(_ context.Context) error { return nil }

func (m *mockStore) CreateWorkflow(_ context.Context, wf *Workflow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.workflows[wf.ID] = wf
	return nil
}

func (m *mockStore) GetWorkflow(_ context.Context, id string) (*Workflow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wf, ok := m.workflows[id]
	if !ok {
		return nil, nil
	}
	copy := *wf
	return &copy, nil
}

func (m *mockStore) GetWorkflowsByResourceID(_ context.Context, resourceType, resourceID string) ([]*Workflow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*Workflow
	for _, wf := range m.workflows {
		if wf.ResourceType == resourceType && wf.ResourceID == resourceID {
			copy := *wf
			result = append(result, &copy)
		}
	}
	return result, nil
}

func (m *mockStore) UpdateWorkflowStatus(_ context.Context, id string, status WorkflowStatus, errorMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	wf, ok := m.workflows[id]
	if !ok {
		return fmt.Errorf("workflow not found: %s", id)
	}
	wf.Status = status
	wf.ErrorMessage = errorMsg
	wf.UpdatedAt = time.Now()
	return nil
}

func (m *mockStore) IncrementCompletedSteps(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	wf, ok := m.workflows[id]
	if !ok {
		return fmt.Errorf("workflow not found")
	}
	wf.CompletedSteps++
	return nil
}

func (m *mockStore) IncrementFailedSteps(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	wf, ok := m.workflows[id]
	if !ok {
		return fmt.Errorf("workflow not found")
	}
	wf.FailedSteps++
	return nil
}

func (m *mockStore) TryCompleteWorkflow(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wf, ok := m.workflows[id]
	if !ok {
		return false, nil
	}
	if wf.Status == WorkflowStatusRunning && wf.CompletedSteps == wf.TotalSteps && wf.FailedSteps == 0 {
		wf.Status = WorkflowStatusSucceeded
		return true, nil
	}
	return false, nil
}

func (m *mockStore) BatchCreateSteps(_ context.Context, steps []*StepTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range steps {
		copy := *s
		m.steps[s.ID] = &copy
	}
	return nil
}

func (m *mockStore) GetStep(_ context.Context, id string) (*StepTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.steps[id]
	if !ok {
		return nil, nil
	}
	copy := *s
	return &copy, nil
}

func (m *mockStore) GetStepsByWorkflow(_ context.Context, workflowID string) ([]*StepTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*StepTask
	for _, s := range m.steps {
		if s.WorkflowID == workflowID {
			copy := *s
			result = append(result, &copy)
		}
	}
	return result, nil
}

func (m *mockStore) GetNextPendingStep(_ context.Context, workflowID string) (*StepTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *StepTask
	for _, s := range m.steps {
		if s.WorkflowID == workflowID && s.Status == StepStatusPending {
			if best == nil || s.StepOrder < best.StepOrder {
				copy := *s
				best = &copy
			}
		}
	}
	return best, nil
}

func (m *mockStore) UpdateStepStatus(_ context.Context, id string, status StepStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.steps[id]
	if !ok {
		return fmt.Errorf("step not found")
	}
	s.Status = status
	return nil
}

func (m *mockStore) UpdateStepResult(_ context.Context, id string, status StepStatus, result string, errorMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.steps[id]
	if !ok {
		return fmt.Errorf("step not found")
	}
	s.Status = status
	s.Result = result
	s.ErrorMessage = errorMsg
	return nil
}

func (m *mockStore) UpdateStepBrokerID(_ context.Context, id string, brokerTaskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.steps[id]
	if !ok {
		return fmt.Errorf("step not found")
	}
	s.BrokerTaskID = brokerTaskID
	return nil
}

func (m *mockStore) GetStepStats(_ context.Context, workflowID string) (*StepStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := &StepStats{}
	for _, s := range m.steps {
		if s.WorkflowID == workflowID {
			stats.Total++
			switch s.Status {
			case StepStatusCompleted:
				stats.Completed++
			case StepStatusFailed:
				stats.Failed++
			}
		}
	}
	stats.Pending = stats.Total - stats.Completed - stats.Failed
	return stats, nil
}

func (m *mockStore) IncrementStepRetryCount(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.steps[id]
	if !ok {
		return fmt.Errorf("step not found")
	}
	s.RetryCount++
	return nil
}

// --- mock Broker ---

type mockBroker struct {
	mu       sync.Mutex
	tasks    []*Task
	idSeq    int
}

func newMockBroker() *mockBroker {
	return &mockBroker{}
}

func (b *mockBroker) Publish(_ context.Context, task *Task) (*TaskInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.idSeq++
	b.tasks = append(b.tasks, task)
	return &TaskInfo{
		BrokerTaskID: fmt.Sprintf("broker-%d", b.idSeq),
		Queue:        task.Queue,
	}, nil
}

func (b *mockBroker) Close() error { return nil }

func (b *mockBroker) publishedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.tasks)
}

// --- helper to build an Engine with hooks ---

type hookTracker struct {
	mu                     sync.Mutex
	stepCompleteCount      int
	stepFailedCount        int
	workflowCompleteCount  int
	workflowFailedCount    int
	lastWorkflow           *Workflow
	lastStep               *StepTask
	lastErrMsg             string
}

func (h *hookTracker) hooks() *WorkflowHooks {
	return &WorkflowHooks{
		OnStepComplete: func(_ context.Context, wf *Workflow, step *StepTask) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.stepCompleteCount++
			h.lastWorkflow = wf
			h.lastStep = step
			return nil
		},
		OnStepFailed: func(_ context.Context, wf *Workflow, step *StepTask, errMsg string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.stepFailedCount++
			h.lastWorkflow = wf
			h.lastStep = step
			h.lastErrMsg = errMsg
			return nil
		},
		OnWorkflowComplete: func(_ context.Context, wf *Workflow) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.workflowCompleteCount++
			h.lastWorkflow = wf
			return nil
		},
		OnWorkflowFailed: func(_ context.Context, wf *Workflow, errMsg string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.workflowFailedCount++
			h.lastWorkflow = wf
			h.lastErrMsg = errMsg
			return nil
		},
	}
}

func newTestEngine(hooks *WorkflowHooks) (*Engine, *mockStore, *mockBroker) {
	store := newMockStore()
	broker := newMockBroker()
	cfg := &Config{
		CallbackQueue: "test_callbacks",
		Hooks:         hooks,
	}
	engine := NewEngineWithStore(cfg, broker, store)
	return engine, store, broker
}

// ===== Tests =====

func TestSubmitWorkflow(t *testing.T) {
	tracker := &hookTracker{}
	engine, store, broker := newTestEngine(tracker.hooks())
	ctx := context.Background()

	def := &WorkflowDefinition{
		Name:         "test_wf",
		ResourceType: "vpc",
		ResourceID:   "vpc-001",
		Steps: []StepDefinition{
			{TaskType: "step_a", TaskName: "Step A", QueueTag: "switch"},
			{TaskType: "step_b", TaskName: "Step B", QueueTag: "firewall"},
		},
	}

	wfID, err := engine.SubmitWorkflow(ctx, def)
	if err != nil {
		t.Fatalf("SubmitWorkflow failed: %v", err)
	}
	if wfID == "" {
		t.Fatal("expected non-empty workflow ID")
	}

	// Verify workflow created
	wf, _ := store.GetWorkflow(ctx, wfID)
	if wf == nil {
		t.Fatal("workflow not found in store")
	}
	if wf.Status != WorkflowStatusRunning {
		t.Errorf("expected running, got %s", wf.Status)
	}
	if wf.TotalSteps != 2 {
		t.Errorf("expected 2 total steps, got %d", wf.TotalSteps)
	}

	// Verify first step enqueued
	if broker.publishedCount() != 1 {
		t.Errorf("expected 1 published task, got %d", broker.publishedCount())
	}
}

func TestHandleCallback_StepSuccess_TriggersHook(t *testing.T) {
	tracker := &hookTracker{}
	engine, store, _ := newTestEngine(tracker.hooks())
	ctx := context.Background()

	// Setup: workflow with 2 steps
	def := &WorkflowDefinition{
		Name:         "test_wf",
		ResourceType: "vpc",
		ResourceID:   "vpc-001",
		Steps: []StepDefinition{
			{TaskType: "step_a", TaskName: "Step A", QueueTag: "switch"},
			{TaskType: "step_b", TaskName: "Step B", QueueTag: "switch"},
		},
	}
	wfID, _ := engine.SubmitWorkflow(ctx, def)

	// Find step A
	steps, _ := store.GetStepsByWorkflow(ctx, wfID)
	var stepA *StepTask
	for _, s := range steps {
		if s.TaskType == "step_a" {
			stepA = s
			break
		}
	}
	if stepA == nil {
		t.Fatal("step_a not found")
	}

	// Callback: step A completed
	err := engine.HandleCallback(ctx, &CallbackPayload{
		TaskID: stepA.ID,
		Status: "completed",
		Result: map[string]string{"ok": "true"},
	})
	if err != nil {
		t.Fatalf("HandleCallback failed: %v", err)
	}

	// OnStepComplete should have been called once
	tracker.mu.Lock()
	if tracker.stepCompleteCount != 1 {
		t.Errorf("expected OnStepComplete called 1 time, got %d", tracker.stepCompleteCount)
	}
	if tracker.lastWorkflow == nil || tracker.lastWorkflow.ResourceID != "vpc-001" {
		t.Error("expected OnStepComplete to receive correct workflow")
	}
	if tracker.lastStep == nil || tracker.lastStep.TaskType != "step_a" {
		t.Error("expected OnStepComplete to receive step_a")
	}
	// Workflow should NOT be complete yet
	if tracker.workflowCompleteCount != 0 {
		t.Errorf("expected OnWorkflowComplete not called yet, got %d", tracker.workflowCompleteCount)
	}
	tracker.mu.Unlock()
}

func TestHandleCallback_AllStepsComplete_TriggersWorkflowHook(t *testing.T) {
	tracker := &hookTracker{}
	engine, store, _ := newTestEngine(tracker.hooks())
	ctx := context.Background()

	def := &WorkflowDefinition{
		Name:         "test_wf",
		ResourceType: "vpc",
		ResourceID:   "vpc-002",
		Steps: []StepDefinition{
			{TaskType: "step_a", TaskName: "Step A", QueueTag: "switch"},
		},
	}
	wfID, _ := engine.SubmitWorkflow(ctx, def)

	steps, _ := store.GetStepsByWorkflow(ctx, wfID)
	stepA := steps[0]

	err := engine.HandleCallback(ctx, &CallbackPayload{
		TaskID: stepA.ID,
		Status: "completed",
	})
	if err != nil {
		t.Fatalf("HandleCallback failed: %v", err)
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.stepCompleteCount != 1 {
		t.Errorf("expected OnStepComplete 1, got %d", tracker.stepCompleteCount)
	}
	if tracker.workflowCompleteCount != 1 {
		t.Errorf("expected OnWorkflowComplete 1, got %d", tracker.workflowCompleteCount)
	}
	if tracker.lastWorkflow == nil || tracker.lastWorkflow.ResourceID != "vpc-002" {
		t.Error("expected OnWorkflowComplete to receive correct workflow")
	}

	// Verify workflow status in store
	wf, _ := store.GetWorkflow(ctx, wfID)
	if wf.Status != WorkflowStatusSucceeded {
		t.Errorf("expected succeeded, got %s", wf.Status)
	}
}

func TestHandleCallback_StepFailure_RetriesExhausted_TriggersHooks(t *testing.T) {
	tracker := &hookTracker{}
	engine, store, _ := newTestEngine(tracker.hooks())
	ctx := context.Background()

	def := &WorkflowDefinition{
		Name:         "test_wf",
		ResourceType: "vpc",
		ResourceID:   "vpc-003",
		Steps: []StepDefinition{
			{TaskType: "step_a", TaskName: "Step A", QueueTag: "switch", MaxRetries: 1},
		},
	}
	wfID, _ := engine.SubmitWorkflow(ctx, def)

	steps, _ := store.GetStepsByWorkflow(ctx, wfID)
	stepA := steps[0]

	// Simulate that the step already used its one allowed retry
	store.mu.Lock()
	store.steps[stepA.ID].RetryCount = 1
	store.mu.Unlock()

	err := engine.HandleCallback(ctx, &CallbackPayload{
		TaskID:       stepA.ID,
		Status:       "failed",
		ErrorMessage: "device unreachable",
	})
	if err != nil {
		t.Fatalf("HandleCallback failed: %v", err)
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.stepFailedCount != 1 {
		t.Errorf("expected OnStepFailed 1, got %d", tracker.stepFailedCount)
	}
	if tracker.workflowFailedCount != 1 {
		t.Errorf("expected OnWorkflowFailed 1, got %d", tracker.workflowFailedCount)
	}
	if tracker.lastErrMsg != "device unreachable" {
		t.Errorf("expected error message 'device unreachable', got '%s'", tracker.lastErrMsg)
	}

	wf, _ := store.GetWorkflow(ctx, wfID)
	if wf.Status != WorkflowStatusFailed {
		t.Errorf("expected failed, got %s", wf.Status)
	}
}

func TestHandleCallback_StepFailure_AutoRetry(t *testing.T) {
	tracker := &hookTracker{}
	engine, store, broker := newTestEngine(tracker.hooks())
	ctx := context.Background()

	def := &WorkflowDefinition{
		Name:         "test_wf",
		ResourceType: "vpc",
		ResourceID:   "vpc-004",
		Steps: []StepDefinition{
			{TaskType: "step_a", TaskName: "Step A", QueueTag: "switch", MaxRetries: 2},
		},
	}
	wfID, _ := engine.SubmitWorkflow(ctx, def)

	steps, _ := store.GetStepsByWorkflow(ctx, wfID)
	stepA := steps[0]

	publishedBefore := broker.publishedCount()

	// First failure — should auto-retry, NOT call failure hooks
	err := engine.HandleCallback(ctx, &CallbackPayload{
		TaskID:       stepA.ID,
		Status:       "failed",
		ErrorMessage: "timeout",
	})
	if err != nil {
		t.Fatalf("HandleCallback failed: %v", err)
	}

	tracker.mu.Lock()
	if tracker.stepFailedCount != 0 {
		t.Errorf("expected OnStepFailed 0 (retry should happen), got %d", tracker.stepFailedCount)
	}
	if tracker.workflowFailedCount != 0 {
		t.Errorf("expected OnWorkflowFailed 0, got %d", tracker.workflowFailedCount)
	}
	tracker.mu.Unlock()

	// Step should be re-enqueued
	if broker.publishedCount() != publishedBefore+1 {
		t.Errorf("expected step to be re-enqueued")
	}

	// Verify workflow still running
	wf, _ := store.GetWorkflow(ctx, wfID)
	if wf.Status != WorkflowStatusRunning {
		t.Errorf("expected running during retry, got %s", wf.Status)
	}
}

func TestNilHooks_NoError(t *testing.T) {
	engine, store, _ := newTestEngine(nil)
	ctx := context.Background()

	def := &WorkflowDefinition{
		Name:         "test_wf",
		ResourceType: "vpc",
		ResourceID:   "vpc-005",
		Steps: []StepDefinition{
			{TaskType: "step_a", TaskName: "Step A", QueueTag: "switch"},
		},
	}
	wfID, _ := engine.SubmitWorkflow(ctx, def)

	steps, _ := store.GetStepsByWorkflow(ctx, wfID)

	// Should work without panic even with nil hooks
	err := engine.HandleCallback(ctx, &CallbackPayload{
		TaskID: steps[0].ID,
		Status: "completed",
	})
	if err != nil {
		t.Fatalf("HandleCallback with nil hooks failed: %v", err)
	}

	wf, _ := store.GetWorkflow(ctx, wfID)
	if wf.Status != WorkflowStatusSucceeded {
		t.Errorf("expected succeeded, got %s", wf.Status)
	}
}

func TestGetWorkflowsByResourceID(t *testing.T) {
	store := newMockStore()
	ctx := context.Background()

	now := time.Now()
	store.CreateWorkflow(ctx, &Workflow{ID: "wf-1", ResourceType: "vpc", ResourceID: "vpc-100", Status: WorkflowStatusSucceeded, CreatedAt: now})
	store.CreateWorkflow(ctx, &Workflow{ID: "wf-2", ResourceType: "vpc", ResourceID: "vpc-100", Status: WorkflowStatusRunning, CreatedAt: now})
	store.CreateWorkflow(ctx, &Workflow{ID: "wf-3", ResourceType: "subnet", ResourceID: "sub-200", Status: WorkflowStatusRunning, CreatedAt: now})

	// Query vpc-100
	results, err := store.GetWorkflowsByResourceID(ctx, "vpc", "vpc-100")
	if err != nil {
		t.Fatalf("GetWorkflowsByResourceID failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 workflows for vpc-100, got %d", len(results))
	}

	// Query subnet
	results, err = store.GetWorkflowsByResourceID(ctx, "subnet", "sub-200")
	if err != nil {
		t.Fatalf("GetWorkflowsByResourceID failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 workflow for sub-200, got %d", len(results))
	}

	// Query non-existent
	results, err = store.GetWorkflowsByResourceID(ctx, "vpc", "vpc-999")
	if err != nil {
		t.Fatalf("GetWorkflowsByResourceID failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 workflows for vpc-999, got %d", len(results))
	}
}

func TestHookError_NonBlocking(t *testing.T) {
	hookCalled := false
	hooks := &WorkflowHooks{
		OnStepComplete: func(_ context.Context, _ *Workflow, _ *StepTask) error {
			hookCalled = true
			return fmt.Errorf("hook exploded")
		},
	}
	engine, store, broker := newTestEngine(hooks)
	ctx := context.Background()

	def := &WorkflowDefinition{
		Name:         "test_wf",
		ResourceType: "vpc",
		ResourceID:   "vpc-006",
		Steps: []StepDefinition{
			{TaskType: "step_a", TaskName: "Step A", QueueTag: "switch"},
			{TaskType: "step_b", TaskName: "Step B", QueueTag: "switch"},
		},
	}
	wfID, _ := engine.SubmitWorkflow(ctx, def)
	steps, _ := store.GetStepsByWorkflow(ctx, wfID)

	var stepA *StepTask
	for _, s := range steps {
		if s.TaskType == "step_a" {
			stepA = s
			break
		}
	}

	publishedBefore := broker.publishedCount()

	// Hook error should NOT block workflow progression
	err := engine.HandleCallback(ctx, &CallbackPayload{
		TaskID: stepA.ID,
		Status: "completed",
	})
	if err != nil {
		t.Fatalf("expected no error (hook errors are non-blocking), got: %v", err)
	}
	if !hookCalled {
		t.Fatal("expected hook to be called")
	}

	// Verify step_b was still enqueued despite hook error
	if broker.publishedCount() <= publishedBefore {
		t.Error("expected step_b to be enqueued despite hook error")
	}
}
