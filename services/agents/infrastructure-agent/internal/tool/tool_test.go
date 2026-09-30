package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExecuteChecksHealthAndMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/health" {
			_, _ = writer.Write([]byte(`{"status":"ok"}`))
			return
		}
		if request.URL.Path == "/api/v1/query" {
			_, _ = writer.Write([]byte(`{"status":"success","data":{"result":[]}}`))
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	t.Setenv("INFRA_HEALTH_URL", server.URL+"/health")
	t.Setenv("PROMETHEUS_URL", server.URL)
	summary, evidence, _, err := Execute(context.Background(), "inspect")
	if err != nil {
		t.Fatal(err)
	}
	if summary == "" || len(evidence) != 2 {
		t.Fatalf("unexpected output %q %#v", summary, evidence)
	}
}
