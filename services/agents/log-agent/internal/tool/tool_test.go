package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExecuteQueriesLoki(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/loki/api/v1/query_range" || request.URL.Query().Get("query") == "" {
			t.Fatalf("unexpected request %s", request.URL.String())
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"success","data":{"result":[{"stream":{"job":"demo"},"values":[["1","error 500"]]}]}}`))
	}))
	defer server.Close()
	t.Setenv("LOKI_URL", server.URL)
	summary, evidence, _, err := Execute(context.Background(), "HTTP 500")
	if err != nil {
		t.Fatal(err)
	}
	if summary == "" || len(evidence) != 1 {
		t.Fatalf("unexpected output %q %#v", summary, evidence)
	}
}
