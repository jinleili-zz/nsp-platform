// Package main provides the HTTP server entry point.
package main

import (
	"context"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paic/nsp-common/pkg/auth"
	"github.com/paic/nsp-common/pkg/logger"
	"github.com/paic/nsp-common/pkg/trace"
	"github.com/paic/nsp-demo/internal/handler"
	"github.com/paic/nsp-demo/internal/middleware"
)

func main() {
	// Parse command line flags
	addr := flag.String("addr", ":8080", "HTTP server address")
	dev := flag.Bool("dev", false, "Enable development mode")
	logFile := flag.String("log-file", "", "Log file path (enables multi-output: console + file)")
	flag.Parse()

	// Initialize logger
	var cfg *logger.Config
	if *dev {
		cfg = logger.DevelopmentConfig("nsp-demo")
	} else if *logFile != "" {
		// Multi-output mode: console (human readable) + file (JSON for aggregation)
		cfg = logger.MultiOutputConfig("nsp-demo", *logFile)
	} else {
		cfg = logger.DefaultConfig("nsp-demo")
	}

	if err := logger.Init(cfg); err != nil {
		panic("failed to initialize logger: " + err.Error())
	}
	defer logger.Sync()

	// Set Gin mode based on dev flag
	if *dev {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// Create Gin engine without default middleware
	r := gin.New()

	// Get instance ID for trace middleware (typically k8s pod name from HOSTNAME)
	instanceId := trace.GetInstanceId()

	// Setup AK/SK authentication
	credStore := auth.NewMemoryStore([]*auth.Credential{
		{
			AccessKey: "test-ak",
			SecretKey: "test-sk-1234567890abcdef",
			Label:     "test-client",
			Enabled:   true,
		},
		{
			AccessKey: "demo-ak",
			SecretKey: "demo-sk-abcdef1234567890",
			Label:     "demo-client",
			Enabled:   true,
		},
	})
	nonceStore := auth.NewMemoryNonceStore()
	verifier := auth.NewVerifier(credStore, nonceStore, nil)

	// Apply middleware (order matters)
	// 1. Recovery - catch panics
	r.Use(middleware.GinRecovery())
	// 2. Trace - inject trace_id and span_id (using new B3 standard trace module)
	r.Use(trace.TraceMiddleware(instanceId))
	// 3. Logger - log requests
	r.Use(middleware.GinLogger())
	// 4. AK/SK Auth - authenticate requests (skip health endpoint)
	r.Use(auth.AKSKAuthMiddleware(verifier, &auth.MiddlewareOption{
		Skipper: auth.NewSkipperByPath("/health"),
	}))

	// Register routes
	r.GET("/health", handler.Health)
	r.GET("/hello", handler.Hello)
	r.GET("/user", handler.User)
	r.GET("/error", handler.Error)
	r.GET("/panic", handler.Panic)

	// Create server
	srv := &http.Server{
		Addr:         *addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		logger.Info("server starting", "addr", *addr, "log_file", *logFile)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server failed to start", logger.FieldError, err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("server shutting down")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server forced to shutdown", logger.FieldError, err)
	}

	logger.Info("server stopped")
}
