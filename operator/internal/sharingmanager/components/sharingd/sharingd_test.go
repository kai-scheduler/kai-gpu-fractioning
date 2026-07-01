package sharingd

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/common/daemonmgr"
)

func TestDaemon_BuildDaemonSet_Basics(t *testing.T) {
	d := NewSharingdDaemon(nil)

	if got := d.Name(); got != "sharingd" {
		t.Errorf("Name() = %q, expected %q", got, "sharingd")
	}

	ds := d.BuildDaemonSet(defaultOpts())

	if ds.Name != "gpu-sharing-sharingd" {
		t.Errorf("DaemonSet name = %q, expected %q", ds.Name, "gpu-sharing-sharingd")
	}
	if ds.Namespace != "gpu-sharing-system" {
		t.Errorf("DaemonSet namespace = %q, expected %q", ds.Namespace, "gpu-sharing-system")
	}

	spec := ds.Spec.Template.Spec
	if !spec.HostPID {
		t.Error("expected HostPID=true")
	}

	// Node selector
	if spec.NodeSelector == nil || spec.NodeSelector["nvidia.com/gpu.present"] != "true" {
		t.Errorf("NodeSelector = %v, expected nvidia.com/gpu.present=true", spec.NodeSelector)
	}

	// Container
	if len(spec.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(spec.Containers))
	}

	ctr := spec.Containers[0]
	if ctr.Image != "fake.io/org/sharingd:v0.1.0" {
		t.Errorf("container image = %q, want %q", ctr.Image, "fake.io/org/sharingd:v0.1.0")
	}
	if ctr.SecurityContext == nil || ctr.SecurityContext.Privileged == nil || !*ctr.SecurityContext.Privileged {
		t.Error("expected privileged=true")
	}

	// Volumes
	volumePaths := make(map[string]string)
	for _, v := range spec.Volumes {
		if v.HostPath != nil {
			volumePaths[v.Name] = v.HostPath.Path
		}
	}
	if volumePaths["nri-socket"] != "/var/run/nri" {
		t.Errorf("nri-socket volume path = %q, expected %q", volumePaths["nri-socket"], "/var/run/nri")
	}
	if volumePaths["mps-pipe"] != "/run/nvidia-mps" {
		t.Errorf("mps-pipe volume path = %q, expected %q", volumePaths["mps-pipe"], "/run/nvidia-mps")
	}

	// Volume mounts
	mountPaths := make(map[string]string)
	for _, m := range ctr.VolumeMounts {
		mountPaths[m.Name] = m.MountPath
	}
	if mountPaths["nri-socket"] != "/var/run/nri" {
		t.Errorf("nri-socket mount = %q, expected %q", mountPaths["nri-socket"], "/var/run/nri")
	}
	if mountPaths["mps-pipe"] != "/run/nvidia-mps" {
		t.Errorf("mps-pipe mount = %q, expected %q", mountPaths["mps-pipe"], "/run/nvidia-mps")
	}

	// Labels
	labels := ds.Labels
	if labels[daemonmgr.LabelManagedBy] != daemonmgr.ManagedByValue {
		t.Errorf("missing managed-by label, got %v", labels)
	}
	if labels[daemonmgr.LabelComponent] != "sharingd" {
		t.Errorf("missing component label, got %v", labels)
	}

	// No args when spec is nil
	args := ctr.Args
	if len(args) != 0 {
		t.Errorf("expected no args when spec is nil, got %v", args)
	}
}

func TestDaemon_BuildDaemonSet_Args(t *testing.T) {
	d := NewSharingdDaemon(&v1alpha1.SharingAgentSpec{
		AnnotationPrefix: "custom.prefix.",
		FailOpen:         ptr.To(true),
		NRISocketPath:    "/custom/nri.sock",
		LogLevel:         "debug",
		RetryInterval:    &metav1.Duration{Duration: 10 * time.Second},
		StableThreshold:  &metav1.Duration{Duration: 10 * time.Minute},
		MaxRetries:       ptr.To(int32(5)),
	})

	ds := d.BuildDaemonSet(defaultOpts())
	args := ds.Spec.Template.Spec.Containers[0].Args

	expected := []string{
		"--annotation-prefix", "custom.prefix.",
		"--fail-open",
		"--socket-path", "/custom/nri.sock",
		"--log-level", "debug",
		"--retry-interval", "10s",
		"--stable-threshold", "10m0s",
		"--max-retries", "5",
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

func defaultOpts() daemonmgr.BuildOptions {
	return daemonmgr.BuildOptions{
		Namespace:    "gpu-sharing-system",
		NodeSelector: map[string]string{"nvidia.com/gpu.present": "true"},
		DefaultImages: map[string]v1alpha1.ImageSpec{
			"sharingd": {
				Repository: "fake.io/org/sharingd",
				Tag:        "v0.1.0",
			},
		},
	}
}
