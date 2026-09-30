package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	orchestratorv1 "github.com/caduc/ai-agent-orchestrator/contracts/gen/go/orchestrator/v1"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/domain"
	"github.com/caduc/ai-agent-orchestrator/services/orchestrator/internal/store"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	orchestratorv1.UnimplementedOrchestratorServiceServer
	store       *store.Store
	logger      *slog.Logger
	version     string
	taskTimeout time.Duration
}

func NewServer(storage *store.Store, logger *slog.Logger, version string, taskTimeout time.Duration) *Server {
	return &Server{store: storage, logger: logger, version: version, taskTimeout: taskTimeout}
}

func (s *Server) CreateTask(ctx context.Context, request *orchestratorv1.CreateTaskRequest) (*orchestratorv1.CreateTaskResponse, error) {
	principal, err := requirePrincipal(request.GetPrincipal(), "tasks:create")
	if err != nil {
		return nil, err
	}
	description := strings.TrimSpace(request.GetDescription())
	if description == "" || utf8.RuneCountInString(request.GetDescription()) > 20_000 {
		return nil, status.Error(codes.InvalidArgument, "description must contain between 1 and 20000 characters")
	}
	if len(request.GetIdempotencyKey()) > 255 {
		return nil, status.Error(codes.InvalidArgument, "idempotency key is too long")
	}
	now := time.Now().UTC()
	task := domain.Task{
		ID:           newID(),
		OwnerSubject: principal.GetSubject(),
		Description:  request.GetDescription(),
		Status:       domain.TaskQueued,
		CreatedAt:    now,
		UpdatedAt:    now,
		DeadlineAt:   now.Add(s.taskTimeout),
	}
	keyHash := digest(request.GetIdempotencyKey())
	if request.GetIdempotencyKey() == "" {
		keyHash = ""
	}
	fingerprint := digest(request.GetDescription())
	created, replayed, err := s.store.CreateTask(ctx, task, keyHash, fingerprint)
	if errors.Is(err, store.ErrConflict) {
		return nil, status.Error(codes.AlreadyExists, "idempotency key was already used with a different request")
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "create task failed", "error", err, "request_id", request.GetRequestId())
		return nil, status.Error(codes.Unavailable, "task could not be accepted")
	}
	s.logger.InfoContext(ctx, "task accepted", "task_id", created.ID, "owner", created.OwnerSubject, "replayed", replayed)
	return &orchestratorv1.CreateTaskResponse{Task: taskToProto(created), Replayed: replayed}, nil
}

func (s *Server) GetTask(ctx context.Context, request *orchestratorv1.GetTaskRequest) (*orchestratorv1.GetTaskResponse, error) {
	principal, err := requirePrincipal(request.GetPrincipal(), "tasks:read")
	if err != nil {
		return nil, err
	}
	task, err := s.store.GetTask(ctx, request.GetTaskId())
	if err != nil || !canAccess(principal, task.OwnerSubject) {
		return nil, status.Error(codes.NotFound, "task not found")
	}
	return &orchestratorv1.GetTaskResponse{Task: taskToProto(task)}, nil
}

func (s *Server) CancelTask(ctx context.Context, request *orchestratorv1.CancelTaskRequest) (*orchestratorv1.CancelTaskResponse, error) {
	principal, err := requirePrincipal(request.GetPrincipal(), "tasks:cancel")
	if err != nil {
		return nil, err
	}
	task, err := s.store.GetTask(ctx, request.GetTaskId())
	if err != nil || !canAccess(principal, task.OwnerSubject) {
		return nil, status.Error(codes.NotFound, "task not found")
	}
	task, err = s.store.CancelTask(ctx, request.GetTaskId())
	if errors.Is(err, store.ErrConflict) {
		return nil, status.Error(codes.FailedPrecondition, "terminal task cannot be cancelled")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "task could not be cancelled")
	}
	s.logger.InfoContext(ctx, "task cancelled", "task_id", task.ID, "request_id", request.GetRequestId())
	return &orchestratorv1.CancelTaskResponse{Task: taskToProto(task)}, nil
}

func (s *Server) ListAgents(ctx context.Context, request *orchestratorv1.ListAgentsRequest) (*orchestratorv1.ListAgentsResponse, error) {
	if _, err := requirePrincipal(request.GetPrincipal(), "agents:read"); err != nil {
		return nil, err
	}
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "agents could not be listed")
	}
	response := &orchestratorv1.ListAgentsResponse{}
	for _, agent := range agents {
		response.Agents = append(response.Agents, &orchestratorv1.Agent{Id: agent.ID, Type: agent.Type, Version: agent.Version, Capabilities: agent.Capabilities, Status: agent.Status})
	}
	return response, nil
}

func (s *Server) WatchTaskEvents(request *orchestratorv1.WatchTaskEventsRequest, stream grpc.ServerStreamingServer[orchestratorv1.WatchTaskEventsResponse]) error {
	principal, err := requirePrincipal(request.GetPrincipal(), "tasks:read")
	if err != nil {
		return err
	}
	task, err := s.store.GetTask(stream.Context(), request.GetTaskId())
	if err != nil || !canAccess(principal, task.OwnerSubject) {
		return status.Error(codes.NotFound, "task not found")
	}
	cursor := request.GetAfterEventId()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		events, err := s.store.EventsAfter(stream.Context(), task.ID, cursor, 100)
		if err != nil {
			return status.Error(codes.Unavailable, "events unavailable")
		}
		for _, event := range events {
			response := &orchestratorv1.WatchTaskEventsResponse{
				TaskId: event.TaskID, EventId: event.EventID, Type: event.Type,
				OccurredAt: timestamppb.New(event.OccurredAt), PayloadJson: event.PayloadJSON,
			}
			if err := stream.Send(response); err != nil {
				return err
			}
			cursor = event.EventID
			if terminalEvent(event.Type) {
				return nil
			}
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
		}
	}
}

func (s *Server) Health(ctx context.Context, _ *orchestratorv1.HealthRequest) (*orchestratorv1.HealthResponse, error) {
	response := &orchestratorv1.HealthResponse{Live: true, Ready: true, Version: s.version}
	statusValue := "UP"
	if err := s.store.Ping(ctx); err != nil {
		response.Ready = false
		statusValue = "DOWN"
	}
	response.Components = append(response.Components, &orchestratorv1.ComponentHealth{Component: "postgres", Status: statusValue})
	return response, nil
}

