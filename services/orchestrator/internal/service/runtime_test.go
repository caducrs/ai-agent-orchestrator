package service

import (
	"context"
	"log/slog"
	"testing"
)

func TestSafelyIsolatesPanickingUnit(t *testing.T) {
	t.Parallel()
	r := &Runtime{logger: slog.New(slog.DiscardHandler)}
	if failed := r.safely(context.Background(), "panics", func(context.Context) { panic("boom") }); !failed {
		t.Fatal("a panicking unit must be reported as failed")
	}
	ran := false
	if failed := r.safely(context.Background(), "succeeds", func(context.Context) { ran = true }); failed || !ran {
		t.Fatalf("a healthy unit must run and succeed: failed=%v ran=%v", failed, ran)
	}
}
