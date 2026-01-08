package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	cmdinternal "github.com/spacelift-io/awsautoscalr/cmd/internal"
)

// Azure Functions custom handler for the Spacelift autoscaler.
// This implements the Azure Functions custom handler protocol, which expects
// an HTTP server listening on the port specified by FUNCTIONS_CUSTOMHANDLER_PORT.
//
// For timer triggers, Azure Functions sends a POST request to /{functionName}
// with invocation metadata in the request body.

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	port := os.Getenv("FUNCTIONS_CUSTOMHANDLER_PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/AutoscalerTimer", func(w http.ResponseWriter, r *http.Request) {
		handleAutoscaler(w, r, logger)
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
	}

	logger.Info("Starting Azure Functions custom handler", "port", port)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

func handleAutoscaler(w http.ResponseWriter, r *http.Request, logger *slog.Logger) {
	startTime := time.Now()
	ctx := r.Context()

	invocationID := r.Header.Get("x-azure-functions-invocationid")
	if invocationID != "" {
		logger = logger.With("invocation_id", invocationID)
	}

	logger.Info("Autoscaler invoked")

	if err := cmdinternal.Handle(ctx, logger); err != nil {
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
