package asynqbroker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
)

type noopAsynqLogger struct{}

func (noopAsynqLogger) Debug(...interface{}) {}
func (noopAsynqLogger) Info(...interface{})  {}
func (noopAsynqLogger) Warn(...interface{})  {}
func (noopAsynqLogger) Error(...interface{}) {}
func (noopAsynqLogger) Fatal(...interface{}) {}

func newMiniredis(t *testing.T) (*miniredis.Miniredis, asynq.RedisClientOpt) {
	t.Helper()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(mr.Close)

	return mr, asynq.RedisClientOpt{Addr: mr.Addr()}
}

func newAsynqClient(t *testing.T, opt asynq.RedisConnOpt) *asynq.Client {
	t.Helper()

	client := asynq.NewClient(opt)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Fatalf("client.Close() error = %v", err)
		}
	})
	return client
}

func newInspectorForTest(t *testing.T, opt asynq.RedisConnOpt) *Inspector {
	t.Helper()

	inspector := NewInspector(opt)
	t.Cleanup(func() {
		if err := inspector.Close(); err != nil {
			t.Fatalf("inspector.Close() error = %v", err)
		}
	})
	return inspector
}

func waitFor(t *testing.T, desc string, fn func() (bool, error)) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ok, err := fn()
		if ok && err == nil {
			return
		}
		lastErr = err
		time.Sleep(25 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("timed out waiting for %s: last error = %v", desc, lastErr)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func enqueueTask(t *testing.T, client *asynq.Client, queue, taskType string, payload []byte, opts ...asynq.Option) *asynq.TaskInfo {
	t.Helper()

	allOpts := append([]asynq.Option{asynq.Queue(queue)}, opts...)
	info, err := client.Enqueue(asynq.NewTask(taskType, payload), allOpts...)
	if err != nil {
		t.Fatalf("client.Enqueue(%s) error = %v", taskType, err)
	}
	return info
}

func waitForTaskState(t *testing.T, inspector *Inspector, queue, taskID string, state taskqueue.TaskState) *taskqueue.TaskDetail {
	t.Helper()

	var detail *taskqueue.TaskDetail
	waitFor(t, fmt.Sprintf("task %s in queue %s to reach state %s", taskID, queue, state), func() (bool, error) {
		got, err := inspector.GetTaskInfo(context.Background(), queue, taskID)
		if err != nil {
			if errors.Is(err, taskqueue.ErrTaskNotFound) || errors.Is(err, taskqueue.ErrQueueNotFound) {
				return false, nil
			}
			return false, err
		}
		if got.State != state {
			return false, nil
		}
		detail = got
		return true, nil
	})
	return detail
}

func waitForTaskCount(t *testing.T, inspector *Inspector, queue string, state taskqueue.TaskState, want int) []*taskqueue.TaskDetail {
	t.Helper()

	var tasks []*taskqueue.TaskDetail
	waitFor(t, fmt.Sprintf("queue %s state %s count %d", queue, state, want), func() (bool, error) {
		result, err := inspector.ListTasks(context.Background(), queue, state, nil)
		if err != nil {
			return false, err
		}
		if result.Total != want {
			return false, nil
		}
		tasks = result.Tasks
		return true, nil
	})
	return tasks
}

func waitForWorkers(t *testing.T, inspector *Inspector, wantAtLeast int) []*taskqueue.WorkerInfo {
	t.Helper()

	var workers []*taskqueue.WorkerInfo
	waitFor(t, fmt.Sprintf("at least %d workers", wantAtLeast), func() (bool, error) {
		got, err := inspector.ListWorkers(context.Background())
		if err != nil {
			return false, err
		}
		if len(got) < wantAtLeast {
			return false, nil
		}
		workers = got
		return true, nil
	})
	return workers
}

func waitForQueues(t *testing.T, inspector *Inspector, want ...string) {
	t.Helper()

	waitFor(t, fmt.Sprintf("queues %v", want), func() (bool, error) {
		queues, err := inspector.Queues(context.Background())
		if err != nil {
			return false, err
		}
		for _, q := range want {
			if !slices.Contains(queues, q) {
				return false, nil
			}
		}
		return true, nil
	})
}
