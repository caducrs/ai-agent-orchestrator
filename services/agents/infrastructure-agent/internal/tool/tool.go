package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	asyncv1 "github.com/caduc/ai-agent-orchestrator/contracts/async/v1"
)

func Execute(ctx context.Context, objective string) (string, []asyncv1.Evidence, []string, error) {
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	healthURL := os.Getenv("INFRA_HEALTH_URL")
	if healthURL == "" {
		healthURL = "http://demo-app:8080/health"
	}
	prometheusURL := strings.TrimRight(os.Getenv("PROMETHEUS_URL"), "/")
	if prometheusURL == "" {
		prometheusURL = "http://localhost:9090"
	}
	allowed := map[string]bool{}
	for _, raw := range []string{healthURL, prometheusURL} {
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "", nil, nil, fmt.Errorf("invalid configured infrastructure target")
		}
		allowed[parsed.Host] = true
	}
	queryURL := prometheusURL + "/api/v1/query?query=up"
	var evidence []asyncv1.Evidence
	for _, target := range []struct{ name, address string }{{"health", healthURL}, {"prometheus", queryURL}} {
		parsed, _ := url.Parse(target.address)
		if !allowed[parsed.Host] {
			return "", nil, nil, fmt.Errorf("target outside allowlist")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.address, nil)
		if err != nil {
			return "", nil, nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return "", nil, nil, fmt.Errorf("inspect %s: %w", target.name, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 32<<10))
		response.Body.Close()
		if readErr != nil {
			return "", nil, nil, readErr
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return "", nil, nil, fmt.Errorf("%s returned HTTP %d", target.name, response.StatusCode)
		}
		evidence = append(evidence, asyncv1.Evidence{Source: target.name, Reference: parsed.Host + parsed.Path, Content: string(body)})
	}
	return fmt.Sprintf("Infrastructure analysis for %q verified health and Prometheus metrics", objective), evidence, nil, nil
}
