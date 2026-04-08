package observer

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReaderListTransactions(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	db, assertDone := openScriptedDB(t, []scriptedResponse{
		{
			match: "FROM saga_transactions",
			columns: []string{
				"id", "status", "current_step", "created_at", "updated_at", "finished_at", "timeout_at",
				"last_error", "trace_id", "locked_by", "locked_until",
			},
			rows: [][]driver.Value{
				{"tx-running-1", "running", 1, now, now, nil, nil, "", "trace-1", "", nil},
				{"tx-running-2", "running", 2, now.Add(-time.Minute), now.Add(-time.Minute), nil, nil, "retrying", "", "", nil},
			},
		},
	})
	defer db.Close()
	defer assertDone()

	reader := NewReader(db)
	result, err := reader.ListTransactions(context.Background(), ListFilter{Status: "running", Limit: 1})
	if err != nil {
		t.Fatalf("ListTransactions() error = %v", err)
	}

	if !result.Truncated {
		t.Fatalf("expected truncated result")
	}
	if result.Limit != 1 {
		t.Fatalf("expected limit 1, got %d", result.Limit)
	}
	if len(result.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(result.Transactions))
	}
	if got := result.Transactions[0].TraceID; got != "trace-1" {
		t.Fatalf("expected trace id trace-1, got %q", got)
	}
	if got := result.Transactions[0].Status; got != "running" {
		t.Fatalf("expected status running, got %q", got)
	}
}

func TestReaderListFailedTransactions(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	finished := now.Add(-time.Minute)

	db, assertDone := openScriptedDB(t, []scriptedResponse{
		{
			match: "COALESCE(finished_at, updated_at) DESC",
			columns: []string{
				"id", "status", "current_step", "created_at", "updated_at", "finished_at", "timeout_at",
				"last_error", "trace_id", "locked_by", "locked_until",
			},
			rows: [][]driver.Value{
				{"tx-failed-1", "failed", 3, now.Add(-time.Hour), now, finished, nil, "boom", "trace-failed", "", nil},
			},
		},
	})
	defer db.Close()
	defer assertDone()

	reader := NewReader(db)
	result, err := reader.ListFailedTransactions(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListFailedTransactions() error = %v", err)
	}
	if len(result.Transactions) != 1 {
		t.Fatalf("expected 1 failed transaction, got %d", len(result.Transactions))
	}
	if got := result.Transactions[0].LastError; got != "boom" {
		t.Fatalf("expected last error boom, got %q", got)
	}
}

func TestReaderGetTransactionDetail(t *testing.T) {
	now := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	lockedUntil := now.Add(5 * time.Minute)
	nextPoll := now.Add(10 * time.Second)
	started := now.Add(-time.Minute)
	finished := now.Add(-30 * time.Second)

	db, assertDone := openScriptedDB(t, []scriptedResponse{
		{
			match: "FROM saga_transactions",
			columns: []string{
				"id", "status", "current_step", "created_at", "updated_at", "finished_at", "timeout_at",
				"last_error", "trace_id", "locked_by", "locked_until", "span_id",
			},
			rows: [][]driver.Value{
				{"tx-detail", "compensating", 1, now.Add(-time.Hour), now, nil, nil, "rollback", "", "inst-1", lockedUntil, ""},
			},
		},
		{
			match: "FROM saga_steps s",
			columns: []string{
				"id", "step_index", "name", "step_type", "status", "retry_count", "poll_count", "poll_max_times",
				"started_at", "finished_at", "last_error", "action_response",
				"has_poll_task", "next_poll_at", "poll_locked_by", "poll_locked_until",
			},
			rows: [][]driver.Value{
				{"step-1", 0, "async-step", "async", "polling", 1, 3, 10, started, nil, "waiting", []byte(`{"task":"123"}`), true, nextPoll, "poller-1", lockedUntil},
				{"step-2", 1, "comp-step", "sync", "compensated", 0, 0, 0, started, finished, "", []byte(`{"ok":true}`), false, nil, "", nil},
			},
		},
	})
	defer db.Close()
	defer assertDone()

	reader := NewReader(db)
	detail, err := reader.GetTransactionDetail(context.Background(), "tx-detail")
	if err != nil {
		t.Fatalf("GetTransactionDetail() error = %v", err)
	}
	if detail == nil {
		t.Fatalf("expected transaction detail, got nil")
	}
	if detail.Summary.TraceID != "" {
		t.Fatalf("expected missing trace id to stay empty, got %q", detail.Summary.TraceID)
	}
	if detail.Summary.LockedBy != "inst-1" {
		t.Fatalf("expected locked_by inst-1, got %q", detail.Summary.LockedBy)
	}
	if len(detail.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(detail.Steps))
	}
	if detail.Steps[0].PollTask == nil || detail.Steps[0].PollTask.NextPollAt == nil {
		t.Fatalf("expected first step poll task details")
	}
	if detail.Steps[1].PollTask != nil {
		t.Fatalf("expected second step to tolerate missing poll task")
	}
	if detail.Steps[1].Status != "compensated" {
		t.Fatalf("expected compensated step, got %q", detail.Steps[1].Status)
	}
	if beginCalls := scriptedBeginCalls(t, db); beginCalls != 1 {
		t.Fatalf("expected one snapshot transaction, got %d", beginCalls)
	}
}

