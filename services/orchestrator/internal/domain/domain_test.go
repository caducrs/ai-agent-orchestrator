package domain

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidateTaskTransition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		from    TaskStatus
		to      TaskStatus
		wantErr bool
	}{
		{name: "queue to planning", from: TaskQueued, to: TaskPlanning},
		{name: "running to aggregating", from: TaskRunning, to: TaskAggregating},
		{name: "terminal idempotency", from: TaskCompleted, to: TaskCompleted},
		{name: "terminal cannot restart", from: TaskCompleted, to: TaskRunning, wantErr: true},
		{name: "queue cannot complete", from: TaskQueued, to: TaskCompleted, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateTaskTransition(test.from, test.to)
			if test.wantErr && !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("expected invalid transition, got %v", err)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidatePlan(t *testing.T) {
	t.Parallel()
	capabilities := map[string]bool{"code": true, "logs": true}
	valid := []PlannedSubtask{
		{ID: "a", Description: "read code", Capability: "code"},
		{ID: "b", Description: "inspect logs", Capability: "logs", DependsOn: []string{"a"}},
	}
	if err := ValidatePlan(valid, 10, capabilities); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}

	cases := map[string][]PlannedSubtask{
		"cycle": {
			{ID: "a", Description: "a", Capability: "code", DependsOn: []string{"b"}},
			{ID: "b", Description: "b", Capability: "logs", DependsOn: []string{"a"}},
		},
		"unknown dependency": {{ID: "a", Description: "a", Capability: "code", DependsOn: []string{"missing"}}},
		"duplicate id": {
			{ID: "a", Description: "a", Capability: "code"},
			{ID: "a", Description: "again", Capability: "logs"},
		},
		"unknown capability": {{ID: "a", Description: "a", Capability: "shell"}},
	}
	for name, plan := range cases {
		plan := plan
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := ValidatePlan(plan, 10, capabilities); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDeterministicFallback(t *testing.T) {
	t.Parallel()
	input := []Subtask{
		{ID: "b", Capability: "logs", Status: SubtaskFailed, ErrorCode: "TIMEOUT"},
		{ID: "a", Capability: "code", Status: SubtaskSucceeded, Result: &AgentResult{Summary: "panic in handler", Evidence: []Evidence{{Source: "repo", Reference: "main.go:10", Content: "panic"}}}},
	}
	first := DeterministicFallback("HTTP 500", input)
	second := DeterministicFallback("HTTP 500", input)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("fallback is not deterministic: %#v != %#v", first, second)
	}
	if len(first.Conclusions) != 1 || first.Conclusions[0] != "panic in handler" {
		t.Fatalf("unexpected conclusions: %#v", first.Conclusions)
	}
	if len(first.Failures) != 1 || len(first.Evidence) != 1 {
		t.Fatalf("unexpected fallback result: %#v", first)
	}
}

func FuzzValidatePlanNeverAcceptsUnknownDependency(f *testing.F) {
	f.Add("missing")
	f.Fuzz(func(t *testing.T, dependency string) {
		if dependency == "" || dependency == "a" {
			return
		}
		plan := []PlannedSubtask{{ID: "a", Description: "a", Capability: "code", DependsOn: []string{dependency}}}
		if err := ValidatePlan(plan, 4, map[string]bool{"code": true}); err == nil {
			t.Fatalf("accepted unknown dependency %q", dependency)
		}
	})
}
