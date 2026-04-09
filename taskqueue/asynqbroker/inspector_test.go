package asynqbroker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
)

func TestConvertHelpersAndDefaultBranches(t *testing.T) {
	next := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	completed := time.Now().Add(2 * time.Minute).UTC().Truncate(time.Second)
	payload := wrapWithTrace(context.Background(), []byte(`{"ok":true}`), &taskqueue.ReplySpec{Queue: "reply"}, map[string]string{"tenant": "acme"})

	detail := convertTaskInfo(&asynq.TaskInfo{
		ID:            "task-1",
		Queue:         "default",
		Type:          "email",
		Payload:       payload,
		State:         asynq.TaskStateCompleted,
		MaxRetry:      5,
		Retried:       2,
		LastErr:       "boom",
		NextProcessAt: next,
		CompletedAt:   completed,
	})

	if detail.ID != "task-1" || detail.Type != "email" || detail.Queue != "default" {
		t.Fatalf("convertTaskInfo() basic fields = %#v", detail)
	}
	if detail.State != taskqueue.TaskStateCompleted {
		t.Fatalf("convertTaskInfo().State = %q, want %q", detail.State, taskqueue.TaskStateCompleted)
	}
	if string(detail.Payload) != `{"ok":true}` {
		t.Fatalf("convertTaskInfo().Payload = %s, want unwrapped payload", string(detail.Payload))
	}
	if detail.NextProcessAt == nil || !detail.NextProcessAt.Equal(next) {
		t.Fatalf("convertTaskInfo().NextProcessAt = %v, want %v", detail.NextProcessAt, next)
	}
	if detail.CompletedAt == nil || !detail.CompletedAt.Equal(completed) {
		t.Fatalf("convertTaskInfo().CompletedAt = %v, want %v", detail.CompletedAt, completed)
	}

	stateCases := map[asynq.TaskState]taskqueue.TaskState{
		asynq.TaskStatePending:     taskqueue.TaskStatePending,
		asynq.TaskStateScheduled:   taskqueue.TaskStateScheduled,
		asynq.TaskStateActive:      taskqueue.TaskStateActive,
		asynq.TaskStateRetry:       taskqueue.TaskStateRetry,
		asynq.TaskStateArchived:    taskqueue.TaskStateFailed,
		asynq.TaskStateCompleted:   taskqueue.TaskStateCompleted,
		asynq.TaskStateAggregating: taskqueue.TaskStatePending,
	}
	for input, want := range stateCases {
		if got := convertState(input); got != want {
			t.Fatalf("convertState(%v) = %q, want %q", input, got, want)
		}
	}
	if got := convertState(asynq.TaskState(0)); got != taskqueue.TaskStatePending {
		t.Fatalf("convertState(unknown) = %q, want %q", got, taskqueue.TaskStatePending)
	}

	if !isQueueNotFoundErr(asynq.ErrQueueNotFound) {
		t.Fatal("isQueueNotFoundErr() = false, want true")
	}
	if !isTaskNotFoundErr(asynq.ErrTaskNotFound) {
		t.Fatal("isTaskNotFoundErr() = false, want true")
	}
	if !errors.Is(wrapQueueErr(asynq.ErrQueueNotFound), taskqueue.ErrQueueNotFound) {
		t.Fatalf("wrapQueueErr(queue not found) did not map to taskqueue.ErrQueueNotFound")
	}

	inspector := &Inspector{}
	result, err := inspector.ListTasks(context.Background(), "queue", taskqueue.TaskState("unknown"), nil)
	if err != nil {
		t.Fatalf("ListTasks(default) error = %v", err)
	}
	if result.Total != 0 || len(result.Tasks) != 0 {
		t.Fatalf("ListTasks(default) = %+v, want empty result", result)
	}
	if n, err := inspector.BatchDeleteTasks(context.Background(), "queue", taskqueue.TaskState("unknown")); err != nil || n != 0 {
		t.Fatalf("BatchDeleteTasks(default) = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := inspector.BatchRunTasks(context.Background(), "queue", taskqueue.TaskState("unknown")); err != nil || n != 0 {
		t.Fatalf("BatchRunTasks(default) = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := inspector.BatchArchiveTasks(context.Background(), "queue", taskqueue.TaskState("unknown")); err != nil || n != 0 {
		t.Fatalf("BatchArchiveTasks(default) = (%d, %v), want (0, nil)", n, err)
	}
}

func TestInspectorTaskAndQueueControls(t *testing.T) {
	_, opt := newMiniredis(t)
	client := newAsynqClient(t, opt)
	inspector := newInspectorForTest(t, opt)
	ctx := context.Background()

	deleteInfo := enqueueTask(t, client, "delete-q", "delete", []byte(`{"id":1}`))
	if err := inspector.DeleteTask(ctx, "delete-q", deleteInfo.ID); err != nil {
		t.Fatalf("DeleteTask() error = %v", err)
	}
	if _, err := inspector.GetTaskInfo(ctx, "delete-q", deleteInfo.ID); !errors.Is(err, taskqueue.ErrTaskNotFound) {
		t.Fatalf("GetTaskInfo(deleted) error = %v, want ErrTaskNotFound", err)
	}

	runInfo := enqueueTask(t, client, "run-q", "run", []byte(`{"id":2}`), asynq.ProcessIn(time.Hour))
	if err := inspector.RunTask(ctx, "run-q", runInfo.ID); err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}
	waitForTaskState(t, inspector, "run-q", runInfo.ID, taskqueue.TaskStatePending)

	archiveInfo := enqueueTask(t, client, "archive-q", "archive", []byte(`{"id":3}`))
	if err := inspector.ArchiveTask(ctx, "archive-q", archiveInfo.ID); err != nil {
		t.Fatalf("ArchiveTask() error = %v", err)
	}
	waitForTaskState(t, inspector, "archive-q", archiveInfo.ID, taskqueue.TaskStateFailed)

	for i := 0; i < 2; i++ {
		enqueueTask(t, client, "batch-run-q", "run-batch", []byte(`{}`), asynq.ProcessIn(time.Hour))
		enqueueTask(t, client, "batch-archive-q", "archive-batch", []byte(`{}`))
	}

	n, err := inspector.BatchRunTasks(ctx, "batch-run-q", taskqueue.TaskStateScheduled)
	if err != nil {
		t.Fatalf("BatchRunTasks() error = %v", err)
	}
	if n != 2 {
		t.Fatalf("BatchRunTasks() = %d, want 2", n)
	}
	waitForTaskCount(t, inspector, "batch-run-q", taskqueue.TaskStatePending, 2)

	n, err = inspector.BatchArchiveTasks(ctx, "batch-archive-q", taskqueue.TaskStatePending)
	if err != nil {
		t.Fatalf("BatchArchiveTasks() error = %v", err)
	}
	if n != 2 {
		t.Fatalf("BatchArchiveTasks() = %d, want 2", n)
	}
	waitForTaskCount(t, inspector, "batch-archive-q", taskqueue.TaskStateFailed, 2)

	n, err = inspector.BatchDeleteTasks(ctx, "batch-archive-q", taskqueue.TaskStateFailed)
	if err != nil {
		t.Fatalf("BatchDeleteTasks() error = %v", err)
	}
	if n != 2 {
		t.Fatalf("BatchDeleteTasks() = %d, want 2", n)
	}
	waitForTaskCount(t, inspector, "batch-archive-q", taskqueue.TaskStateFailed, 0)

	enqueueTask(t, client, "pause-q", "pause", []byte(`{}`))
	if err := inspector.PauseQueue(ctx, "pause-q"); err != nil {
		t.Fatalf("PauseQueue() error = %v", err)
	}
	waitFor(t, "queue pause state", func() (bool, error) {
		stats, err := inspector.GetQueueStats(ctx, "pause-q")
		if err != nil {
			return false, err
		}
		return stats.Paused, nil
	})

	if err := inspector.UnpauseQueue(ctx, "pause-q"); err != nil {
		t.Fatalf("UnpauseQueue() error = %v", err)
	}
	waitFor(t, "queue unpause state", func() (bool, error) {
		stats, err := inspector.GetQueueStats(ctx, "pause-q")
		if err != nil {
			return false, err
		}
		return !stats.Paused, nil
	})

	deleteQueueInfo := enqueueTask(t, client, "delete-empty-q", "delete-empty", []byte(`{}`))
	if err := inspector.DeleteTask(ctx, "delete-empty-q", deleteQueueInfo.ID); err != nil {
		t.Fatalf("DeleteTask(delete-empty-q) error = %v", err)
	}
	if err := inspector.DeleteQueue(ctx, "delete-empty-q", false); err != nil {
		t.Fatalf("DeleteQueue(force=false) error = %v", err)
	}
	if _, err := inspector.GetQueueStats(ctx, "delete-empty-q"); err == nil {
		t.Fatal("GetQueueStats(delete-empty-q) error = nil, want non-nil")
	}

	enqueueTask(t, client, "delete-force-q", "delete-force", []byte(`{}`))
	if err := inspector.DeleteQueue(ctx, "delete-force-q", true); err != nil {
		t.Fatalf("DeleteQueue(force=true) error = %v", err)
	}
	if _, err := inspector.GetQueueStats(ctx, "delete-force-q"); err == nil {
		t.Fatal("GetQueueStats(delete-force-q) error = nil, want non-nil")
	}
}

func TestInspectorListTasksAcrossStatesAndCancelActiveTask(t *testing.T) {
	_, opt := newMiniredis(t)
	client := newAsynqClient(t, opt)
	inspector := newInspectorForTest(t, opt)

	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	server := asynq.NewServer(opt, asynq.Config{
		Concurrency:     4,
		Queues:          map[string]int{"completed-q": 1, "retry-q": 1, "failed-q": 1, "active-q": 1},
		ShutdownTimeout: time.Second,
	})
	mux := asynq.NewServeMux()
	mux.HandleFunc("complete", func(context.Context, *asynq.Task) error {
		return nil
	})
	mux.HandleFunc("retry", func(context.Context, *asynq.Task) error {
		return errors.New("retry later")
	})
	mux.HandleFunc("archive", func(context.Context, *asynq.Task) error {
		return errors.New("archive now")
	})
	mux.HandleFunc("block", func(ctx context.Context, task *asynq.Task) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	})
	if err := server.Start(mux); err != nil {
		t.Fatalf("server.Start() error = %v", err)
	}
	t.Cleanup(server.Shutdown)

	pendingInfo := enqueueTask(t, client, "pending-q", "pending", []byte(`{"state":"pending"}`))
	scheduledInfo := enqueueTask(t, client, "scheduled-q", "scheduled", []byte(`{"state":"scheduled"}`), asynq.ProcessIn(time.Hour))
	completedInfo := enqueueTask(t, client, "completed-q", "complete", []byte(`{"state":"completed"}`), asynq.Retention(time.Hour))
	retryInfo := enqueueTask(t, client, "retry-q", "retry", []byte(`{"state":"retry"}`), asynq.MaxRetry(1))
	failedInfo := enqueueTask(t, client, "failed-q", "archive", []byte(`{"state":"failed"}`), asynq.MaxRetry(0))
	activeInfo := enqueueTask(t, client, "active-q", "block", []byte(`{"state":"active"}`), asynq.MaxRetry(0))

	waitForTaskState(t, inspector, "completed-q", completedInfo.ID, taskqueue.TaskStateCompleted)
	waitForTaskState(t, inspector, "retry-q", retryInfo.ID, taskqueue.TaskStateRetry)
	waitForTaskState(t, inspector, "failed-q", failedInfo.ID, taskqueue.TaskStateFailed)
	waitForTaskState(t, inspector, "active-q", activeInfo.ID, taskqueue.TaskStateActive)
	waitForQueues(t, inspector, "pending-q", "scheduled-q", "completed-q", "retry-q", "failed-q", "active-q")

	workers := waitForWorkers(t, inspector, 1)
	if len(workers) == 0 {
		t.Fatal("expected at least one worker")
	}

	cases := []struct {
		queue string
		state taskqueue.TaskState
		id    string
	}{
		{queue: "pending-q", state: taskqueue.TaskStatePending, id: pendingInfo.ID},
		{queue: "scheduled-q", state: taskqueue.TaskStateScheduled, id: scheduledInfo.ID},
		{queue: "completed-q", state: taskqueue.TaskStateCompleted, id: completedInfo.ID},
		{queue: "retry-q", state: taskqueue.TaskStateRetry, id: retryInfo.ID},
		{queue: "failed-q", state: taskqueue.TaskStateFailed, id: failedInfo.ID},
		{queue: "active-q", state: taskqueue.TaskStateActive, id: activeInfo.ID},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.state), func(t *testing.T) {
			result, err := inspector.ListTasks(context.Background(), tc.queue, tc.state, nil)
			if err != nil {
				t.Fatalf("ListTasks(%s, %s) error = %v", tc.queue, tc.state, err)
			}
			if result.Total < 1 {
				t.Fatalf("ListTasks(%s, %s).Total = %d, want >= 1", tc.queue, tc.state, result.Total)
			}
			found := false
			for _, task := range result.Tasks {
				if task.ID == tc.id {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("ListTasks(%s, %s) missing task id %s", tc.queue, tc.state, tc.id)
			}
		})
	}

	if err := inspector.CancelTask(context.Background(), activeInfo.ID); err != nil {
		t.Fatalf("CancelTask() error = %v", err)
	}
	waitForTaskState(t, inspector, "active-q", activeInfo.ID, taskqueue.TaskStateFailed)
}