var (
	registerScriptedDriver sync.Once
	scriptedRegistryMu     sync.Mutex
	scriptedRegistry       = map[string]*scriptedScenario{}
	scriptedCounter        int
)

type scriptedScenario struct {
	mu        sync.Mutex
	responses []scriptedResponse
	beginTx   int
}

type scriptedResponse struct {
	match   string
	columns []string
	rows    [][]driver.Value
}

func openScriptedDB(t *testing.T, responses []scriptedResponse) (*sql.DB, func()) {
	t.Helper()

	registerScriptedDriver.Do(func() {
		sql.Register("observer-scripted", scriptedDriver{})
	})

	scriptedRegistryMu.Lock()
	scriptedCounter++
	key := fmt.Sprintf("scenario-%d", scriptedCounter)
	scriptedRegistry[key] = &scriptedScenario{responses: append([]scriptedResponse(nil), responses...)}
	scriptedRegistryMu.Unlock()

	db, err := sql.Open("observer-scripted", key)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}

	return db, func() {
		t.Helper()
		scriptedRegistryMu.Lock()
		scenario := scriptedRegistry[key]
		delete(scriptedRegistry, key)
		scriptedRegistryMu.Unlock()
		if scenario == nil {
			return
		}
		scenario.mu.Lock()
		defer scenario.mu.Unlock()
		if len(scenario.responses) != 0 {
			t.Fatalf("unconsumed scripted responses: %d", len(scenario.responses))
		}
	}
}

func scriptedBeginCalls(t *testing.T, db *sql.DB) int {
	t.Helper()

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("db.Conn() error = %v", err)
	}
	defer conn.Close()

	var beginCalls int
	err = conn.Raw(func(driverConn any) error {
		scripted, ok := driverConn.(*scriptedConn)
		if !ok {
			return fmt.Errorf("unexpected driver conn type %T", driverConn)
		}

		scriptedRegistryMu.Lock()
		scenario := scriptedRegistry[scripted.key]
		scriptedRegistryMu.Unlock()
		if scenario == nil {
			return fmt.Errorf("missing scripted scenario")
		}

		scenario.mu.Lock()
		beginCalls = scenario.beginTx
		scenario.mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("conn.Raw() error = %v", err)
	}

	return beginCalls
}

type scriptedDriver struct{}

func (scriptedDriver) Open(name string) (driver.Conn, error) {
	return &scriptedConn{key: name}, nil
}

type scriptedConn struct {
	key string
}

func (c *scriptedConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("Prepare not supported")
}

func (c *scriptedConn) Close() error { return nil }

func (c *scriptedConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *scriptedConn) BeginTx(_ context.Context, _ driver.TxOptions) (driver.Tx, error) {
	scriptedRegistryMu.Lock()
	scenario := scriptedRegistry[c.key]
	scriptedRegistryMu.Unlock()
	if scenario == nil {
		return nil, fmt.Errorf("missing scripted scenario")
	}

	scenario.mu.Lock()
	scenario.beginTx++
	scenario.mu.Unlock()
	return scriptedTx{}, nil
}

func (c *scriptedConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	scriptedRegistryMu.Lock()
	scenario := scriptedRegistry[c.key]
	scriptedRegistryMu.Unlock()
	if scenario == nil {
		return nil, fmt.Errorf("missing scripted scenario")
	}

	scenario.mu.Lock()
	defer scenario.mu.Unlock()

	if len(scenario.responses) == 0 {
		return nil, fmt.Errorf("unexpected query: %s", query)
	}

	response := scenario.responses[0]
	scenario.responses = scenario.responses[1:]
	if response.match != "" && !strings.Contains(query, response.match) {
		return nil, fmt.Errorf("query %q does not contain %q", query, response.match)
	}

	return &scriptedRows{
		columns: response.columns,
		rows:    response.rows,
	}, nil
}

func (c *scriptedConn) CheckNamedValue(*driver.NamedValue) error { return nil }

type scriptedTx struct{}

func (scriptedTx) Commit() error { return nil }

func (scriptedTx) Rollback() error { return nil }

type scriptedRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *scriptedRows) Columns() []string { return r.columns }

func (r *scriptedRows) Close() error { return nil }

func (r *scriptedRows) Next(dest []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	row := r.rows[r.index]
	r.index++
	for i := range row {
		dest[i] = row[i]
	}
	return nil
}
