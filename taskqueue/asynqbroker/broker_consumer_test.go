package asynqbroker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jinleili-zz/nsp-platform/logger"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/trace"
)

func TestBrokerPublishAndInspectorRoundTrip(t *testing.T) {
	_, opt := newMiniredis(t)
	broker := NewBroker(opt)
	t.Cleanup(func() {
		if err := broker.Close(); err != nil {
			t.Fatalf("broker.Close() error = %v", err)
		}
	})

	inspector := newInspectorForTest(t, opt)

	traceCtx := &trace.TraceContext{TraceID: "trace-broker", SpanId: "span-broker", Sampled: true}
	ctx := trace.ContextWithTrace(context.Background(), traceCtx)
	task := &taskqueue.Task{
		Type:    "send_email",
		Payload: []byte(`{"id":"123"}`),
		Queue:   "emails",
		Reply:   &taskqueue.ReplySpec{Queue: "callback"},
		Metadata: map[string]string{
			"tenant": "acme",
		},
	}

	info, err := broker.Publish(ctx, task)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if info.BrokerTaskID == "" {
		t.Fatalf("expected non-empty broker task id")
	}
	if info.Queue != "emails" {
		t.Fatalf("Publish() queue = %q, want %q", info.Queue, "emails")
	}

	waitForQueues(t, inspector, "emails")

	waitFor(t, "queue stats for emails", func() (bool, error) {
		stats, err := inspector.GetQueueStats(context.Background(), "emails")
		if err != nil {
			return false, err
		}
		return stats.Pending == 1, nil
	})

	detail := waitForTaskState(t, inspector, "emails", info.BrokerTaskID, taskqueue.TaskStatePending)
	if detail.Type != "send_email" {
		t.Fatalf("GetTaskInfo().Type = %q, want %q", detail.Type, "send_email")
	}
	if string(detail.Payload) != `{"id":"123"}` {
		t.Fatalf("GetTaskInfo().Payload = %s, want original payload", string(detail.Payload))
	}

	result, err := inspector.ListTasks(context.Background(), "emails", taskqueue.TaskStatePending, nil)
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if result.Total != 1 || len(result.Tasks) != 1 {
		t.Fatalf("ListTasks() = total %d len %d, want 1/1", result.Total, len(result.Tasks))
	}

	defaultInfo, err := broker.Publish(context.Background(), &taskqueue.Task{
		Type:    "default_queue",
		Payload: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("Publish() default queue error = %v", err)
	}
	if defaultInfo.Queue != "default" {
		t.Fatalf("Publish() default queue = %q, want %q", defaultInfo.Queue, "default")
	}

	if _, err := inspector.GetQueueStats(context.Background(), "missing-queue"); err == nil {
		t.Fatal("GetQueueStats(missing) error = nil, want non-nil")
	}
	if _, err := inspector.ListTasks(context.Background(), "missing-queue", taskqueue.TaskStatePending, nil); !errors.Is(err, taskqueue.ErrQueueNotFound) {
		t.Fatalf("ListTasks(missing) error = %v, want ErrQueueNotFound", err)
	}
	if _, err := inspector.GetTaskInfo(context.Background(), "emails", "missing-task"); !errors.Is(err, taskqueue.ErrTaskNotFound) {
		t.Fatalf("GetTaskInfo(missing) error = %v, want ErrTaskNotFound", err)
	}
}

func TestBrokerPublishError(t *testing.T) {
	broker := NewBroker(asynq.RedisClientOpt{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = broker.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := broker.Publish(ctx, &taskqueue.Task{
		Type:    "send_email",
		Payload: []byte(`{}`),
	})
	if err == nil {
		t.Fatalf("expected Publish() to fail against unavailable redis")
	}
}

func TestConsumerStartReturnsOnContextCancel(t *testing.T) {
	_, opt := newMiniredis(t)
	consumer := NewConsumer(opt, ConsumerConfig{
		Queues: map[string]int{"consume": 1},
		Logger: noopAsynqLogger{},
	})
	inspector := newInspectorForTest(t, opt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- consumer.Start(ctx)
	}()

	waitForWorkers(t, inspector, 1)

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("consumer.Start() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for consumer shutdown")
	}

	if err := consumer.Stop(); err != nil {
		t.Fatalf("consumer.Stop() error = %v", err)
	}
}

func TestConsumerStartReturnsOnStop(t *testing.T) {
	_, opt := newMiniredis(t)
	consumer := NewConsumer(opt, ConsumerConfig{
		Queues: map[string]int{"consume": 1},
		Logger: noopAsynqLogger{},
	})
	inspector := newInspectorForTest(t, opt)

	ctx := context.Background()
	errCh := make(chan error, 1)
	go func() {
		errCh <- consumer.Start(ctx)
	}()

	waitForWorkers(t, inspector, 1)

	if err := consumer.Stop(); err != nil {
		t.Fatalf("consumer.Stop() error = %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("consumer.Start() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Start() to return after Stop()")
	}
}

func TestConsumerHandleRoutesTaskAndErrorState(t *testing.T) {
	_, opt := newMiniredis(t)
	consumer := NewConsumer(opt, ConsumerConfig{Logger: noopAsynqLogger{}})

	type receivedTask struct {
		task      *taskqueue.Task
		traceID   string
		spanID    string
		loggerTID string
		loggerSID string
	}

	receivedCh := make(chan receivedTask, 1)
	consumer.Handle("process", func(ctx context.Context, task *taskqueue.Task) error {
		got := receivedTask{task: task}
		if tc, ok := trace.TraceFromContext(ctx); ok && tc != nil {
			got.traceID = tc.TraceID
			got.spanID = tc.SpanId
		}
		got.loggerTID = logger.TraceIDFromContext(ctx)
		got.loggerSID = logger.SpanIDFromContext(ctx)
		receivedCh <- got
		return nil
	})
	consumer.Handle("fail", func(ctx context.Context, task *taskqueue.Task) error {
		return errors.New("boom")
	})

	traceCtx := &trace.TraceContext{TraceID: "trace-consume", SpanId: "span-consume", Sampled: true}
	processCtx := trace.ContextWithTrace(context.Background(), traceCtx)
	processPayload := wrapWithTrace(processCtx, []byte(`{"hello":"world"}`), &taskqueue.ReplySpec{Queue: "reply-q"}, map[string]string{"tenant": "acme"})

	if err := consumer.mux.ProcessTask(context.Background(), asynq.NewTask("process", processPayload)); err != nil {
		t.Fatalf("ProcessTask(process) error = %v", err)
	}

	var got receivedTask
	select {
	case got = <-receivedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for consumer handler")
	}

	if got.task.Type != "process" {
		t.Fatalf("handler task type = %q, want %q", got.task.Type, "process")
	}
	if got.task.Reply == nil || got.task.Reply.Queue != "reply-q" {
		t.Fatalf("handler reply = %#v, want reply-q", got.task.Reply)
	}
	if got.task.Metadata["tenant"] != "acme" {
		t.Fatalf("handler metadata = %#v, want tenant=acme", got.task.Metadata)
	}
	if got.loggerTID != got.traceID || got.loggerSID != got.spanID {
		t.Fatalf("logger and trace context mismatch: logger=(%q,%q) trace=(%q,%q)", got.loggerTID, got.loggerSID, got.traceID, got.spanID)
	}

	err := consumer.mux.ProcessTask(context.Background(), asynq.NewTask("fail", []byte(`{"bad":true}`)))
	if err == nil {
		t.Fatal("ProcessTask(fail) error = nil, want non-nil")
	}
}
