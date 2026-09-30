package tool

import (
	"context"
	"testing"
)

func TestExecuteRequiresTarget(t *testing.T) {
	t.Setenv("TARGET_DATABASE_URL", "")
	if _, _, _, err := Execute(context.Background(), "inspect"); err == nil {
		t.Fatal("expected configuration error")
	}
}
