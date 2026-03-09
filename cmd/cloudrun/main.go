package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	cmdinternal "github.com/spacelift-io/awsautoscalr/cmd/internal"
	spaceliftinternal "github.com/spacelift-io/awsautoscalr/internal"
)

// Google Cloud Run entry point for the Spacelift autoscaler.
// This implements an HTTP server that responds to Cloud Scheduler triggers.
//
// Cloud Run expects:
// - HTTP server listening on PORT env var (default 8080)
// - Health check endpoint
// - Graceful shutdown handling

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Parse config at startup - fail fast on misconfiguration
	var cfg spaceliftinternal.RuntimeConfig
	if err := cfg.Parse(spaceliftinternal.PlatformGCP); err != nil {
		logger.Error("failed to parse configuration", "error", err)
		os.Exit(1)
	}

	// Create a context that listens for shutdown signals
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()

	// Scale endpoint - triggered by Cloud Scheduler
	mux.HandleFunc("/scale", func(w http.ResponseWriter, r *http.Request) {
		handleScale(w, r, logger, &cfg)
	})

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, logger, http.StatusOK, map[string]string{
			"status": "healthy",
		})
	})

	// Root endpoint - service info
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, logger, http.StatusOK, map[string]string{
			"service": "Spacelift Autoscaler Cloud Run",
			"version": "1.0.0",
		})
	})

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute,
		// Propagate cancellation context to all requests
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	// Start server in a goroutine so we can listen for shutdown signals
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("Starting Cloud Run HTTP server", "port", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Wait for shutdown signal or server error
	select {
	case err := <-serverErr:
		logger.Error("server error", "error", err)
		os.Exit(1)
	case <-ctx.Done():
		logger.Info("Shutdown signal received, starting graceful shutdown")
		stop() // Stop receiving more signals
	}

	// Create a context with timeout for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Attempt graceful shutdown
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("forced shutdown due to timeout", "error", err)
		os.Exit(1)
	}

	logger.Info("Server stopped gracefully")
}

func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Warn("failed to encode response", "error", err)
	}
}

func handleScale(w http.ResponseWriter, r *http.Request, logger *slog.Logger, cfg *spaceliftinternal.RuntimeConfig) {
	// Validate HTTP method - Cloud Scheduler sends POST requests
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, logger, http.StatusMethodNotAllowed, map[string]string{
			"error": "Method not allowed. Only POST requests are accepted.",
		})
		return
	}

	startTime := time.Now()
	ctx := r.Context()

	// Extract trace ID if present (Cloud Run sets this header)
	traceID := r.Header.Get("X-Cloud-Trace-Context")
	if traceID != "" {
		logger = logger.With("trace_id", traceID)
	}

	logger.Info("Autoscaler invoked")

	if err := cmdinternal.Handle(ctx, logger, cfg, spaceliftinternal.NewGCPController); err != nil {
		logger.Error("autoscaling failed", "error", err, "duration", time.Since(startTime))

		writeJSON(w, logger, http.StatusInternalServerError, map[string]string{
			"error": "autoscaling failed - see logs for details",
		})
		return
	}

	logger.Info("Autoscaler completed successfully", "duration", time.Since(startTime))

	writeJSON(w, logger, http.StatusOK, map[string]any{
		"status":   "success",
		"duration": time.Since(startTime).String(),
	})
}
