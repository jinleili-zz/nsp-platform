module github.com/yourorg/nsp-demo

go 1.25.6

require github.com/yourorg/nsp-common v0.0.0

require (
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.1 // indirect
	go.uber.org/zap/exp v0.3.0 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace github.com/yourorg/nsp-common => ../nsp-common
