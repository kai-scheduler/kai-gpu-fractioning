package internal

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/readiness"
)

func TestReadinessFollowsNRILifecycle(t *testing.T) {
	state := &readiness.State{}
	p := NewPlugin(Config{
		MapDir:    t.TempDir(),
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Readiness: state,
	})

	if state.Ready() {
		t.Fatal("plugin should start not-ready")
	}

	// Synchronize is only invoked after successful NRI registration, so it is
	// the ready-flip point.
	if _, err := p.Synchronize(context.Background(), nil, nil); err != nil {
		t.Fatalf("Synchronize returned error: %v", err)
	}
	if !state.Ready() {
		t.Error("expected ready after Synchronize")
	}

	// Shutdown means the runtime is disconnecting; readiness must drop.
	p.Shutdown(context.Background())
	if state.Ready() {
		t.Error("expected not-ready after Shutdown")
	}
}

func TestReadinessNilStateIsSafe(t *testing.T) {
	p := NewPlugin(Config{
		MapDir: t.TempDir(),
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	// Without a configured readiness state the callbacks must not panic.
	if _, err := p.Synchronize(context.Background(), nil, nil); err != nil {
		t.Fatalf("Synchronize returned error: %v", err)
	}
	p.Shutdown(context.Background())
}
