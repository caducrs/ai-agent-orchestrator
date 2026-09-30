package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExecuteFindsEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "handler.go"), []byte("package demo\nfunc handler() error { return errValue }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODE_ROOT", root)
	summary, evidence, _, err := Execute(context.Background(), "HTTP 500")
	if err != nil {
		t.Fatal(err)
	}
	if summary == "" || len(evidence) == 0 {
		t.Fatalf("missing analysis output: %q %#v", summary, evidence)
	}
}
