package asynqbroker

import (
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/jinleili-zz/nsp-platform/logger"
)

const (
	componentAsynqBroker = "asynqbroker"
	componentAsynq       = "asynq"
)

type BrokerConfig struct {
	Logger logger.Logger
}

type InspectorConfig struct {
	Logger logger.Logger
}

func resolveRuntimeLogger(log logger.Logger) logger.Logger {
	if log == nil {
		log = logger.Platform()
	}
	return log.With(logger.FieldComponent, componentAsynqBroker)
}

func resolveFrameworkLogger(runtime logger.Logger, explicit asynq.Logger) asynq.Logger {
	if explicit != nil {
		return explicit
	}
	return &asynqLoggerAdapter{
		log: runtime.With(logger.FieldComponent, componentAsynq),
	}
}

type asynqLoggerAdapter struct {
	log logger.Logger
}

func (l *asynqLoggerAdapter) Debug(args ...interface{}) {
	l.log.Debug(fmt.Sprint(args...))
}

func (l *asynqLoggerAdapter) Info(args ...interface{}) {
	l.log.Info(fmt.Sprint(args...))
}

func (l *asynqLoggerAdapter) Warn(args ...interface{}) {
	l.log.Warn(fmt.Sprint(args...))
}

func (l *asynqLoggerAdapter) Error(args ...interface{}) {
	l.log.Error(fmt.Sprint(args...))
}

func (l *asynqLoggerAdapter) Fatal(args ...interface{}) {
	l.log.Fatal(fmt.Sprint(args...))
}
