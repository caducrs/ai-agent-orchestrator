package provider

import (
	"reflect"
	"testing"

	llmv1 "github.com/caduc/ai-agent-orchestrator/contracts/gen/go/llm/v1"
)

func TestLocalPlan(t *testing.T) {
	t.Parallel()
	request := &llmv1.PlanRequest{TaskId: "task", Description: "HTTP 500", AvailableCapabilities: []string{"code", "logs", "database", "infrastructure"}, MaxSubtasks: 4}
	first := localPlan(request)
	second := localPlan(request)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("local plan is not deterministic")
	}
	if len(first.GetSubtasks()) != 4 {
		t.Fatalf("expected four subtasks, got %d", len(first.GetSubtasks()))
	}
	ids := map[string]bool{}
	for _, subtask := range first.GetSubtasks() {
		if ids[subtask.GetId()] {
			t.Fatalf("duplicate id %s", subtask.GetId())
		}
		ids[subtask.GetId()] = true
	}
}

func TestLocalAggregate(t *testing.T) {
	t.Parallel()
	request := &llmv1.AggregateRequest{TaskId: "task", TaskDescription: "HTTP 500", Results: []*llmv1.NormalizedResult{
		{SubtaskId: "a", Capability: "code", Success: true, Summary: "nil pointer", Evidence: []*llmv1.Evidence{{Source: "repo", Reference: "main.go:10", Content: "panic"}}},
		{SubtaskId: "b", Capability: "logs", Success: false, ErrorCode: "TIMEOUT"},
	}}
	response := localAggregate(request)
	if len(response.GetConclusions()) != 1 || len(response.GetEvidence()) != 1 || len(response.GetFailures()) != 1 {
		t.Fatalf("unexpected aggregation: %#v", response)
	}
}
