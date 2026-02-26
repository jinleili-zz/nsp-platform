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

	"github.com/yourorg/nsp-common/pkg/logger"
	"github.com/yourorg/nsp-demo/internal/handler"
	"github.com/yourorg/nsp-demo/internal/middleware"
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

	// Build middleware chain
	mux := http.NewServeMux()

	// Register routes
	mux.HandleFunc("GET /health", handler.Health)
	mux.HandleFunc("GET /hello", handler.Hello)
	mux.HandleFunc("GET /user", handler.User)
	mux.HandleFunc("GET /error", handler.Error)
	mux.HandleFunc("GET /panic", handler.Panic)

	// Apply middleware (order matters: outermost first)
	var h http.Handler = mux
	h = middleware.Logger(h)
	h = middleware.Trace(h)
	h = middleware.Recovery(h)

	// Create server
	srv := &http.Server{
		Addr:         *addr,
		Handler:      h,
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
