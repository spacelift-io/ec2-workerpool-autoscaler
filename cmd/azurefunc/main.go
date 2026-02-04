package main

import (
	"context"
	"encoding/json"
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

// Azure Functions custom handler for the Spacelift autoscaler.
// This implements the Azure Functions custom handler protocol, which expects
// an HTTP server listening on the port specified by FUNCTIONS_CUSTOMHANDLER_PORT.
//
// For timer triggers, Azure Functions sends a POST request to /{functionName}
// with invocation metadata in the request body.

// migrateDeprecatedEnvVars checks for deprecated environment variable names
// and copies their values to the new names, logging deprecation warnings.
func migrateDeprecatedEnvVars(logger *slog.Logger) {
	deprecatedVars := []struct {
		oldName string
		newName string
	}{
		{"AZURE_AUTOSCALING_MIN_SIZE", "AUTOSCALING_MIN_SIZE"},
		{"AZURE_AUTOSCALING_MAX_SIZE", "AUTOSCALING_MAX_SIZE"},
		{"AZURE_SECRET_NAME", "SPACELIFT_API_KEY_SECRET_NAME"},
		{"AUTOSCALING_GROUP_ARN", "AZURE_VMSS_RESOURCE_ID"},
	}

	for _, v := range deprecatedVars {
		oldVal := os.Getenv(v.oldName)
		if oldVal == "" {
			continue
		}

		newVal := os.Getenv(v.newName)
		if newVal == "" {
			os.Setenv(v.newName, oldVal)
			logger.Warn("deprecated environment variable used",
				"old", v.oldName,
				"new", v.newName,
				"action", "Please update to use the new variable name")
		} else {
			logger.Warn("deprecated environment variable ignored",
				"old", v.oldName,
				"new", v.newName,
				"reason", "new variable is already set")
		}
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// Migrate deprecated environment variables before parsing config
	migrateDeprecatedEnvVars(logger)

	// Parse config at startup - fail fast on misconfiguration
	var cfg spaceliftinternal.RuntimeConfig
	if err := cfg.Parse(spaceliftinternal.PlatformAzure); err != nil {
		logger.Error("failed to parse configuration", "error", err)
		os.Exit(1)
	}

	// Create a context that listens for shutdown signals
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	port := os.Getenv("FUNCTIONS_CUSTOMHANDLER_PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/AutoscalerTimer", func(w http.ResponseWriter, r *http.Request) {
		handleAutoscaler(w, r, logger, &cfg)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Spacelift Autoscaler Azure Function"))
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
		logger.Info("Starting Azure Functions custom handler", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

func handleAutoscaler(w http.ResponseWriter, r *http.Request, logger *slog.Logger, cfg *spaceliftinternal.RuntimeConfig) {
	startTime := time.Now()
	ctx := r.Context()

	invocationID := r.Header.Get("x-azure-functions-invocationid")
	if invocationID != "" {
		logger = logger.With("invocation_id", invocationID)
	}

	logger.Info("Autoscaler invoked")

	if err := cmdinternal.Handle(ctx, logger, cfg, spaceliftinternal.NewAzureController); err != nil {
		logger.Error("autoscaling failed", "error", err, "duration", time.Since(startTime))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	logger.Info("Autoscaler completed successfully", "duration", time.Since(startTime))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"duration": time.Since(startTime).String(),
	})
}
