package internal

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/containerd/nri/pkg/api"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/configuration"
)

func TestCreateContainer(t *testing.T) {
	tests := []struct {
		name            string
		annotations     map[string]string
		containerName   string
		expectedNil     bool
		expectedErr     bool
		expectedEnvKeys []string
		expectedEnvVals map[string]string
		expectedMount   bool
	}{
		{
			name: "both request and limit",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.trainer.request": "2048Mi",
				"nvidia.com/gpu-memory.container.trainer.limit":   "4Gi",
			},
			containerName:   "trainer",
			expectedNil:     false,
			expectedEnvKeys: []string{envGPUMemoryRequests, envGPUMemoryLimits, envMPSPipeDirectory},
			expectedEnvVals: map[string]string{envGPUMemoryRequests: "2147", envGPUMemoryLimits: "4294"},
			expectedMount:   true,
		},
		{
			name: "only limit",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.main.limit": "4Gi",
			},
			containerName:   "main",
			expectedNil:     false,
			expectedEnvKeys: []string{envGPUMemoryLimits, envMPSPipeDirectory},
			expectedMount:   true,
		},
		{
			name: "only request",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.worker.request": "1Gi",
			},
			containerName:   "worker",
			expectedNil:     false,
			expectedEnvKeys: []string{envGPUMemoryRequests, envMPSPipeDirectory},
			expectedMount:   true,
		},
		{
			name:          "no GPU annotations returns nil",
			annotations:   map[string]string{"some-other/annotation": "value"},
			containerName: "main",
			expectedNil:   true,
		},
		{
			name:          "nil annotations returns nil",
			annotations:   nil,
			containerName: "main",
			expectedNil:   true,
		},
		{
			name: "different container returns nil",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.other.limit": "4Gi",
			},
			containerName: "main",
			expectedNil:   true,
		},
		{
			name: "malformed value returns error (fail-closed)",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.main.limit": "not-valid",
			},
			containerName: "main",
			expectedErr:   true,
		},
		{
			name: "below 1MB returns error (fail-closed)",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.main.limit": "500Ki",
			},
			containerName: "main",
			expectedErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestPlugin(t)
			pod := &api.PodSandbox{
				Name:        "test-pod",
				Annotations: tt.annotations,
			}
			ctr := &api.Container{Name: tt.containerName}

			adj, _, err := p.CreateContainer(context.Background(), pod, ctr)

			if tt.expectedErr {
				if err == nil {
					t.Errorf("expected error, got adj=%+v", adj)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectedNil {
				if adj != nil {
					t.Errorf("expected nil adjustment, got %+v", adj)
				}
				return
			}

			if adj == nil {
				t.Fatal("expected non-nil adjustment, got nil")
			}

			envKeys := make(map[string]bool)
			envVals := make(map[string]string)
			for _, kv := range adj.Env {
				envKeys[kv.Key] = true
				envVals[kv.Key] = kv.Value
			}
			for _, key := range tt.expectedEnvKeys {
				if !envKeys[key] {
					t.Errorf("expected env var %q not found in adjustment", key)
				}
			}
			for key, expectedVal := range tt.expectedEnvVals {
				if envVals[key] != expectedVal {
					t.Errorf("env %q = %q, expected %q", key, envVals[key], expectedVal)
				}
			}

			if tt.expectedMount {
				if len(adj.Mounts) == 0 {
					t.Error("expected mount in adjustment, got none")
				} else {
					m := adj.Mounts[0]
					if m.Source != configuration.DefaultMPSPipeDirectory {
						t.Errorf("mount source = %q, expected %q", m.Source, configuration.DefaultMPSPipeDirectory)
					}
					if m.Destination != configuration.DefaultMPSPipeDirectory {
						t.Errorf("mount destination = %q, expected %q", m.Destination, configuration.DefaultMPSPipeDirectory)
					}
				}
			}
		})
	}
}

func TestCreateContainer_FailOpenLogsWarning(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p := NewPlugin(Config{
		AnnotationPrefix: configuration.DefaultAnnotationPrefix,
		MPSPipeDirectory: configuration.DefaultMPSPipeDirectory,
		FailOpen:         true,
		MapDir:           t.TempDir(),
		Log:              log,
	})

	pod := &api.PodSandbox{
		Name: "test-pod",
		Annotations: map[string]string{
			"nvidia.com/gpu-memory.container.main.limit": "garbage-value",
		},
	}
	ctr := &api.Container{Name: "main"}

	adj, _, err := p.CreateContainer(context.Background(), pod, ctr)
	if err != nil {
		t.Fatalf("fail-open should not return error, got: %v", err)
	}
	if adj != nil {
		t.Errorf("expected nil adjustment on parse error, got %+v", adj)
	}
	if logBuf.Len() == 0 {
		t.Error("expected warning log on parse failure, got nothing")
	}
}

func newTestPlugin(t *testing.T) *Plugin {
	t.Helper()
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewPlugin(Config{
		AnnotationPrefix: configuration.DefaultAnnotationPrefix,
		MPSPipeDirectory: configuration.DefaultMPSPipeDirectory,
		FailOpen:         false,
		MapDir:           t.TempDir(),
		Log:              log,
	})
}
