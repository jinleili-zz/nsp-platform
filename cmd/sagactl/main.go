package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	_ "github.com/lib/pq"

	"github.com/jinleili-zz/nsp-platform/saga/observer"
)

const defaultWatchInterval = 3 * time.Second

type observerService interface {
	ListTransactions(ctx context.Context, filter observer.ListFilter) (*observer.ListResult, error)
	ListFailedTransactions(ctx context.Context, limit int) (*observer.ListResult, error)
	GetTransactionDetail(ctx context.Context, txID string) (*observer.TransactionDetail, error)
}

type app struct {
	out  io.Writer
	err  io.Writer
	open func(context.Context, string) (observerService, io.Closer, error)
}

func main() {
	a := newApp(os.Stdout, os.Stderr, nil)
	if err := a.run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newApp(out, err io.Writer, opener func(context.Context, string) (observerService, io.Closer, error)) *app {
	if opener == nil {
		opener = openObserverService
	}
	return &app{out: out, err: err, open: opener}
}

func (a *app) run(args []string) error {
	root := flag.NewFlagSet("sagactl", flag.ContinueOnError)
	root.SetOutput(a.err)

	dsn := root.String("dsn", "", "PostgreSQL DSN for read-only saga observer access")
	if err := root.Parse(args); err != nil {
		return err
	}

	subArgs := root.Args()
	if len(subArgs) == 0 {
		a.printRootUsage()
		return errors.New("missing subcommand")
	}

	effectiveDSN := strings.TrimSpace(*dsn)
	if effectiveDSN == "" {
		effectiveDSN = strings.TrimSpace(os.Getenv("SAGA_OBSERVER_DSN"))
	}
	if effectiveDSN == "" {
		return errors.New("dsn is required via --dsn or SAGA_OBSERVER_DSN")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	svc, closer, err := a.open(ctx, effectiveDSN)
	if err != nil {
		return err
	}
	if closer != nil {
		defer closer.Close()
	}

	switch subArgs[0] {
	case "list":
		return a.runList(ctx, svc, subArgs[1:])
	case "failed":
		return a.runFailed(ctx, svc, subArgs[1:])
	case "show":
		return a.runShow(ctx, svc, subArgs[1:])
	case "watch":
		return a.runWatch(ctx, svc, subArgs[1:])
	default:
		a.printRootUsage()
		return fmt.Errorf("unknown subcommand %q", subArgs[0])
	}
}

func (a *app) runList(ctx context.Context, svc observerService, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(a.err)

	status := fs.String("status", "", "Filter by transaction status")
	limit := fs.Int("limit", observer.DefaultLimit, "Maximum number of rows to return")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := validateStatus(*status); err != nil {
		return err
	}

	result, err := svc.ListTransactions(ctx, observer.ListFilter{
		Status: *status,
		Limit:  *limit,
	})
	if err != nil {
		return err
	}

	renderTransactionList(a.out, result, *status)
	return nil
}

func (a *app) runFailed(ctx context.Context, svc observerService, args []string) error {
	fs := flag.NewFlagSet("failed", flag.ContinueOnError)
	fs.SetOutput(a.err)

	limit := fs.Int("limit", observer.DefaultLimit, "Maximum number of rows to return")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result, err := svc.ListFailedTransactions(ctx, *limit)
	if err != nil {
		return err
	}

	renderTransactionList(a.out, result, "failed")
	return nil
}

func (a *app) runShow(ctx context.Context, svc observerService, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(a.err)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("show requires exactly one transaction id")
	}

	detail, err := svc.GetTransactionDetail(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if detail == nil {
		return fmt.Errorf("transaction not found: %s", fs.Arg(0))
	}

	io.WriteString(a.out, renderTransactionDetail(detail, false, 0))
	return nil
}

func (a *app) runWatch(ctx context.Context, svc observerService, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(a.err)

	interval := fs.Duration("interval", defaultWatchInterval, "Refresh interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("watch requires exactly one transaction id")
	}
	if *interval <= 0 {
		return errors.New("interval must be positive")
	}

	txID := fs.Arg(0)
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	for {
		detail, err := svc.GetTransactionDetail(ctx, txID)
		if err != nil {
			return err
		}
		if detail == nil {
			return fmt.Errorf("transaction not found: %s", txID)
		}

		io.WriteString(a.out, "\033[H\033[2J")
		io.WriteString(a.out, renderTransactionDetail(detail, true, *interval))

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (a *app) printRootUsage() {
	fmt.Fprintln(a.err, "usage: sagactl [--dsn <dsn>] <list|failed|show|watch> [flags]")
}

func openObserverService(ctx context.Context, dsn string) (observerService, io.Closer, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("open observer database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("ping observer database: %w", err)
	}
	return observer.NewReader(db), db, nil
}

func validateStatus(status string) error {
	switch status {
	case "", "pending", "running", "compensating", "succeeded", "failed":
		return nil
	default:
		return fmt.Errorf("invalid status %q", status)
	}
}

func renderTransactionList(w io.Writer, result *observer.ListResult, filter string) {
	if result == nil {
		fmt.Fprintln(w, "no results")
		return
	}

	if filter == "" {
		fmt.Fprintln(w, "transactions:")
	} else {
		fmt.Fprintf(w, "transactions (status=%s):\n", filter)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tSTEP\tCREATED\tUPDATED\tTRACE\tLAST ERROR")
	for _, tx := range result.Transactions {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%d\t%s\t%s\t%s\t%s\n",
			tx.ID,
			tx.Status,
			tx.CurrentStep,
			formatTime(tx.CreatedAt),
			formatTime(tx.UpdatedAt),
			fallback(tx.TraceID, "-"),
			truncateText(tx.LastError, 60),
		)
	}
	tw.Flush()

	if result.Truncated {
		fmt.Fprintf(w, "\noutput truncated to %d results; rerun with --limit to see more\n", result.Limit)
	}
}

func renderTransactionDetail(detail *observer.TransactionDetail, watching bool, interval time.Duration) string {
	var b strings.Builder
	if watching {
		fmt.Fprintf(&b, "watching transaction %s (refresh=%s)\n\n", detail.Summary.ID, interval)
	}

	fmt.Fprintln(&b, "Transaction")
	fmt.Fprintf(&b, "  id: %s\n", detail.Summary.ID)
	fmt.Fprintf(&b, "  status: %s\n", detail.Summary.Status)
	fmt.Fprintf(&b, "  current_step: %d\n", detail.Summary.CurrentStep)
	fmt.Fprintf(&b, "  created_at: %s\n", formatTime(detail.Summary.CreatedAt))
	fmt.Fprintf(&b, "  updated_at: %s\n", formatTime(detail.Summary.UpdatedAt))
	fmt.Fprintf(&b, "  finished_at: %s\n", formatOptionalTime(detail.Summary.FinishedAt))
	fmt.Fprintf(&b, "  timeout_at: %s\n", formatOptionalTime(detail.Summary.TimeoutAt))
	fmt.Fprintf(&b, "  trace_id: %s\n", fallback(detail.Summary.TraceID, "unavailable"))
	fmt.Fprintf(&b, "  span_id: %s\n", fallback(detail.SpanID, "unavailable"))
	if detail.Summary.LockedBy != "" {
		fmt.Fprintf(&b, "  locked_by: %s\n", detail.Summary.LockedBy)
	}
	if detail.Summary.LockedUntil != nil {
		fmt.Fprintf(&b, "  locked_until: %s\n", formatOptionalTime(detail.Summary.LockedUntil))
	}
	if detail.Summary.LastError != "" {
		fmt.Fprintf(&b, "  last_error: %s\n", detail.Summary.LastError)
	}

	fmt.Fprintln(&b, "\nSteps")
	for _, step := range detail.Steps {
		fmt.Fprintf(&b, "  [%d] %s (%s)\n", step.Index, step.Name, step.Type)
		fmt.Fprintf(&b, "    status: %s\n", step.Status)
		fmt.Fprintf(&b, "    retry_count: %d\n", step.RetryCount)
		fmt.Fprintf(&b, "    poll_count: %d/%d\n", step.PollCount, step.PollMaxTimes)
		fmt.Fprintf(&b, "    started_at: %s\n", formatOptionalTime(step.StartedAt))
		fmt.Fprintf(&b, "    finished_at: %s\n", formatOptionalTime(step.FinishedAt))
		fmt.Fprintf(&b, "    last_error: %s\n", fallback(step.LastError, "-"))
		if step.PollTask != nil {
			fmt.Fprintf(&b, "    poll_task.next_poll_at: %s\n", formatOptionalTime(step.PollTask.NextPollAt))
			if step.PollTask.LockedBy != "" {
				fmt.Fprintf(&b, "    poll_task.locked_by: %s\n", step.PollTask.LockedBy)
			}
			if step.PollTask.LockedUntil != nil {
				fmt.Fprintf(&b, "    poll_task.locked_until: %s\n", formatOptionalTime(step.PollTask.LockedUntil))
			}
		}
		fmt.Fprintf(&b, "    action_response: %s\n", summarizeJSON(step.ActionResponse, 160))
	}

	return b.String()
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return formatTime(*t)
}

func fallback(value, alt string) string {
	if strings.TrimSpace(value) == "" {
		return alt
	}
	return value
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" || limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func summarizeJSON(raw []byte, limit int) string {
	if len(raw) == 0 {
		return "-"
	}
	var compact strings.Builder
	if err := jsonCompact(&compact, raw); err == nil {
		return truncateText(compact.String(), limit)
	}
	return truncateText(string(raw), limit)
}

func jsonCompact(dst *strings.Builder, raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dst.Write(encoded)
	return nil
}
