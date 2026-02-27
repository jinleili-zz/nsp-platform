// generator.go - TraceID / SpanID 生成
// Package trace 提供分布式链路追踪功能
package trace

import (
	"crypto/rand"
	"encoding/hex"
	"os"
)

// NewTraceID 生成 32 位 hex 字符串的 TraceID（16字节随机数）
// 格式与 B3 标准一致（128bit）
// 示例：4bf92f3577b34da6a3ce929d0e0e4736
func NewTraceID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		// 在极端情况下使用时间戳和进程 ID 作为 fallback
		// 但这种情况几乎不会发生
		panic("crypto/rand read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// NewSpanId 生成 16 位 hex 字符串的 SpanId（8字节随机数）
// 格式与 B3 标准一致（64bit）
// 示例：00f067aa0ba902b7
func NewSpanId() string {
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		panic("crypto/rand read failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// GetInstanceId 读取当前实例标识
// 优先读取环境变量 HOSTNAME（k8s pod 名称）
// HOSTNAME 为空时 fallback 到 os.Hostname()
// 两者都失败时返回 "unknown"
func GetInstanceId() string {
	// 优先读取 HOSTNAME 环境变量
	hostname := os.Getenv("HOSTNAME")
	if hostname != "" {
		return hostname
	}

	// fallback 到 os.Hostname()
	hostname, err := os.Hostname()
	if err == nil && hostname != "" {
		return hostname
	}

	// 都失败时返回 unknown
	return "unknown"
}
