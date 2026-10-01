package store

import "testing"

func TestIsTransientFailure(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"TIMEOUT", "TOOL_EXECUTION_FAILED", "SOURCE_UNAVAILABLE", "LLM_UNAVAILABLE"} {
		if !isTransientFailure(code) {
			t.Fatalf("expected %s to be transient", code)
		}
	}
	for _, code := range []string{"TOOL_POLICY_DENIED", "INVALID_ARGUMENT", ""} {
		if isTransientFailure(code) {
			t.Fatalf("expected %s to be permanent", code)
		}
	}
}
