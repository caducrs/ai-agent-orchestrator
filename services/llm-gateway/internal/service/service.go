package service

import (
	"context"
	"errors"
	"log/slog"

	llmv1 "github.com/caduc/ai-agent-orchestrator/contracts/gen/go/llm/v1"
	"github.com/caduc/ai-agent-orchestrator/services/llm-gateway/internal/provider"
	"github.com/caduc/ai-agent-orchestrator/services/llm-gateway/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	llmv1.UnimplementedLLMGatewayServiceServer
	store    *store.Store
	provider *provider.Client
	logger   *slog.Logger
}

func New(storage *store.Store, client *provider.Client, logger *slog.Logger) *Server {
	return &Server{store: storage, provider: client, logger: logger}
}

func (s *Server) OpenTaskBudget(ctx context.Context, request *llmv1.OpenTaskBudgetRequest) (*llmv1.OpenTaskBudgetResponse, error) {
	if request.GetTaskId() == "" || request.GetMaxTokens() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "task_id and positive max_tokens are required")
	}
	budget, err := s.store.OpenBudget(ctx, request.GetTaskId(), request.GetMaxTokens())
	if err != nil {
		return nil, status.Error(codes.Internal, "budget could not be opened")
	}
	return &llmv1.OpenTaskBudgetResponse{TaskId: budget.TaskID, MaxTokens: budget.MaxTokens, UsedTokens: budget.UsedTokens}, nil
}

func (s *Server) Plan(ctx context.Context, request *llmv1.PlanRequest) (*llmv1.PlanResponse, error) {
	if request.GetTaskId() == "" || request.GetDescription() == "" || len(request.GetAvailableCapabilities()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "task, description and capabilities are required")
	}
	callID, err := s.reserve(ctx, request.GetTaskId(), "plan", 4_000)
	if err != nil {
		return nil, err
	}
	response, callErr := s.provider.Plan(ctx, request)
	s.finish(ctx, callID, responseTokens(response), callErr)
	if callErr != nil {
		s.logger.ErrorContext(ctx, "plan provider call failed", "task_id", request.GetTaskId(), "error", callErr)
		return nil, status.Error(codes.Unavailable, "provider unavailable")
	}
	return response, nil
}

func (s *Server) AgentStep(ctx context.Context, request *llmv1.AgentStepRequest) (*llmv1.AgentStepResponse, error) {
	if request.GetTaskId() == "" || request.GetSubtaskId() == "" || request.GetCapability() == "" {
		return nil, status.Error(codes.InvalidArgument, "task, subtask and capability are required")
	}
	callID, err := s.reserve(ctx, request.GetTaskId(), "agent_step", 2_000)
	if err != nil {
		return nil, err
	}
	response, callErr := s.provider.AgentStep(ctx, request)
	input, output := int64(0), int64(0)
	if response != nil {
		input, output = response.GetInputTokens(), response.GetOutputTokens()
	}
	s.finish(ctx, callID, [2]int64{input, output}, callErr)
	if callErr != nil {
		return nil, status.Error(codes.Unavailable, "provider unavailable")
	}
	return response, nil
}

func (s *Server) Aggregate(ctx context.Context, request *llmv1.AggregateRequest) (*llmv1.AggregateResponse, error) {
	if request.GetTaskId() == "" {
		return nil, status.Error(codes.InvalidArgument, "task_id is required")
	}
	callID, err := s.reserve(ctx, request.GetTaskId(), "aggregate", 6_000)
	if err != nil {
		return nil, err
	}
	response, callErr := s.provider.Aggregate(ctx, request)
	input, output := int64(0), int64(0)
	if response != nil {
		input, output = response.GetInputTokens(), response.GetOutputTokens()
	}
	s.finish(ctx, callID, [2]int64{input, output}, callErr)
	if callErr != nil {
		return nil, status.Error(codes.Unavailable, "provider unavailable")
	}
	return response, nil
}

func (s *Server) Health(ctx context.Context, _ *llmv1.HealthRequest) (*llmv1.HealthResponse, error) {
	ready := s.store.Ping(ctx) == nil
	return &llmv1.HealthResponse{Live: true, Ready: ready, Provider: s.provider.Name()}, nil
}

func (s *Server) reserve(ctx context.Context, taskID, purpose string, tokens int64) (string, error) {
	callID, err := s.store.Reserve(ctx, taskID, purpose, s.provider.Name(), s.provider.Model(), tokens)
	if errors.Is(err, store.ErrBudgetExhausted) {
		return "", status.Error(codes.ResourceExhausted, "LLM budget exhausted")
	}
	if err != nil {
		return "", status.Error(codes.Internal, "LLM budget could not be reserved")
	}
	return callID, nil
}

func (s *Server) finish(ctx context.Context, callID string, tokens [2]int64, callErr error) {
	state := "SUCCEEDED"
	code := ""
	if callErr != nil {
		state = "FAILED"
		code = "PROVIDER_ERROR"
	}
	if err := s.store.Finish(ctx, callID, tokens[0], tokens[1], state, code); err != nil {
		s.logger.ErrorContext(ctx, "finish LLM call failed", "call_id", callID, "error", err)
	}
}

func responseTokens(response *llmv1.PlanResponse) [2]int64 {
	if response == nil {
		return [2]int64{}
	}
	return [2]int64{response.GetInputTokens(), response.GetOutputTokens()}
}
