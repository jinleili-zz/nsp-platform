// File: coordinator.go
// Package saga - Coordinator drives SAGA transaction state machine

package saga

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jinleili-zz/nsp-platform/logger"
)

// CoordinatorConfig holds configuration for the Coordinator.
type CoordinatorConfig struct {
	// WorkerCount is the number of concurrent transaction workers (default: 4).
	WorkerCount int
	// ScanInterval is the interval for scanning pending transactions (default: 5s).
	ScanInterval time.Duration
	// TimeoutScanInterval is the interval for scanning timed-out transactions (default: 30s).
	TimeoutScanInterval time.Duration
	// AsyncStepTimeout is the timeout for waiting on async step completion (default: 10 minutes).
	AsyncStepTimeout time.Duration
	// InstanceID is the unique identifier for this instance (for distributed locking).
	InstanceID string
	// LeaseDuration is the duration of the distributed lock lease (default: 5 minutes).
	LeaseDuration time.Duration
	// Logger is the optional module runtime logger. Defaults to logger.Platform().
	Logger logger.Logger
}

// DefaultCoordinatorConfig returns the default coordinator configuration.
func DefaultCoordinatorConfig() *CoordinatorConfig {
	return &CoordinatorConfig{
		WorkerCount:         4,
		ScanInterval:        5 * time.Second,
		TimeoutScanInterval: 30 * time.Second,
		AsyncStepTimeout:    10 * time.Minute,
		InstanceID:          "",
		LeaseDuration:       5 * time.Minute,
	}
}

// Coordinator drives SAGA transaction execution using a state machine.
type Coordinator struct {
	store    Store
	executor *Executor
	poller   *Poller
	config   *CoordinatorConfig

	taskQueue chan string // Queue of transaction IDs to process
	stopCh    chan struct{}
	wg        sync.WaitGroup

	// Track active transactions to prevent duplicate processing
	activeTxMu sync.Mutex
	activeTx   map[string]bool
	log        logger.Logger
}

// NewCoordinator creates a new Coordinator with the given dependencies.
// If cfg.InstanceID is empty, it generates one from hostname and PID.
func NewCoordinator(store Store, executor *Executor, poller *Poller, cfg *CoordinatorConfig) *Coordinator {
	if cfg == nil {
		cfg = DefaultCoordinatorConfig()
	}

	if cfg.InstanceID == "" {
		hostname, _ := os.Hostname()
		cfg.InstanceID = fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}

	return &Coordinator{
		store:     store,
		executor:  executor,
		poller:    poller,
		config:    cfg,
		taskQueue: make(chan string, 1000),
		stopCh:    make(chan struct{}),
		activeTx:  make(map[string]bool),
		log:       resolveSagaLogger(cfg.Logger),
	}
}

// Start begins the coordinator's background tasks.
func (c *Coordinator) Start(ctx context.Context) {
	// Start worker goroutines
	for i := 0; i < c.config.WorkerCount; i++ {
		c.wg.Add(1)
		go c.worker(ctx, i)
	}

	// Start recovery scan (once at startup)
	c.wg.Add(1)
	go c.recoveryScan(ctx)

	// Start timeout scanner
	c.wg.Add(1)
	go c.timeoutScanner(ctx)
}

// Stop gracefully stops the coordinator.
func (c *Coordinator) Stop() {
	close(c.stopCh)
	c.wg.Wait()
}

// Submit adds a transaction to the processing queue.
// If the transaction is already being processed, it will be skipped.
func (c *Coordinator) Submit(txID string) bool {
	c.activeTxMu.Lock()
	if c.activeTx[txID] {
		c.activeTxMu.Unlock()
		return false // Already processing
	}
	c.activeTx[txID] = true
	c.activeTxMu.Unlock()

	select {
	case c.taskQueue <- txID:
		return true
	default:
		// Queue is full, mark as inactive and log warning
		c.activeTxMu.Lock()
		delete(c.activeTx, txID)
		c.activeTxMu.Unlock()
		c.log.Warn("transaction queue is full, dropping transaction",
			sagaFieldInstance, c.config.InstanceID,
			sagaFieldTxID, txID,
		)
		return false
	}
}

