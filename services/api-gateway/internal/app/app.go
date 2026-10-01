package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	orchestratorv1 "github.com/caduc/ai-agent-orchestrator/contracts/gen/go/orchestrator/v1"
	"github.com/caduc/ai-agent-orchestrator/services/api-gateway/internal/auth"
	"github.com/caduc/ai-agent-orchestrator/services/api-gateway/internal/config"
	"github.com/caduc/ai-agent-orchestrator/services/api-gateway/internal/httpapi"
	"github.com/caduc/ai-agent-orchestrator/services/api-gateway/internal/observability"
	"github.com/caduc/ai-agent-orchestrator/services/api-gateway/internal/ratelimit"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func Run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "api-gateway", "version", cfg.ServiceVersion)
	telemetryShutdown, err := observability.Setup(ctx, "api-gateway", cfg.ServiceVersion, os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if err != nil {
		return fmt.Errorf("setup telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = telemetryShutdown(shutdownCtx)
	}()
	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword})
	defer redisClient.Close()
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := waitRedis(startupCtx, redisClient); err != nil {
		return err
	}
	connection, err := grpc.NewClient(cfg.OrchestratorGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return fmt.Errorf("connect orchestrator: %w", err)
	}
	defer connection.Close()
	client := orchestratorv1.NewOrchestratorServiceClient(connection)
	api := httpapi.New(client, auth.New(cfg.DevelopmentToken, cfg.DevelopmentSubject), ratelimit.New(redisClient, cfg.RatePerSecond, cfg.RateBurst), logger, cfg.RequestTimeout, cfg.SSEHeartbeat, cfg.SSEBuffer)
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: otelhttp.NewHandler(api.Handler(), "api-gateway.http"), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	errorsChannel := make(chan error, 1)
	go func() {
		logger.Info("HTTP server listening", "address", cfg.HTTPAddr)
		errorsChannel <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case serveErr := <-errorsChannel:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	return server.Shutdown(shutdownCtx)
}

func waitRedis(ctx context.Context, client *redis.Client) error {
	backoff := 250 * time.Millisecond
	for {
		if err := client.Ping(ctx).Err(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("connect Redis: %w", ctx.Err())
		case <-time.After(backoff):
			if backoff < 2*time.Second {
				backoff *= 2
			}
		}
	}
}
