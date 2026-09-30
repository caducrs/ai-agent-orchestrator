package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

type state struct {
	requests atomic.Int64
	errors   atomic.Int64
}

func Run(ctx context.Context) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "demo-app")
	current := &state{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok","service":"demo-app"}`))
	})
	mux.HandleFunc("GET /metrics", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(writer, "demo_http_requests_total %d\ndemo_http_errors_total %d\n", current.requests.Load(), current.errors.Load())
	})
	mux.HandleFunc("GET /simulate", func(writer http.ResponseWriter, request *http.Request) {
		current.requests.Add(1)
		current.errors.Add(1)
		logger.ErrorContext(request.Context(), "HTTP 500 caused by nil dependency", "status", 500, "request_id", strconv.FormatInt(time.Now().UnixNano(), 36))
		http.Error(writer, "internal server error", http.StatusInternalServerError)
	})
	server := &http.Server{Addr: env("HTTP_ADDR", ":8080"), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	errorsChannel := make(chan error, 1)
	go func() { errorsChannel <- server.ListenAndServe() }()
	go sendLogs(ctx, logger, current)
	select {
	case <-ctx.Done():
	case err := <-errorsChannel:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

func sendLogs(ctx context.Context, logger *slog.Logger, current *state) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		current.requests.Add(1)
		current.errors.Add(1)
		line := fmt.Sprintf("level=error status=500 service=demo-app msg=%q", "database dependency timeout while handling request")
		logger.Error("database dependency timeout while handling request", "status", 500)
		pushLoki(ctx, line)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func pushLoki(ctx context.Context, line string) {
	payload := map[string]any{"streams": []map[string]any{{"stream": map[string]string{"job": "demo-app", "service": "demo-app"}, "values": [][]string{{strconv.FormatInt(time.Now().UnixNano(), 10), line}}}}}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, env("LOKI_PUSH_URL", "http://loki:3100/loki/api/v1/push"), bytes.NewReader(raw))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err == nil {
		response.Body.Close()
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
