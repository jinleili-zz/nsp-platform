package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jinleili-zz/nsp-platform/saga/observer"
)

func TestAppRunList(t *testing.T) {
	var out strings.Builder
	svc := &fakeObserverService{
		listResult: &observer.ListResult{
			Limit:     1,
			Truncated: true,
			Transactions: []observer.TransactionSummary{
				{
					ID:          "tx-1",
					Status:      "running",
					CurrentStep: 1,
					CreatedAt:   time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
					UpdatedAt:   time.Date(2026, 4, 8, 12, 1, 0, 0, time.UTC),
					TraceID:     "trace-1",
					LastError:   "still running",
				},
			},
		},
	}

	app := newApp(&out, io.Discard, func(_ context.Context, dsn string) (observerService, io.Closer, error) {
		if dsn != "test-dsn" {
			return nil, nil, fmt.Errorf("unexpected dsn %q", dsn)
		}
		return svc, nopCloser{}, nil
	})

	err := app.run([]string{"--dsn", "test-dsn", "list", "--status", "running", "--limit", "1"})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	if svc.lastListFilter.Status != "running" || svc.lastListFilter.Limit != 1 {
		t.Fatalf("unexpected list filter: %+v", svc.lastListFilter)
	}

	output := out.String()
	if !strings.Contains(output, "transactions (status=running):") {
		t.Fatalf("expected status heading in output: %s", output)
	}
	if !strings.Contains(output, "output truncated to 1 results") {
		t.Fatalf("expected truncation message in output: %s", output)
	}
}

