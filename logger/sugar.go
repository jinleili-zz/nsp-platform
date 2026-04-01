// Package logger provides a unified logging module for NSP platform microservices.
// sugar.go defines the SugaredLogger interface and its zap-backed implementation.
package logger

import (
	"context"

	"go.uber.org/zap"
)

// SugaredLogger provides printf-style logging methods on top of Logger.
type SugaredLogger interface {
	// Debugf logs a formatted message at debug level.
	Debugf(format string, args ...any)

	// Infof logs a formatted message at info level.
	Infof(format string, args ...any)

	// Warnf logs a formatted message at warn level.
	Warnf(format string, args ...any)

	// Errorf logs a formatted message at error level.
	Errorf(format string, args ...any)

	// Fatalf logs a formatted message and exits the process.
	Fatalf(format string, args ...any)

	// DebugContextf logs a formatted message at debug level with context fields.
	DebugContextf(ctx context.Context, format string, args ...any)

	// InfoContextf logs a formatted message at info level with context fields.
	InfoContextf(ctx context.Context, format string, args ...any)

	// WarnContextf logs a formatted message at warn level with context fields.
	WarnContextf(ctx context.Context, format string, args ...any)

	// ErrorContextf logs a formatted message at error level with context fields.
	ErrorContextf(ctx context.Context, format string, args ...any)

	// With returns a new SugaredLogger with additional key-value fields attached.
	With(keysAndValues ...any) SugaredLogger
}

// zapSugaredLogger implements SugaredLogger with zap.SugaredLogger.
type zapSugaredLogger struct {
	slogger *zap.SugaredLogger
}

// Debugf implements SugaredLogger.
func (l *zapSugaredLogger) Debugf(format string, args ...any) {
	l.slogger.Debugf(format, args...)
}

// Infof implements SugaredLogger.
func (l *zapSugaredLogger) Infof(format string, args ...any) {
	l.slogger.Infof(format, args...)
}

// Warnf implements SugaredLogger.
func (l *zapSugaredLogger) Warnf(format string, args ...any) {
	l.slogger.Warnf(format, args...)
}

// Errorf implements SugaredLogger.
func (l *zapSugaredLogger) Errorf(format string, args ...any) {
	l.slogger.Errorf(format, args...)
}

// Fatalf implements SugaredLogger.
func (l *zapSugaredLogger) Fatalf(format string, args ...any) {
	l.slogger.Errorf(format, args...)
	_ = l.slogger.Sync()
	exitFunc(1)
}

// DebugContextf implements SugaredLogger.
func (l *zapSugaredLogger) DebugContextf(ctx context.Context, format string, args ...any) {
	l.withContext(ctx).Debugf(format, args...)
}

// InfoContextf implements SugaredLogger.
func (l *zapSugaredLogger) InfoContextf(ctx context.Context, format string, args ...any) {
	l.withContext(ctx).Infof(format, args...)
}

// WarnContextf implements SugaredLogger.
func (l *zapSugaredLogger) WarnContextf(ctx context.Context, format string, args ...any) {
	l.withContext(ctx).Warnf(format, args...)
}

// ErrorContextf implements SugaredLogger.
func (l *zapSugaredLogger) ErrorContextf(ctx context.Context, format string, args ...any) {
	l.withContext(ctx).Errorf(format, args...)
}

// With implements SugaredLogger.
func (l *zapSugaredLogger) With(keysAndValues ...any) SugaredLogger {
	return &zapSugaredLogger{slogger: l.slogger.With(keysAndValues...)}
}

// withContext returns a SugaredLogger enriched with context trace fields.
func (l *zapSugaredLogger) withContext(ctx context.Context) *zap.SugaredLogger {
	fields := extractContextFields(ctx)
	if len(fields) == 0 {
		return l.slogger
	}
	return l.slogger.With(fields...)
}