// worker processes transactions from the task queue.
func (c *Coordinator) worker(ctx context.Context, id int) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case txID := <-c.taskQueue:
			c.driveTransaction(ctx, txID)
		}
	}
}

// recoveryScan performs crash recovery at startup.
// Uses FOR UPDATE SKIP LOCKED via store to ensure multi-instance safety.
// Releases locks for any transactions that cannot be submitted to the worker queue.
func (c *Coordinator) recoveryScan(ctx context.Context) {
	defer c.wg.Done()

	// Wait a short time before starting recovery to allow other components to initialize
	select {
	case <-ctx.Done():
		return
	case <-c.stopCh:
		return
	case <-time.After(1 * time.Second):
	}

	txs, err := c.store.ListRecoverableTransactions(ctx, c.config.InstanceID, 100, c.config.LeaseDuration)
	if err != nil {
		c.log.ErrorContext(ctx, "failed to list recoverable transactions",
			sagaFieldInstance, c.config.InstanceID,
			logger.FieldError, err,
		)
		return
	}

	queued := 0
	for i, tx := range txs {
		txCtx := rehydrateSagaTraceContext(ctx, tx)
		select {
		case <-ctx.Done():
			// Release locks for all remaining transactions
			for _, remaining := range txs[i:] {
				c.store.ReleaseTransaction(txCtx, remaining.ID, c.config.InstanceID)
			}
			return
		case <-c.stopCh:
			for _, remaining := range txs[i:] {
				c.store.ReleaseTransaction(txCtx, remaining.ID, c.config.InstanceID)
			}
			return
		default:
			if c.Submit(tx.ID) {
				queued++
			} else {
				// Queue full or already active, release the lock
				c.store.ReleaseTransaction(txCtx, tx.ID, c.config.InstanceID)
			}
		}
	}

	c.log.InfoContext(ctx, "recovery scan complete",
		sagaFieldInstance, c.config.InstanceID,
		"found_transactions", len(txs),
		"queued_transactions", queued,
	)
}

// timeoutScanner periodically scans for timed-out transactions.
func (c *Coordinator) timeoutScanner(ctx context.Context) {
	defer c.wg.Done()

	ticker := time.NewTicker(c.config.TimeoutScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.scanTimeouts(ctx)
		}
	}
}

// scanTimeouts scans for and handles timed-out transactions.
// Uses FOR UPDATE SKIP LOCKED via store and CAS status updates for multi-instance safety.
// Releases locks for transactions that cannot be submitted to the worker queue.
func (c *Coordinator) scanTimeouts(ctx context.Context) {
	txs, err := c.store.ListTimedOutTransactions(ctx, c.config.InstanceID, c.config.LeaseDuration)
	if err != nil {
		c.log.ErrorContext(ctx, "failed to list timed out transactions",
			sagaFieldInstance, c.config.InstanceID,
			logger.FieldError, err,
		)
		return
	}

	for _, tx := range txs {
		txCtx := rehydrateSagaTraceContext(ctx, tx)
		ok, err := c.store.UpdateTransactionStatusCAS(txCtx, tx.ID, tx.Status, TxStatusCompensating, "transaction timeout")
		if err != nil {
			c.log.ErrorContext(txCtx, "failed to mark timed out transaction as compensating",
				appendTransactionLogFields([]any{
					sagaFieldInstance, c.config.InstanceID,
					logger.FieldError, err,
				}, tx)...,
			)
			c.store.ReleaseTransaction(txCtx, tx.ID, c.config.InstanceID)
			continue
		}
		if !ok {
			// CAS failed, another instance already handled this
			c.store.ReleaseTransaction(txCtx, tx.ID, c.config.InstanceID)
			continue
		}
		if !c.Submit(tx.ID) {
			// Queue full, release the lock so other instances can pick it up
			c.store.ReleaseTransaction(txCtx, tx.ID, c.config.InstanceID)
		}
	}
}

// driveTransaction drives a single transaction through its state machine.
// Uses distributed locking (ClaimTransaction/ReleaseTransaction) to ensure
// only one instance processes a given transaction at a time.
func (c *Coordinator) driveTransaction(ctx context.Context, txID string) {
	// Mark transaction as inactive when done
	defer func() {
		c.activeTxMu.Lock()
		delete(c.activeTx, txID)
		c.activeTxMu.Unlock()
	}()

	// Distributed claim: only one instance can drive this transaction
	claimed, err := c.store.ClaimTransaction(ctx, txID, c.config.InstanceID, c.config.LeaseDuration)
	if err != nil {
		c.log.ErrorContext(ctx, "failed to claim transaction",
			sagaFieldInstance, c.config.InstanceID,
			sagaFieldTxID, txID,
			logger.FieldError, err,
		)
		return
	}
	if !claimed {
		// Another instance is processing this transaction
		return
	}
	// Release lock when done
	defer c.store.ReleaseTransaction(ctx, txID, c.config.InstanceID)

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		default:
		}

		// Get current transaction state
		tx, err := c.store.GetTransaction(ctx, txID)
		if err != nil {
			c.log.ErrorContext(ctx, "failed to load transaction",
				sagaFieldInstance, c.config.InstanceID,
				sagaFieldTxID, txID,
				logger.FieldError, err,
			)
			return
		}
		if tx == nil {
			c.log.WarnContext(ctx, "transaction not found during drive loop",
				sagaFieldInstance, c.config.InstanceID,
				sagaFieldTxID, txID,
			)
			return
		}
		txCtx := rehydrateSagaTraceContext(ctx, tx)

		// Get all steps
		steps, err := c.store.GetSteps(txCtx, txID)
		if err != nil {
			c.log.ErrorContext(txCtx, "failed to load transaction steps",
				appendTransactionLogFields([]any{
					sagaFieldInstance, c.config.InstanceID,
					logger.FieldError, err,
				}, tx)...,
			)
			return
		}

		// Renew lease to prevent expiration during long-running steps
		renewed, err := c.store.ClaimTransaction(txCtx, txID, c.config.InstanceID, c.config.LeaseDuration)
		if err != nil || !renewed {
			// Lost the lock, another instance may have taken over
			c.log.WarnContext(txCtx, "failed to renew transaction lease, stopping drive loop",
				appendTransactionLogFields([]any{
					sagaFieldInstance, c.config.InstanceID,
				}, tx)...,
			)
			return
		}

		// Drive based on current status
		switch tx.Status {
		case TxStatusPending:
			// Use CAS to prevent concurrent status change
			ok, err := c.store.UpdateTransactionStatusCAS(txCtx, txID, TxStatusPending, TxStatusRunning, "")
			if err != nil || !ok {
				// CAS failed, another instance already transitioned
				return
			}
			continue

		case TxStatusRunning:
			// Execute next step
			shouldContinue := c.executeNextStep(txCtx, tx, steps)
			if !shouldContinue {
				return
			}
			continue

		case TxStatusCompensating:
			// Execute compensation
			c.executeCompensation(txCtx, tx, steps)
			return

		case TxStatusSucceeded, TxStatusFailed:
			// Terminal state
			return
		}
	}
}

// executeNextStep finds and executes the next pending step.
// Returns true if the loop should continue, false if it should exit.
func (c *Coordinator) executeNextStep(ctx context.Context, tx *Transaction, steps []*Step) bool {
	if transactionTimedOut(tx) {
		c.triggerCompensation(ctx, tx, errTransactionExecutionTimeout.Error())
		return true
	}

	// Find the first step that needs execution
	var currentStep *Step
	allSucceeded := true

	for i := range steps {
		step := steps[i]
		switch step.Status {
		case StepStatusPending:
			if currentStep == nil {
				currentStep = step
			}
			allSucceeded = false
		case StepStatusRunning, StepStatusPolling:
			// A step is in progress, wait for it
			currentStep = step
			allSucceeded = false
		case StepStatusSucceeded:
			// Continue to next
		case StepStatusFailed:
			// Trigger compensation and continue loop to handle compensating status
			c.triggerCompensation(ctx, tx, "step failed: "+step.Name)
			return true // Continue loop to process compensation
		default:
			allSucceeded = false
		}
	}

	// All steps succeeded - use CAS to prevent concurrent status overwrites
	if allSucceeded {
		if transactionTimedOut(tx) {
			c.triggerCompensation(ctx, tx, errTransactionExecutionTimeout.Error())
			return true
		}
		ok, err := c.store.UpdateTransactionStatusCAS(ctx, tx.ID, TxStatusRunning, TxStatusSucceeded, "")
		if err != nil {
			c.log.ErrorContext(ctx, "failed to mark transaction as succeeded",
				appendTransactionLogFields([]any{
					logger.FieldError, err,
				}, tx)...,
			)
		}
		if !ok {
			c.log.WarnContext(ctx, "transaction status already changed, skipping succeeded transition",
				appendTransactionLogFields(nil, tx)...,
			)
		}
		return false
	}

	// No step to execute
	if currentStep == nil {
		return false
	}

	// Handle step based on its current status
	switch currentStep.Status {
	case StepStatusPending:
		return c.executeStep(ctx, tx, currentStep, steps)

	case StepStatusRunning:
		// Step is running (recovery case), wait for it
		// For sync steps, we can re-execute (idempotent)
		if currentStep.Type == StepTypeSync {
			return c.executeStep(ctx, tx, currentStep, steps)
		}
		// For async steps, check if there's a poll task
		return c.waitForAsyncStep(ctx, tx, currentStep, steps)

	case StepStatusPolling:
		return c.waitForAsyncStep(ctx, tx, currentStep, steps)
	}

	return false
}

// executeStep executes a step based on its type.
func (c *Coordinator) executeStep(ctx context.Context, tx *Transaction, step *Step, steps []*Step) bool {
	switch step.Type {
	case StepTypeSync:
		return c.executeSyncStep(ctx, tx, step, steps)
	case StepTypeAsync:
		return c.executeAsyncStep(ctx, tx, step, steps)
	default:
		// Default to sync
		return c.executeSyncStep(ctx, tx, step, steps)
	}
}

// executeSyncStep executes a synchronous step.
func (c *Coordinator) executeSyncStep(ctx context.Context, tx *Transaction, step *Step, steps []*Step) bool {
	if transactionTimedOut(tx) {
		c.triggerCompensation(ctx, tx, errTransactionExecutionTimeout.Error())
		return true
	}

	execCtx, cancel := withTransactionExecutionContext(ctx, tx)
	defer cancel()

	err := c.executor.ExecuteStep(execCtx, tx, step, steps)
	if err == nil {
		if transactionTimedOut(tx) || transactionExecutionTimedOut(execCtx) {
			c.triggerCompensation(ctx, tx, errTransactionExecutionTimeout.Error())
			return true
		}

		// Success - update current step and continue
		writeCtx, writeCancel := durableStoreContext(ctx)
		defer writeCancel()
		if err := c.store.UpdateTransactionStep(writeCtx, tx.ID, step.Index+1); err != nil {
			c.log.ErrorContext(ctx, "failed to advance transaction step",
				appendStepLogFields(appendTransactionLogFields([]any{
					logger.FieldError, err,
				}, tx), step)...,
			)
		}
		return true
	}

	if errors.Is(err, errTransactionExecutionTimeout) {
		c.triggerCompensation(ctx, tx, errTransactionExecutionTimeout.Error())
		return true
	}

	// Check error type
	if errors.Is(err, ErrStepRetryable) {
		// Retry the step
		time.Sleep(time.Second) // Simple backoff
		return true
	}

	// Step failed, trigger compensation
	c.triggerCompensation(ctx, tx, err.Error())
	return true
}

// executeAsyncStep initiates an asynchronous step.
func (c *Coordinator) executeAsyncStep(ctx context.Context, tx *Transaction, step *Step, steps []*Step) bool {
	if transactionTimedOut(tx) {
		c.triggerCompensation(ctx, tx, errTransactionExecutionTimeout.Error())
		return true
	}

	execCtx, cancel := withTransactionExecutionContext(ctx, tx)
	defer cancel()

	err := c.executor.ExecuteAsyncStep(execCtx, tx, step, steps)
	if err == nil {
		if transactionTimedOut(tx) || transactionExecutionTimedOut(execCtx) {
			c.triggerCompensation(ctx, tx, errTransactionExecutionTimeout.Error())
			return true
		}
		// Async step submitted, wait for poll result
		return c.waitForAsyncStep(ctx, tx, step, steps)
	}

	if errors.Is(err, errTransactionExecutionTimeout) {
		c.triggerCompensation(ctx, tx, errTransactionExecutionTimeout.Error())
		return true
	}

	// Check error type
	if errors.Is(err, ErrStepRetryable) {
		time.Sleep(time.Second)
		return true
	}

	// Step failed, trigger compensation
	c.triggerCompensation(ctx, tx, err.Error())
	return true
}

// waitForAsyncStep waits for an async step to complete via polling.
func (c *Coordinator) waitForAsyncStep(ctx context.Context, tx *Transaction, step *Step, steps []*Step) bool {
	if transactionTimedOut(tx) {
		c.triggerCompensation(ctx, tx, errTransactionExecutionTimeout.Error())
		return true
	}

	txCtx, txCancel := withTransactionExecutionContext(ctx, tx)
	defer txCancel()

	// Register for poll notifications
	notifyCh := c.poller.RegisterNotify(tx.ID)
	defer c.poller.UnregisterNotify(tx.ID)

	// Re-read the latest step state after registering so a poll result that
	// arrived before channel registration does not strand the coordinator.
	latestStep, err := c.store.GetStep(txCtx, step.ID)
	if err != nil {
		c.log.ErrorContext(ctx, "failed to reload async step state",
			appendStepLogFields(appendTransactionLogFields([]any{
				logger.FieldError, err,
			}, tx), step)...,
		)
		return false
	}
	if latestStep == nil {
		c.triggerCompensation(ctx, tx, fmt.Sprintf("async step %s not found", step.ID))
		return true
	}
	switch latestStep.Status {
	case StepStatusSucceeded:
		if transactionTimedOut(tx) || transactionExecutionTimedOut(txCtx) {
			c.triggerCompensation(ctx, tx, errTransactionExecutionTimeout.Error())
			return true
		}
		writeCtx, writeCancel := durableStoreContext(ctx)
		defer writeCancel()
		if err := c.store.UpdateTransactionStep(writeCtx, tx.ID, latestStep.Index+1); err != nil {
			c.log.ErrorContext(ctx, "failed to advance transaction step after async success",
				appendStepLogFields(appendTransactionLogFields([]any{
					logger.FieldError, err,
				}, tx), latestStep)...,
			)
		}
		return true
	case StepStatusFailed:
		reason := latestStep.LastError
		if reason == "" {
			reason = "async step failed"
		}
		c.triggerCompensation(ctx, tx, reason)
		return true
	case StepStatusCompensating, StepStatusCompensated, StepStatusSkipped:
		return true
	}

	// Create a timeout context
	timeoutCtx, cancel := context.WithTimeout(txCtx, c.config.AsyncStepTimeout)
	defer cancel()

	select {
	case <-timeoutCtx.Done():
		if transactionExecutionTimedOut(timeoutCtx) || transactionTimedOut(tx) {
			c.triggerCompensation(ctx, tx, errTransactionExecutionTimeout.Error())
			return true // Continue loop to process compensation
		}
		// Timeout waiting for async step
		c.triggerCompensation(ctx, tx, "timeout waiting for async step")
		return true // Continue loop to process compensation

	case <-c.stopCh:
		return false

	case result := <-notifyCh:
		switch result {
		case PollResultSuccess:
			// Step succeeded, continue to next step
			if err := c.store.UpdateTransactionStep(ctx, tx.ID, step.Index+1); err != nil {
				c.log.ErrorContext(ctx, "failed to advance transaction step after poll success",
					appendStepLogFields(appendTransactionLogFields([]any{
						logger.FieldError, err,
					}, tx), step)...,
				)
			}
			return true

		case PollResultFailure, PollResultTimeout:
			// Step failed, trigger compensation
			c.triggerCompensation(ctx, tx, "async step failed or timed out")
			return true // Continue loop to process compensation

		case PollResultError:
			// Error occurred, retry
			return true
		}
	}

	return false
}

// triggerCompensation initiates the compensation process using CAS to prevent
// concurrent status overwrites by multiple instances.
func (c *Coordinator) triggerCompensation(ctx context.Context, tx *Transaction, reason string) {
	writeCtx, cancel := durableStoreContext(ctx)
	defer cancel()

	ok, err := c.store.UpdateTransactionStatusCAS(writeCtx, tx.ID, TxStatusRunning, TxStatusCompensating, reason)
	if err != nil {
		c.log.ErrorContext(ctx, "failed to switch transaction to compensating",
			appendTransactionLogFields([]any{
				logger.FieldError, err,
				"reason", reason,
			}, tx)...,
		)
	}
	if !ok {
		c.log.WarnContext(ctx, "transaction status already changed, skipping compensation trigger",
			appendTransactionLogFields([]any{
				"reason", reason,
			}, tx)...,
		)
	}
}

// executeCompensation executes compensation for all completed steps in reverse order.
func (c *Coordinator) executeCompensation(ctx context.Context, tx *Transaction, steps []*Step) {
	// Find all steps that need compensation. A step left in compensating after a
	// crash must be retried during recovery, not skipped.
	var toCompensate []*Step
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step.Status == StepStatusSucceeded || step.Status == StepStatusCompensating {
			toCompensate = append(toCompensate, step)
		}
	}

	// Execute compensation for each step
	allCompensated := true
	for _, step := range toCompensate {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		default:
		}

		err := c.executor.CompensateStep(ctx, tx, step, steps)
		if err != nil {
			c.log.ErrorContext(ctx, "step compensation failed",
				appendStepLogFields(appendTransactionLogFields([]any{
					logger.FieldError, err,
				}, tx), step)...,
			)
			allCompensated = false
			// Continue to try other compensations
		}
	}

	// Mark skipped steps
	for _, step := range steps {
		if step.Status == StepStatusPending {
			if err := c.store.UpdateStepStatus(ctx, step.ID, StepStatusSkipped, ""); err != nil {
				c.log.ErrorContext(ctx, "failed to mark step as skipped",
					appendStepLogFields(appendTransactionLogFields([]any{
						logger.FieldError, err,
					}, tx), step)...,
				)
			}
		}
	}

	// Update transaction status
	if allCompensated {
		if err := c.store.UpdateTransactionStatus(ctx, tx.ID, TxStatusFailed, "compensation completed"); err != nil {
			c.log.ErrorContext(ctx, "failed to mark transaction as failed after compensation",
				appendTransactionLogFields([]any{
					logger.FieldError, err,
				}, tx)...,
			)
		}
	} else {
		// Some compensations failed, needs manual intervention
		if err := c.store.UpdateTransactionStatus(ctx, tx.ID, TxStatusFailed, "compensation partially failed, manual intervention required"); err != nil {
			c.log.ErrorContext(ctx, "failed to persist partially failed compensation state",
				appendTransactionLogFields([]any{
					logger.FieldError, err,
				}, tx)...,
			)
		}
	}
}
