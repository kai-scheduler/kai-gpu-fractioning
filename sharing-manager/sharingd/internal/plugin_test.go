package internal

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/containerd/nri/pkg/api"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/configuration"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/injection"
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
			expectedEnvKeys: []string{injection.EnvGPUMemoryRequests, injection.EnvGPUMemoryLimits, injection.EnvMPSPipeDirectory},
			expectedEnvVals: map[string]string{injection.EnvGPUMemoryRequests: "2147", injection.EnvGPUMemoryLimits: "4294"},
			expectedMount:   true,
		},
		{
			name: "only limit",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.main.limit": "4Gi",
			},
			containerName:   "main",
			expectedNil:     false,
			expectedEnvKeys: []string{injection.EnvGPUMemoryLimits, injection.EnvMPSPipeDirectory},
			expectedMount:   true,
		},
		{
			name: "only request",
			annotations: map[string]string{
				"nvidia.com/gpu-memory.container.worker.request": "1Gi",
			},
			containerName:   "worker",
			expectedNil:     false,
			expectedEnvKeys: []string{injection.EnvGPUMemoryRequests, injection.EnvMPSPipeDirectory},
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
	p, err := NewPlugin(Config{
		AnnotationPrefix: configuration.DefaultAnnotationPrefix,
		MPSPipeDirectory: configuration.DefaultMPSPipeDirectory,
		FailOpen:         true,
		MapDir:           t.TempDir(),
		Log:              log,
	}, nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}

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
	p, err := NewPlugin(Config{
		AnnotationPrefix: configuration.DefaultAnnotationPrefix,
		MPSPipeDirectory: configuration.DefaultMPSPipeDirectory,
		FailOpen:         false,
		MapDir:           t.TempDir(),
		Log:              log,
	}, nil)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	return p
}

// fakeStopper records the container IDs Synchronize asks to stop.
type fakeStopper struct {
	mu      sync.Mutex
	stopped []string
}

func (f *fakeStopper) Stop(_ context.Context, containerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, containerID)
	return nil
}

func (f *fakeStopper) stoppedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stopped...)
}

// enforcingPlugin builds a plugin with retroactive enforcement enabled, backed
// by the given fake stopper.
func enforcingPlugin(t *testing.T, stopper *fakeStopper) *Plugin {
	t.Helper()
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p, err := NewPlugin(Config{
		AnnotationPrefix:       configuration.DefaultAnnotationPrefix,
		MPSPipeDirectory:       configuration.DefaultMPSPipeDirectory,
		MapDir:                 t.TempDir(),
		RetroactiveEnforcement: true,
		Log:                    log,
	}, stopper)
	if err != nil {
		t.Fatalf("NewPlugin: %v", err)
	}
	return p
}

// syncSnapshot returns a pod plus a fully-injected container and an uninjected
// container, both sharing containers ("trainer") with a 4Gi limit annotation.
func syncSnapshot() ([]*api.PodSandbox, []*api.Container) {
	pods := []*api.PodSandbox{
		{Id: "p1", Name: "pod1", Namespace: "default", Annotations: map[string]string{
			configuration.DefaultAnnotationPrefix + "trainer.limit": "4Gi",
		}},
		{Id: "p2", Name: "pod2", Namespace: "default", Annotations: map[string]string{
			configuration.DefaultAnnotationPrefix + "trainer.limit": "4Gi",
		}},
	}
	containers := []*api.Container{
		{ // injected → not a violation
			Id: "c1", Name: "trainer", PodSandboxId: "p1",
			State: api.ContainerState_CONTAINER_RUNNING,
			Env:   []string{injection.EnvMPSPipeDirectory + "=" + configuration.DefaultMPSPipeDirectory, injection.EnvGPUMemoryLimits + "=4294"},
			Mounts: []*api.Mount{{
				Source:      configuration.DefaultMPSPipeDirectory,
				Destination: configuration.DefaultMPSPipeDirectory,
				Type:        "bind",
			}},
		},
		{ // uninjected → violation
			Id: "c2", Name: "trainer", PodSandboxId: "p2",
			State: api.ContainerState_CONTAINER_RUNNING,
		},
	}
	return pods, containers
}

func TestSynchronizeEnforcesRetroactively(t *testing.T) {
	fake := &fakeStopper{}
	p := enforcingPlugin(t, fake)
	pods, containers := syncSnapshot()

	if _, err := p.Synchronize(context.Background(), pods, containers); err != nil {
		t.Fatalf("Synchronize returned error: %v", err)
	}
	p.Flush() // wait for async remediation

	if got := fake.stoppedIDs(); !slices.Equal(got, []string{"c2"}) {
		t.Fatalf("stopped = %v, want [c2]", got)
	}
}

func TestSynchronizeNoEnforcementWhenDisabled(t *testing.T) {
	fake := &fakeStopper{}
	// Default plugin: RetroactiveEnforcement is false.
	p := newTestPlugin(t)
	pods, containers := syncSnapshot()

	if _, err := p.Synchronize(context.Background(), pods, containers); err != nil {
		t.Fatalf("Synchronize returned error: %v", err)
	}
	p.Flush()

	if got := len(fake.stoppedIDs()); got != 0 {
		t.Fatalf("expected no stops when disabled, got %d", got)
	}
}

func TestNewPluginErrorsWhenEnabledWithoutStopper(t *testing.T) {
	// RetroactiveEnforcement set but nil stopper is a misconfiguration and must
	// surface as an error rather than silently disabling enforcement.
	log := slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	p, err := NewPlugin(Config{
		AnnotationPrefix:       configuration.DefaultAnnotationPrefix,
		MPSPipeDirectory:       configuration.DefaultMPSPipeDirectory,
		MapDir:                 t.TempDir(),
		RetroactiveEnforcement: true,
		Log:                    log,
	}, nil)
	if err == nil {
		t.Fatal("expected error when enforcement enabled without a stopper, got nil")
	}
	if p != nil {
		t.Fatalf("expected nil plugin on error, got %+v", p)
	}
}
