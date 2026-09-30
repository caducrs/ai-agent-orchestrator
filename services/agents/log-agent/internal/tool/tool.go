package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	asyncv1 "github.com/caduc/ai-agent-orchestrator/contracts/async/v1"
)

func Execute(ctx context.Context, objective string) (string, []asyncv1.Evidence, []string, error) {
	baseURL := strings.TrimRight(os.Getenv("LOKI_URL"), "/")
	if baseURL == "" {
		baseURL = "http://localhost:3100"
	}
	query := `{job=~".+"} |~ "(?i)error|panic|500"`
	endpoint, err := url.Parse(baseURL + "/loki/api/v1/query_range")
	if err != nil {
		return "", nil, nil, err
	}
	values := endpoint.Query()
	values.Set("query", query)
	values.Set("start", strconv.FormatInt(time.Now().Add(-30*time.Minute).UnixNano(), 10))
	values.Set("end", strconv.FormatInt(time.Now().UnixNano(), 10))
	values.Set("limit", "100")
	endpoint.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", nil, nil, err
	}
	response, err := (&http.Client{Timeout: 25 * time.Second}).Do(request)
	if err != nil {
		return "", nil, nil, fmt.Errorf("query Loki: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return "", nil, nil, err
	}
	if response.StatusCode != http.StatusOK {
		return "", nil, nil, fmt.Errorf("Loki returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Result []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", nil, nil, fmt.Errorf("decode Loki response: %w", err)
	}
	evidence := []asyncv1.Evidence{{Source: "loki", Reference: query, Content: string(body)}}
	warnings := []string{}
	if len(body) == 64<<10 {
		warnings = append(warnings, "Loki response was truncated")
	}
	summary := fmt.Sprintf("Log analysis for %q queried Loki and found %d matching streams", objective, len(payload.Data.Result))
	return summary, evidence, warnings, nil
}
