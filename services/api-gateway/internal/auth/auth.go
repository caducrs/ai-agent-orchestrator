package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	orchestratorv1 "github.com/caduc/ai-agent-orchestrator/contracts/gen/go/orchestrator/v1"
)

type contextKey struct{}

type Authenticator struct {
	token   string
	subject string
}

func New(token, subject string) *Authenticator {
	return &Authenticator{token: token, subject: subject}
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		header := request.Header.Get("Authorization")
		provided := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(a.token)) != 1 {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"code":"UNAUTHENTICATED","message":"authentication required"}`))
			return
		}
		principal := &orchestratorv1.Principal{
			Subject: a.subject,
			Scopes:  []string{"tasks:create", "tasks:read", "tasks:cancel", "agents:read"},
			Roles:   []string{"admin"},
		}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), contextKey{}, principal)))
	})
}

func Principal(ctx context.Context) *orchestratorv1.Principal {
	principal, _ := ctx.Value(contextKey{}).(*orchestratorv1.Principal)
	return principal
}
