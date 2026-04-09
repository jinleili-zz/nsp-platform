package asynqbroker

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jinleili-zz/nsp-platform/logger"
	"github.com/jinleili-zz/nsp-platform/taskqueue"
	"github.com/jinleili-zz/nsp-platform/trace"
)

func captureAsynqbrokerLogOutput(t *testing.T, fn func()) string {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "asynqbroker_logger_*.log")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	oldStdout, err := syscall.Dup(int(os.Stdout.Fd()))
	if err != nil {
		tmpFile.Close()
		t.Fatalf("failed to dup stdout: %v", err)
	}
	defer func() {
		syscall.Dup2(oldStdout, int(os.Stdout.Fd()))
		syscall.Close(oldStdout)
	}()

	if err := syscall.Dup2(int(tmpFile.Fd()), int(os.Stdout.Fd())); err != nil {
		tmpFile.Close()
		t.Fatalf("failed to redirect stdout: %v", err)
	}

	fn()

	tmpFile.Close()

	content, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("failed to read captured log output: %v", err)
	}
	return string(content)
}

func initAsynqbrokerTestLogger(t *testing.T) {
	t.Helper()

	if err := logger.Init(&logger.Config{
		Level:        logger.LevelDebug,
		Format:       logger.FormatJSON,
		ServiceName:  "asynqbroker-test",
		OutputPaths:  []string{"stdout"},
		EnableCaller: false,
	}); err != nil {
		t.Fatalf("logger.Init() error = %v", err)
	}
	t.Cleanup(func() {
		_ = logger.Sync()
	})
}

func TestResolveFrameworkLoggerUsesRepositoryAdapterByDefault(t *testing.T) {
	initAsynqbrokerTestLogger(t)

	runtimeLog := resolveRuntimeLogger(logger.GetLogger().With("custom_logger", "adapter"))
	frameworkLog := resolveFrameworkLogger(runtimeLog, nil)

	output := captureAsynqbrokerLogOutput(t, func() {
		frameworkLog.Info("framework started")
		_ = logger.Sync()
	})

	for _, want := range []string{
		`"component":"asynq"`,
		`"custom_logger":"adapter"`,
		`framework started`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %s, got %s", want, output)
		}
	}
}

func TestResolveFrameworkLoggerPreservesExplicitOverride(t *testing.T) {
	explicit := noopAsynqLogger{}
	got := resolveFrameworkLogger(resolveRuntimeLogger(nil), explicit)

	if _, ok := got.(noopAsynqLogger); !ok {
		t.Fatalf("expected explicit asynq logger override to be preserved, got %T", got)
	}
}

func TestConsumerLogsHandlerErrorWithRestoredTrace(t *testing.T) {
	initAsynqbrokerTestLogger(t)

	_, opt := newMiniredis(t)
	consumer := NewConsumer(opt, ConsumerConfig{
		Concurrency:   1,
		Queues:        map[string]int{"jobs": 1},
		Logger:        noopAsynqLogger{},
		RuntimeLogger: logger.GetLogger().With("custom_logger", "consumer"),
	})
	consumer.Handle("fail", func(ctx context.Context, task *taskqueue.Task) error {
		return errors.New("boom")
	})

	inspector := newInspectorForTest(t, opt)
	client := newAsynqClient(t, opt)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- consumer.Start(ctx)
	}()
	waitForWorkers(t, inspector, 1)

	traceCtx := &trace.TraceContext{
		TraceID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SpanId:  "bbbbbbbbbbbbbbbb",
		Sampled: true,
	}
	payload := wrapWithTrace(trace.ContextWithTrace(context.Background(), traceCtx), []byte(`{"ok":false}`), nil, nil)

	var taskInfo *asynq.TaskInfo
	output := captureAsynqbrokerLogOutput(t, func() {
		var err error
		taskInfo, err = client.Enqueue(asynq.NewTask("fail", payload), asynq.Queue("jobs"), asynq.MaxRetry(0))
		if err != nil {
			t.Fatalf("client.Enqueue() error = %v", err)
		}

		waitForTaskState(t, inspector, "jobs", taskInfo.ID, taskqueue.TaskStateFailed)

		if err := consumer.Stop(); err != nil {
			t.Fatalf("consumer.Stop() error = %v", err)
		}
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("consumer.Start() error = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for consumer shutdown")
		}
		_ = logger.Sync()
	})

	for _, want := range []string{
		`"custom_logger":"consumer"`,
		`"component":"asynqbroker"`,
		`"task_type":"fail"`,
		`"task_id":"` + taskInfo.ID + `"`,
		`"queue":"jobs"`,
		`"trace_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`,
		`"span_id":"`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %s, got %s", want, output)
		}
	}
}

func TestBrokerAndInspectorUseInjectedRuntimeLogger(t *testing.T) {
	initAsynqbrokerTestLogger(t)

	broker := NewBrokerWithConfig(asynq.RedisClientOpt{Addr: "127.0.0.1:1"}, BrokerConfig{
		Logger: logger.GetLogger().With("custom_logger", "broker"),
	})
	t.Cleanup(func() { _ = broker.Close() })

	brokerOutput := captureAsynqbrokerLogOutput(t, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		_, err := broker.Publish(ctx, &taskqueue.Task{
			Type:    "email:send",
			Payload: []byte(`{}`),
			Queue:   "emails",
		})
		if err == nil {
			t.Fatal("expected broker.Publish() to fail against unavailable redis")
		}
		_ = logger.Sync()
	})

	for _, want := range []string{
		`"custom_logger":"broker"`,
		`"component":"asynqbroker"`,
		`"task_type":"email:send"`,
		`"queue":"emails"`,
	} {
		if !strings.Contains(brokerOutput, want) {
			t.Fatalf("expected broker log output to contain %s, got %s", want, brokerOutput)
		}
	}

	_, opt := newMiniredis(t)
	inspector := NewInspectorWithConfig(opt, InspectorConfig{
		Logger: logger.GetLogger().With("custom_logger", "inspector"),
	})
	t.Cleanup(func() {
		if err := inspector.Close(); err != nil {
			t.Fatalf("inspector.Close() error = %v", err)
		}
	})

	inspectorOutput := captureAsynqbrokerLogOutput(t, func() {
		_, err := inspector.GetQueueStats(context.Background(), "missing-queue")
		if err == nil {
			t.Fatal("GetQueueStats() error = nil, want non-nil")
		}
		_ = logger.Sync()
	})

	for _, want := range []string{
		`"custom_logger":"inspector"`,
		`"component":"asynqbroker"`,
		`"queue":"missing-queue"`,
	} {
		if !strings.Contains(inspectorOutput, want) {
			t.Fatalf("expected inspector log output to contain %s, got %s", want, inspectorOutput)
		}
	}
}
