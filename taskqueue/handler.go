package taskqueue

import "context"

// HandlerFunc is the function signature for task handlers.
// It receives a decoded TaskPayload and returns a TaskResult on success.
type HandlerFunc func(ctx context.Context, payload *TaskPayload) (*TaskResult, error)
