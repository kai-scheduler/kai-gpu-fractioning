package mpsd

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/common/daemonmgr"
)

func TestDaemon_BuildDaemonSet_Basics(t *testing.T) {
	d := NewMpsdDaemon(nil)

	if got := d.Name(); got != "mpsd" {
		t.Errorf("Name() = %q, want %q", got, "mpsd")
	}

	ds := d.BuildDaemonSet(defaultOpts())

	if ds.Name != "gpu-sharing-mpsd" {
		t.Errorf("DaemonSet name = %q, want %q", ds.Name, "gpu-sharing-mpsd")
	}
	if ds.Namespace != "gpu-sharing-system" {
		t.Errorf("DaemonSet namespace = %q, want %q", ds.Namespace, "gpu-sharing-system")
	}

	spec := ds.Spec.Template.Spec

	// RuntimeClassName
	if spec.RuntimeClassName == nil || *spec.RuntimeClassName != "nvidia" {
		t.Errorf("RuntimeClassName = %v, want nvidia", spec.RuntimeClassName)
	}

	// No HostPID (mpsd doesn't need it)
	if spec.HostPID {
		t.Error("expected HostPID=false for mpsd")
	}

	// Node selector
	if spec.NodeSelector == nil || spec.NodeSelector["nvidia.com/gpu.present"] != "true" {
		t.Errorf("NodeSelector = %v, want nvidia.com/gpu.present=true", spec.NodeSelector)
	}

	// Container
	if len(spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(spec.Containers))
	}

	ctr := spec.Containers[0]
	if ctr.Image != "fake.io/org/mpsd:v0.1.0" {
		t.Errorf("container image = %q, want %q", ctr.Image, "fake.io/org/mpsd:v0.1.0")
	}
	if ctr.SecurityContext == nil || ctr.SecurityContext.Privileged == nil || !*ctr.SecurityContext.Privileged {
		t.Error("expected privileged=true")
	}

	// Probes
	if ctr.ReadinessProbe == nil {
		t.Fatal("expected readiness probe")
	}
	if ctr.ReadinessProbe.Exec == nil || len(ctr.ReadinessProbe.Exec.Command) == 0 {
		t.Error("expected exec readiness probe checking MPS control socket")
	}

	// Volumes
	volumePaths := make(map[string]string)
	for _, v := range spec.Volumes {
		if v.HostPath != nil {
			volumePaths[v.Name] = v.HostPath.Path
		}
	}
	if volumePaths["mps-pipe"] != "/run/nvidia-mps" {
		t.Errorf("mps-pipe volume path = %q, want %q", volumePaths["mps-pipe"], "/run/nvidia-mps")
	}
	if volumePaths["mps-log"] != "/var/log/nvidia-mps" {
		t.Errorf("mps-log volume path = %q, want %q", volumePaths["mps-log"], "/var/log/nvidia-mps")
	}

	// Volume mounts
	mountPaths := make(map[string]string)
	for _, m := range ctr.VolumeMounts {
		mountPaths[m.Name] = m.MountPath
	}
	if mountPaths["mps-pipe"] != "/run/nvidia-mps" {
		t.Errorf("mps-pipe mount = %q, want %q", mountPaths["mps-pipe"], "/run/nvidia-mps")
	}
	if mountPaths["mps-log"] != "/var/log/nvidia-mps" {
		t.Errorf("mps-log mount = %q, want %q", mountPaths["mps-log"], "/var/log/nvidia-mps")
	}

	// Labels
	labels := ds.Labels
	if labels[daemonmgr.LabelManagedBy] != daemonmgr.ManagedByValue {
		t.Errorf("missing managed-by label, got %v", labels)
	}
	if labels[daemonmgr.LabelComponent] != "mpsd" {
		t.Errorf("missing component label, got %v", labels)
	}

	// No args when spec is nil
	if len(ctr.Args) != 0 {
		t.Errorf("expected no args when spec is nil, got %v", ctr.Args)
	}
}

func TestDaemon_BuildDaemonSet_Args(t *testing.T) {
	d := NewMpsdDaemon(&v1alpha1.MpsDaemonSpec{
		LogLevel:          "debug",
		Backoff:           &metav1.Duration{Duration: 10 * time.Second},
		MaxRetries:        ptr.To(int32(5)),
		StableThreshold:   &metav1.Duration{Duration: 10 * time.Minute},
		GracefulStopDelay: &metav1.Duration{Duration: 30 * time.Second},
	})

	ds := d.BuildDaemonSet(defaultOpts())
	args := ds.Spec.Template.Spec.Containers[0].Args

	expected := []string{
		"--log-level", "debug",
		"--backoff", "10s",
		"--max-retries", "5",
		"--stable-threshold", "10m0s",
		"--graceful-stop-delay", "30s",
	}

	if len(args) != len(expected) {
		t.Fatalf("args length = %d, want %d\ngot:  %v\nwant: %v", len(args), len(expected), args, expected)
	}
	for i := range expected {
		if args[i] != expected[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], expected[i])
		}
	}
}

func TestDaemon_BuildDaemonSet_ImageOverride(t *testing.T) {
	d := NewMpsdDaemon(&v1alpha1.MpsDaemonSpec{
		Image: &v1alpha1.ImageSpec{
			Tag: "custom-tag",
		},
	})

	ds := d.BuildDaemonSet(defaultOpts())
	ctr := ds.Spec.Template.Spec.Containers[0]

	want := "fake.io/org/mpsd:custom-tag"
	if ctr.Image != want {
		t.Errorf("container image = %q, want %q", ctr.Image, want)
	}
}

func defaultOpts() daemonmgr.BuildOptions {
	return daemonmgr.BuildOptions{
		Namespace:    "gpu-sharing-system",
		NodeSelector: map[string]string{"nvidia.com/gpu.present": "true"},
		DefaultImages: map[string]v1alpha1.ImageSpec{
			"mpsd": {
				Repository: "fake.io/org/mpsd",
				Tag:        "v0.1.0",
			},
		},
	}
}