func TestAppRunFailed(t *testing.T) {
	var out strings.Builder
	svc := &fakeObserverService{
		failedResult: &observer.ListResult{
			Transactions: []observer.TransactionSummary{
				{
					ID:          "tx-failed",
					Status:      "failed",
					CurrentStep: 2,
					CreatedAt:   time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
					UpdatedAt:   time.Date(2026, 4, 8, 10, 1, 0, 0, time.UTC),
					LastError:   "boom",
				},
			},
		},
	}

	app := newApp(&out, io.Discard, func(context.Context, string) (observerService, io.Closer, error) {
		return svc, nopCloser{}, nil
	})

	if err := app.run([]string{"--dsn", "dsn", "failed", "--limit", "5"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if svc.lastFailedLimit != 5 {
		t.Fatalf("expected failed limit 5, got %d", svc.lastFailedLimit)
	}
	if !strings.Contains(out.String(), "transactions (status=failed):") {
		t.Fatalf("expected failed heading in output: %s", out.String())
	}
}

func TestAppRunShow(t *testing.T) {
	var out strings.Builder
	lockedUntil := time.Date(2026, 4, 8, 12, 5, 0, 0, time.UTC)
	svc := &fakeObserverService{
		detailResult: &observer.TransactionDetail{
			Summary: observer.TransactionSummary{
				ID:          "tx-1",
				Status:      "compensating",
				CurrentStep: 1,
				CreatedAt:   time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
				UpdatedAt:   time.Date(2026, 4, 8, 12, 1, 0, 0, time.UTC),
				TraceID:     "",
				LockedBy:    "inst-1",
				LockedUntil: &lockedUntil,
				LastError:   "rollback",
			},
			Steps: []observer.StepDetail{
				{
					Index:          0,
					Name:           "async-step",
					Type:           "async",
					Status:         "polling",
					RetryCount:     1,
					PollCount:      3,
					PollMaxTimes:   10,
					ActionResponse: []byte(`{"task_id":"123","nested":{"ok":true}}`),
				},
			},
		},
	}

	app := newApp(&out, io.Discard, func(context.Context, string) (observerService, io.Closer, error) {
		return svc, nopCloser{}, nil
	})

	if err := app.run([]string{"--dsn", "dsn", "show", "tx-1"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if svc.lastDetailTxID != "tx-1" {
		t.Fatalf("expected detail tx id tx-1, got %q", svc.lastDetailTxID)
	}

	output := out.String()
	if !strings.Contains(output, "trace_id: unavailable") {
		t.Fatalf("expected unavailable trace in output: %s", output)
	}
	if !strings.Contains(output, "locked_by: inst-1") {
		t.Fatalf("expected lock info in output: %s", output)
	}
	if !strings.Contains(output, `"task_id":"123"`) {
		t.Fatalf("expected action response summary in output: %s", output)
	}
}

func TestAppRunWatch(t *testing.T) {
	var out strings.Builder
	svc := &fakeObserverService{
		detailResult: &observer.TransactionDetail{
			Summary: observer.TransactionSummary{
				ID:          "tx-watch",
				Status:      "running",
				CurrentStep: 1,
				CreatedAt:   time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC),
				UpdatedAt:   time.Date(2026, 4, 8, 12, 1, 0, 0, time.UTC),
			},
		},
	}

	app := newApp(&out, io.Discard, func(context.Context, string) (observerService, io.Closer, error) {
		return svc, nopCloser{}, nil
	})
	app.newContext = func() (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx, func() {}
	}

	if err := app.run([]string{"--dsn", "dsn", "watch", "--interval", "1ms", "tx-watch"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if svc.lastDetailTxID != "tx-watch" {
		t.Fatalf("expected detail tx id tx-watch, got %q", svc.lastDetailTxID)
	}
	output := out.String()
	if !strings.Contains(output, "watching transaction tx-watch") {
		t.Fatalf("expected watch heading in output: %s", output)
	}
}

func TestAppRunListHelpDoesNotOpenObserver(t *testing.T) {
	var out strings.Builder
	openCalled := false
	app := newApp(&out, io.Discard, func(context.Context, string) (observerService, io.Closer, error) {
		openCalled = true
		return nil, nil, fmt.Errorf("unexpected open")
	})

	err := app.run([]string{"list", "-h"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("run() error = %v, want flag.ErrHelp", err)
	}
	if openCalled {
		t.Fatalf("expected help flow to avoid opening observer service")
	}
}

func TestAppRunShowBadArgsDoesNotOpenObserver(t *testing.T) {
	var out strings.Builder
	openCalled := false
	app := newApp(&out, io.Discard, func(context.Context, string) (observerService, io.Closer, error) {
		openCalled = true
		return nil, nil, fmt.Errorf("unexpected open")
	})

	err := app.run([]string{"show"})
	if err == nil || err.Error() != "show requires exactly one transaction id" {
		t.Fatalf("run() error = %v", err)
	}
	if openCalled {
		t.Fatalf("expected argument validation to avoid opening observer service")
	}
}

func TestAppRunUnknownSubcommandDoesNotRequireDSN(t *testing.T) {
	var out strings.Builder
	openCalled := false
	app := newApp(&out, io.Discard, func(context.Context, string) (observerService, io.Closer, error) {
		openCalled = true
		return nil, nil, fmt.Errorf("unexpected open")
	})

	err := app.run([]string{"typo"})
	if err == nil || err.Error() != `unknown subcommand "typo"` {
		t.Fatalf("run() error = %v", err)
	}
	if openCalled {
		t.Fatalf("expected unknown subcommand flow to avoid opening observer service")
	}
}

func TestTruncateTextUTF8(t *testing.T) {
	got := truncateText("步骤执行失败：连接超时", 8)
	want := "步骤执行失..."
	if got != want {
		t.Fatalf("truncateText() = %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncateText() returned invalid utf8: %q", got)
	}
}

type fakeObserverService struct {
	listResult      *observer.ListResult
	failedResult    *observer.ListResult
	detailResult    *observer.TransactionDetail
	lastListFilter  observer.ListFilter
	lastFailedLimit int
	lastDetailTxID  string
}

func (f *fakeObserverService) ListTransactions(_ context.Context, filter observer.ListFilter) (*observer.ListResult, error) {
	f.lastListFilter = filter
	return f.listResult, nil
}

func (f *fakeObserverService) ListFailedTransactions(_ context.Context, limit int) (*observer.ListResult, error) {
	f.lastFailedLimit = limit
	return f.failedResult, nil
}

func (f *fakeObserverService) GetTransactionDetail(_ context.Context, txID string) (*observer.TransactionDetail, error) {
	f.lastDetailTxID = txID
	return f.detailResult, nil
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }
