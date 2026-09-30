package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCreateTaskRejectsUnsupportedMediaType(t *testing.T) {
	t.Parallel()
	server := &Server{logger: slog.Default(), requestTimeout: time.Second}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"description":"test"}`))
	recorder := httptest.NewRecorder()
	server.createTask(recorder, request)
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d", recorder.Code)
	}
}

func TestCreateTaskRejectsMultipleDocuments(t *testing.T) {
	t.Parallel()
	server := &Server{logger: slog.Default(), requestTimeout: time.Second}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"description":"test"}{"description":"other"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.createTask(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", recorder.Code)
	}
}

func TestOperationDoesNotIncludeTaskID(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/123", nil)
	if got := operation(request); got != "tasks:read" {
		t.Fatalf("unexpected operation %s", got)
	}
}
