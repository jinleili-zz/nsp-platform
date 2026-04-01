// Package main demonstrates the NSP logger module.
package main

import (
	"context"

	"github.com/jinleili-zz/nsp-platform/logger"
)

func main() {
	cfg := logger.DevelopmentConfig("logger-example")
	if err := logger.Init(cfg); err != nil {
		panic("failed to initialize logger: " + err.Error())
	}
	defer logger.Sync()

	ctx := logger.ContextWithTraceID(context.Background(), "trace-example-001")
	ctx = logger.ContextWithSpanID(ctx, "span-example-001")

	orderID := "ORD-20260401-001"
	userID := "U-1001"
	amount := 199.90

	// key-value 风格：message 固定，业务数据通过字段输出。
	logger.Info("create order request received",
		"order_id", orderID,
		"user_id", userID,
		"amount", amount,
	)

	// 带 context 的 key-value 风格：自动附加 trace_id / span_id。
	logger.InfoContext(ctx, "order validation passed",
		"order_id", orderID,
		"module", "checkout",
	)

	// printf 风格：动态内容直接格式化到 message 中。
	logger.Infof("order %s created for user %s, amount=%.2f", orderID, userID, amount)

	// 结构化字段：适合把业务上下文作为 key-value 输出。
	logger.Info("payment request sent",
		"module", "payment",
		"order_id", orderID,
		"channel", "wechat-pay",
	)

	// 带 context 的 printf 风格：格式化 message，同时保留 trace 字段。
	logger.InfoContextf(ctx, "order %s finished with status %s", orderID, "success")
}
