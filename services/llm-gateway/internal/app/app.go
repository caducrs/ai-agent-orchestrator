package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	llmv1 "github.com/caduc/ai-agent-orchestrator/contracts/gen/go/llm/v1"
	"github.com/caduc/ai-agent-orchestrator/services/llm-gateway/internal/config"
	"github.com/caduc/ai-agent-orchestrator/services/llm-gateway/internal/observability"
	"github.com/caduc/ai-agent-orchestrator/services/llm-gateway/internal/provider"
	"github.com/caduc/ai-agent-orchestrator/services/llm-gateway/internal/service"
	"github.com/caduc/ai-agent-orchestrator/services/llm-gateway/internal/store"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func Run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "llm-gateway", "version", cfg.ServiceVersion)
	telemetryShutdown, err := observability.Setup(ctx, "llm-gateway", cfg.ServiceVersion, os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if err != nil {
		return fmt.Errorf("setup telemetry: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = telemetryShutdown(shutdownCtx)
	}()
	startupCtx, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancel()
	pool, err := store.Connect(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	storage := store.New(pool)
	if err := storage.Migrate(startupCtx); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	server := grpc.NewServer(grpc.MaxRecvMsgSize(4<<20), grpc.MaxSendMsgSize(4<<20), grpc.StatsHandler(otelgrpc.NewServerHandler()))
	llmv1.RegisterLLMGatewayServiceServer(server, service.New(storage, provider.New(cfg), logger))
	reflection.Register(server)
	errorsChannel := make(chan error, 1)
	go func() {
		logger.Info("gRPC server listening", "address", cfg.GRPCAddr, "provider", cfg.Provider)
		errorsChannel <- server.Serve(listener)
	}()
	select {
	case <-ctx.Done():
	case serveErr := <-errorsChannel:
		if serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			return serveErr
		}
	}
	stopped := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(30 * time.Second):
		server.Stop()
	}
	return nil
}
