package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	orchestratorv1 "github.com/caduc/ai-agent-orchestrator/contracts/gen/go/orchestrator/v1"
	"github.com/caduc/ai-agent-orchestrator/services/api-gateway/internal/auth"
	"github.com/caduc/ai-agent-orchestrator/services/api-gateway/internal/ratelimit"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type requestIDKey struct{}

type Server struct {
	client         orchestratorv1.OrchestratorServiceClient
	authenticator  *auth.Authenticator
	limiter        *ratelimit.Limiter
	logger         *slog.Logger
	requestTimeout time.Duration
	sseHeartbeat   time.Duration
	sseBuffer      int
}

type createTaskBody struct {
	Description string `json:"description"`
}

type errorEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func New(client orchestratorv1.OrchestratorServiceClient, authenticator *auth.Authenticator, limiter *ratelimit.Limiter, logger *slog.Logger, requestTimeout, heartbeat time.Duration, sseBuffer int) *Server {
	return &Server{client: client, authenticator: authenticator, limiter: limiter, logger: logger, requestTimeout: requestTimeout, sseHeartbeat: heartbeat, sseBuffer: sseBuffer}
}

func (s *Server) Handler() http.Handler {
	public := http.NewServeMux()
	public.HandleFunc("GET /api/v1/health", s.health)

	protected := http.NewServeMux()
	protected.HandleFunc("POST /api/v1/tasks", s.createTask)
	protected.HandleFunc("GET /api/v1/tasks/{id}", s.getTask)
	protected.HandleFunc("POST /api/v1/tasks/{id}/cancel", s.cancelTask)
	protected.HandleFunc("GET /api/v1/tasks/{id}/events", s.events)
	protected.HandleFunc("GET /api/v1/agents", s.listAgents)
	protectedHandler := s.authenticator.Middleware(s.rateLimit(protected))

	root := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/health" {
			public.ServeHTTP(writer, request)
			return
		}
		protectedHandler.ServeHTTP(writer, request)
	})
	return s.requestID(root)
}

func (s *Server) createTask(writer http.ResponseWriter, request *http.Request) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		s.writeError(writer, request, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 256<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var body createTaskBody
	if err := decoder.Decode(&body); err != nil {
		statusCode := http.StatusBadRequest
		if strings.Contains(err.Error(), "request body too large") {
			statusCode = http.StatusRequestEntityTooLarge
		}
		s.writeError(writer, request, statusCode, "INVALID_REQUEST", "invalid JSON request")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		s.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "request must contain one JSON document")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), s.requestTimeout)
	defer cancel()
	response, err := s.client.CreateTask(ctx, &orchestratorv1.CreateTaskRequest{
		Description: body.Description, IdempotencyKey: request.Header.Get("Idempotency-Key"),
		Principal: auth.Principal(request.Context()), RequestId: requestID(request.Context()),
	})
	if err != nil {
		s.writeGRPCError(writer, request, err)
		return
	}
	s.writeProto(writer, http.StatusAccepted, response)
}

func (s *Server) getTask(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), s.requestTimeout)
	defer cancel()
	response, err := s.client.GetTask(ctx, &orchestratorv1.GetTaskRequest{TaskId: request.PathValue("id"), Principal: auth.Principal(request.Context())})
	if err != nil {
		s.writeGRPCError(writer, request, err)
		return
	}
	s.writeProto(writer, http.StatusOK, response)
}

func (s *Server) cancelTask(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), s.requestTimeout)
	defer cancel()
	response, err := s.client.CancelTask(ctx, &orchestratorv1.CancelTaskRequest{TaskId: request.PathValue("id"), Principal: auth.Principal(request.Context()), RequestId: requestID(request.Context())})
	if err != nil {
		s.writeGRPCError(writer, request, err)
		return
	}
	s.writeProto(writer, http.StatusOK, response)
}

func (s *Server) listAgents(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), s.requestTimeout)
	defer cancel()
	response, err := s.client.ListAgents(ctx, &orchestratorv1.ListAgentsRequest{Principal: auth.Principal(request.Context())})
	if err != nil {
		s.writeGRPCError(writer, request, err)
		return
	}
	s.writeProto(writer, http.StatusOK, response)
}

func (s *Server) health(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), s.requestTimeout)
	defer cancel()
	response, err := s.client.Health(ctx, &orchestratorv1.HealthRequest{})
	if err != nil {
		s.writeError(writer, request, http.StatusServiceUnavailable, "NOT_READY", "orchestrator unavailable")
		return
	}
	statusCode := http.StatusOK
	if !response.GetReady() {
		statusCode = http.StatusServiceUnavailable
	}
	s.writeProto(writer, statusCode, response)
}

