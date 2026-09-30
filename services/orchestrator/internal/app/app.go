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
	orchestratorv1 "github.com/caduc/ai-agent-orchestrator/contracts/gen/go/orchestrator/v1"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/config"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/service"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
)

func Run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})).With("service", "orchestrator", "version", cfg.ServiceVersion)
	startupCtx, cancelStartup := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancelStartup()

	pool, err := connectPostgres(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	storage := store.New(pool, cfg.SubtaskTimeout)
	if err := storage.Migrate(startupCtx); err != nil {
		return err
	}

	natsConnection, err := nats.Connect(cfg.NATSURL, nats.Name("orchestrator"), nats.Timeout(5*time.Second), nats.RetryOnFailedConnect(false), nats.MaxReconnects(-1))
	if err != nil {
		return fmt.Errorf("connect NATS: %w", err)
	}
	defer natsConnection.Close()
	jetStream, err := natsConnection.JetStream(nats.PublishAsyncMaxPending(256))
	if err != nil {
		return fmt.Errorf("open JetStream context: %w", err)
	}

	llmConnection, err := grpc.NewClient(cfg.LLMGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect LLM Gateway: %w", err)
	}
	defer llmConnection.Close()
	llmClient := llmv1.NewLLMGatewayServiceClient(llmConnection)

	runtime := service.NewRuntime(storage, llmClient, jetStream, logger, cfg)
	if err := runtime.Start(ctx); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.GRPCAddr, err)
	}
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(1<<20),
		grpc.MaxSendMsgSize(4<<20),
	)
	orchestratorv1.RegisterOrchestratorServiceServer(grpcServer, service.NewServer(storage, logger, cfg.ServiceVersion, cfg.TaskTimeout))
	reflection.Register(grpcServer)

	serveErrors := make(chan error, 1)
	go func() {
		logger.Info("gRPC server listening", "address", cfg.GRPCAddr)
		serveErrors <- grpcServer.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve gRPC: %w", err)
		}
	}

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(cfg.ShutdownGracePeriod):
		grpcServer.Stop()
	}
	runtime.Wait()
	if err := natsConnection.Drain(); err != nil {
		logger.Warn("NATS drain failed", "error", err)
	}
	return nil
}

func connectPostgres(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	backoff := 250 * time.Millisecond
	for {
		pool, err := pgxpool.New(ctx, databaseURL)
		if err == nil {
			err = pool.Ping(ctx)
			if err == nil {
				return pool, nil
			}
			pool.Close()
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connect PostgreSQL: %w", ctx.Err())
		case <-time.After(backoff):
			if backoff < 2*time.Second {
				backoff *= 2
			}
		}
	}
}
