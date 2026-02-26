// Package logger provides a unified logging module for NSP platform microservices.
// fields.go defines standard field key constants for consistent logging across services.
package logger

// Standard field key constants for structured logging.
// Using constants ensures consistency and prevents typos across all microservices.
const (
	// FieldService is the key for service name field.
	// Example: "nsp-order", "nsp-user", "nsp-gateway"
	FieldService = "service"

	// FieldTraceID is the key for distributed tracing trace ID.
	// Used to correlate logs across multiple services in a single request chain.
	FieldTraceID = "trace_id"

	// FieldSpanID is the key for distributed tracing span ID.
	// Used to identify a specific operation within a trace.
	FieldSpanID = "span_id"

	// FieldUserID is the key for authenticated user identifier.
	// Used to track which user initiated the operation.
	FieldUserID = "user_id"

	// FieldRequestID is the key for request identifier.
	// Used to identify a specific request within a service.
	FieldRequestID = "request_id"

	// FieldModule is the key for logical module name within a service.
	// Example: "order-service", "payment-handler", "user-repository"
	FieldModule = "module"

	// FieldMethod is the key for the method or function being executed.
	// Example: "CreateOrder", "ProcessPayment", "GetUserByID"
	FieldMethod = "method"

	// FieldPath is the key for HTTP request path or RPC method path.
	// Example: "/api/v1/orders", "/users/{id}"
	FieldPath = "path"

	// FieldCode is the key for response code or error code.
	// Example: 200, 500, "ORDER_NOT_FOUND"
	FieldCode = "code"

	// FieldLatencyMS is the key for operation latency in milliseconds.
	// Used for performance monitoring and SLA tracking.
	FieldLatencyMS = "latency_ms"

	// FieldError is the key for error message or error details.
	// Used to attach error information to log entries.
	FieldError = "error"

	// FieldPeerAddr is the key for remote peer address.
	// Example: client IP address, downstream service address.
	FieldPeerAddr = "peer_addr"
)