func (s *Server) events(writer http.ResponseWriter, request *http.Request) {
	cursor := int64(0)
	if value := request.Header.Get("Last-Event-ID"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			s.writeError(writer, request, http.StatusBadRequest, "INVALID_EVENT_CURSOR", "Last-Event-ID is invalid")
			return
		}
		cursor = parsed
	}
	stream, err := s.client.WatchTaskEvents(request.Context(), &orchestratorv1.WatchTaskEventsRequest{TaskId: request.PathValue("id"), AfterEventId: cursor, Principal: auth.Principal(request.Context())})
	if err != nil {
		s.writeGRPCError(writer, request, err)
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		s.writeError(writer, request, http.StatusInternalServerError, "STREAM_UNSUPPORTED", "streaming is unavailable")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	type streamItem struct {
		event *orchestratorv1.WatchTaskEventsResponse
		err   error
	}
	items := make(chan streamItem, s.sseBuffer)
	go func() {
		defer close(items)
		for {
			event, receiveErr := stream.Recv()
			item := streamItem{event: event, err: receiveErr}
			select {
			case items <- item:
			case <-request.Context().Done():
				return
			}
			if receiveErr != nil {
				return
			}
		}
	}()
	heartbeat := time.NewTicker(s.sseHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case item, open := <-items:
			if !open {
				return
			}
			if item.err != nil {
				if !errors.Is(item.err, io.EOF) && status.Code(item.err) != codes.Canceled {
					s.logger.WarnContext(request.Context(), "SSE upstream closed", "error", item.err, "task_id", request.PathValue("id"))
				}
				return
			}
			event := item.event
			eventType := strings.NewReplacer("\r", "", "\n", "").Replace(event.GetType())
			_, err := fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.GetEventId(), eventType, event.GetPayloadJson())
			if err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := io.WriteString(writer, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal := auth.Principal(request.Context())
		if principal == nil {
			s.writeError(writer, request, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
			return
		}
		host, _, _ := net.SplitHostPort(request.RemoteAddr)
		key := principal.GetSubject() + ":" + host + ":" + operation(request)
		allowed, retryAfter, err := s.limiter.Allow(request.Context(), key)
		if err != nil {
			s.writeError(writer, request, http.StatusServiceUnavailable, "RATE_LIMITER_UNAVAILABLE", "rate limiter unavailable")
			return
		}
		if !allowed {
			seconds := int64(retryAfter/time.Second) + 1
			writer.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
			s.writeError(writer, request, http.StatusTooManyRequests, "RATE_LIMITED", "request rate exceeded")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		id := request.Header.Get("X-Request-ID")
		if id == "" || len(id) > 128 {
			id = newRequestID()
		}
		writer.Header().Set("X-Request-ID", id)
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), requestIDKey{}, id)))
	})
}

func (s *Server) writeGRPCError(writer http.ResponseWriter, request *http.Request, err error) {
	grpcStatus, _ := status.FromError(err)
	statusCode := http.StatusInternalServerError
	code := "INTERNAL_ERROR"
	switch grpcStatus.Code() {
	case codes.InvalidArgument:
		statusCode, code = http.StatusBadRequest, "INVALID_REQUEST"
	case codes.Unauthenticated:
		statusCode, code = http.StatusUnauthorized, "UNAUTHENTICATED"
	case codes.PermissionDenied:
		statusCode, code = http.StatusForbidden, "FORBIDDEN"
	case codes.NotFound:
		statusCode, code = http.StatusNotFound, "NOT_FOUND"
	case codes.AlreadyExists, codes.FailedPrecondition, codes.Aborted:
		statusCode, code = http.StatusConflict, "CONFLICT"
	case codes.ResourceExhausted:
		statusCode, code = http.StatusTooManyRequests, "RESOURCE_EXHAUSTED"
	case codes.Unavailable:
		statusCode, code = http.StatusServiceUnavailable, "UNAVAILABLE"
	case codes.DeadlineExceeded:
		statusCode, code = http.StatusGatewayTimeout, "TIMEOUT"
	}
	s.writeError(writer, request, statusCode, code, grpcStatus.Message())
}

func (s *Server) writeError(writer http.ResponseWriter, request *http.Request, statusCode int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(errorEnvelope{Code: code, Message: message, RequestID: requestID(request.Context())})
}

func (s *Server) writeProto(writer http.ResponseWriter, statusCode int, message proto.Message) {
	payload, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(message)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_, _ = writer.Write(payload)
}

func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

func newRequestID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buffer)
}

func operation(request *http.Request) string {
	path := request.URL.Path
	switch {
	case request.Method == http.MethodPost && path == "/api/v1/tasks":
		return "tasks:create"
	case strings.HasSuffix(path, "/events"):
		return "tasks:events"
	case strings.HasSuffix(path, "/cancel"):
		return "tasks:cancel"
	case path == "/api/v1/agents":
		return "agents:read"
	default:
		return "tasks:read"
	}
}
