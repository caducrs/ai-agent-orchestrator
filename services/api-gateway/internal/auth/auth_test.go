package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticator(t *testing.T) {
	t.Parallel()
	authenticator := New("secret", "user-1")
	handler := authenticator.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal := Principal(request.Context())
		if principal == nil || principal.GetSubject() != "user-1" {
			t.Fatal("principal was not stored in context")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))

	unauthorized := httptest.NewRequest(http.MethodGet, "/", nil)
	unauthorizedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedRecorder, unauthorized)
	if unauthorizedRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorizedRecorder.Code)
	}

	authorized := httptest.NewRequest(http.MethodGet, "/", nil)
	authorized.Header.Set("Authorization", "Bearer secret")
	authorizedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(authorizedRecorder, authorized)
	if authorizedRecorder.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", authorizedRecorder.Code)
	}
}
