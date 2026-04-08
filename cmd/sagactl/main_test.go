package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

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

type fakeObserverService struct {
	listResult      *observer.ListResult
	failedResult    *observer.ListResult
	detailResult    *observer.TransactionDetail
	lastListFilter  observer.ListFilter
	lastFailedLimit int
}

func (f *fakeObserverService) ListTransactions(_ context.Context, filter observer.ListFilter) (*observer.ListResult, error) {
	f.lastListFilter = filter
	return f.listResult, nil
}

func (f *fakeObserverService) ListFailedTransactions(_ context.Context, limit int) (*observer.ListResult, error) {
	f.lastFailedLimit = limit
	return f.failedResult, nil
}

func (f *fakeObserverService) GetTransactionDetail(_ context.Context, _ string) (*observer.TransactionDetail, error) {
	return f.detailResult, nil
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }
