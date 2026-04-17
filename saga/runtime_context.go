package saga

import (
	"context"
	"errors"
	"time"
)

const sagaStoreWriteTimeout = 5 * time.Second

var errTransactionExecutionTimeout = errors.New("transaction timeout")

func withTransactionExecutionContext(ctx context.Context, tx *Transaction) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tx == nil || tx.TimeoutAt == nil {
		return ctx, func() {}
	}

	deadline := tx.TimeoutAt.UTC()
	if parentDeadline, ok := ctx.Deadline(); ok && !deadline.Before(parentDeadline) {
		return ctx, func() {}
	}

	return context.WithDeadlineCause(ctx, deadline, errTransactionExecutionTimeout)
}

func transactionTimedOut(tx *Transaction) bool {
	return tx != nil && tx.TimeoutAt != nil && !time.Now().Before(tx.TimeoutAt.UTC())
}

func transactionExecutionTimedOut(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errTransactionExecutionTimeout)
}

func durableStoreContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), sagaStoreWriteTimeout)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), sagaStoreWriteTimeout)
}