func requirePrincipal(principal *orchestratorv1.Principal, requiredScope string) (*orchestratorv1.Principal, error) {
	if principal == nil || strings.TrimSpace(principal.GetSubject()) == "" {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	for _, scope := range principal.GetScopes() {
		if scope == requiredScope {
			return principal, nil
		}
	}
	return nil, status.Error(codes.PermissionDenied, "insufficient scope")
}

func canAccess(principal *orchestratorv1.Principal, owner string) bool {
	if principal.GetSubject() == owner {
		return true
	}
	for _, role := range principal.GetRoles() {
		if role == "admin" {
			return true
		}
	}
	return false
}

func newID() string {
	value, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return value.String()
}

func digest(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func terminalEvent(eventType string) bool {
	return eventType == "task.completed" || eventType == "task.failed" || eventType == "task.cancelled"
}

func taskToProto(task domain.Task) *orchestratorv1.Task {
	result := &orchestratorv1.Task{
		Id: task.ID, OwnerSubject: task.OwnerSubject, Description: task.Description,
		Status: taskStatusToProto(task.Status), CreatedAt: timestamppb.New(task.CreatedAt),
		UpdatedAt: timestamppb.New(task.UpdatedAt), DeadlineAt: timestamppb.New(task.DeadlineAt), ErrorCode: task.ErrorCode,
	}
	for _, subtask := range task.Subtasks {
		converted := &orchestratorv1.Subtask{
			Id: subtask.ID, Description: subtask.Description, Capability: subtask.Capability,
			Status: subtaskStatusToProto(subtask.Status), DependsOn: subtask.DependsOn,
			Attempt: subtask.Attempt, ErrorCode: subtask.ErrorCode,
		}
		if subtask.Result != nil {
			converted.Result = &orchestratorv1.AgentResult{Summary: subtask.Result.Summary, Warnings: subtask.Result.Warnings, Truncated: subtask.Result.Truncated}
			for _, evidence := range subtask.Result.Evidence {
				converted.Result.Evidence = append(converted.Result.Evidence, evidenceToProto(evidence))
			}
		}
		result.Subtasks = append(result.Subtasks, converted)
	}
	if task.FinalResult != nil {
		result.FinalResult = &orchestratorv1.FinalResult{Summary: task.FinalResult.Summary, Conclusions: task.FinalResult.Conclusions, Failures: task.FinalResult.Failures, Limitations: task.FinalResult.Limitations}
		for _, evidence := range task.FinalResult.Evidence {
			result.FinalResult.Evidence = append(result.FinalResult.Evidence, evidenceToProto(evidence))
		}
	}
	return result
}

func evidenceToProto(evidence domain.Evidence) *orchestratorv1.Evidence {
	return &orchestratorv1.Evidence{Source: evidence.Source, Reference: evidence.Reference, Content: evidence.Content}
}

func taskStatusToProto(value domain.TaskStatus) orchestratorv1.TaskStatus {
	mapping := map[domain.TaskStatus]orchestratorv1.TaskStatus{
		domain.TaskQueued:             orchestratorv1.TaskStatus_TASK_STATUS_QUEUED,
		domain.TaskPlanning:           orchestratorv1.TaskStatus_TASK_STATUS_PLANNING,
		domain.TaskRunning:            orchestratorv1.TaskStatus_TASK_STATUS_RUNNING,
		domain.TaskAggregating:        orchestratorv1.TaskStatus_TASK_STATUS_AGGREGATING,
		domain.TaskCompleted:          orchestratorv1.TaskStatus_TASK_STATUS_COMPLETED,
		domain.TaskPartiallyCompleted: orchestratorv1.TaskStatus_TASK_STATUS_PARTIALLY_COMPLETED,
		domain.TaskFailed:             orchestratorv1.TaskStatus_TASK_STATUS_FAILED,
		domain.TaskCancelled:          orchestratorv1.TaskStatus_TASK_STATUS_CANCELLED,
	}
	return mapping[value]
}

func subtaskStatusToProto(value domain.SubtaskStatus) orchestratorv1.SubtaskStatus {
	mapping := map[domain.SubtaskStatus]orchestratorv1.SubtaskStatus{
		domain.SubtaskPending:   orchestratorv1.SubtaskStatus_SUBTASK_STATUS_PENDING,
		domain.SubtaskBlocked:   orchestratorv1.SubtaskStatus_SUBTASK_STATUS_BLOCKED,
		domain.SubtaskReady:     orchestratorv1.SubtaskStatus_SUBTASK_STATUS_READY,
		domain.SubtaskRunning:   orchestratorv1.SubtaskStatus_SUBTASK_STATUS_RUNNING,
		domain.SubtaskSucceeded: orchestratorv1.SubtaskStatus_SUBTASK_STATUS_SUCCEEDED,
		domain.SubtaskFailed:    orchestratorv1.SubtaskStatus_SUBTASK_STATUS_FAILED,
		domain.SubtaskSkipped:   orchestratorv1.SubtaskStatus_SUBTASK_STATUS_SKIPPED,
		domain.SubtaskCancelled: orchestratorv1.SubtaskStatus_SUBTASK_STATUS_CANCELLED,
	}
	return mapping[value]
}

func eventJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(raw)
}
