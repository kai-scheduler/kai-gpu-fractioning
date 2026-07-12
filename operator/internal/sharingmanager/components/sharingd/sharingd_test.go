package sharingd

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/common/daemonmgr"
)

func TestDaemon_BuildDaemonSet_Basics(t *testing.T) {
	d := NewSharingdDaemon(nil, &v1alpha1.MetricsAgentSpec{Enabled: true})

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

	// Two containers: sharingd (the NRI plugin) + metricsd (the metrics sidecar).
	if len(spec.Containers) != 2 {
		t.Fatalf("expected 2 containers (sharingd + metricsd), got %d", len(spec.Containers))
	}

	ctr := containerByName(t, spec.Containers, "sharingd")
	if ctr.Image != "fake.io/org/sharingd:v0.1.0" {
		t.Errorf("container image = %q, want %q", ctr.Image, "fake.io/org/sharingd:v0.1.0")
	}
	if ctr.SecurityContext == nil || ctr.SecurityContext.Privileged == nil || !*ctr.SecurityContext.Privileged {
		t.Error("expected privileged=true")
	}

	// metricsd sidecar: image, privileged, metrics port, read-only shared map mount.
	metricsd := containerByName(t, spec.Containers, "metricsd")
	if metricsd.Image != "fake.io/org/metricsd:v0.1.0" {
		t.Errorf("metricsd image = %q, want %q", metricsd.Image, "fake.io/org/metricsd:v0.1.0")
	}
	if len(metricsd.Ports) != 1 || metricsd.Ports[0].ContainerPort != 2112 {
		t.Errorf("expected metricsd to expose port 2112, got %v", metricsd.Ports)
	}
	if !hasMount(metricsd.VolumeMounts, "map-dir", "/var/run/gpu-sharing/map", true) {
		t.Errorf("expected metricsd to mount map-dir read-only, got %v", metricsd.VolumeMounts)
	}
	if !hasMount(ctr.VolumeMounts, "map-dir", "/var/run/gpu-sharing/map", false) {
		t.Errorf("expected sharingd to mount map-dir read-write, got %v", ctr.VolumeMounts)
	}

	// The shared map dir is an emptyDir (single pod owns writer + reader).
	var mapDirIsEmptyDir bool
	for _, v := range spec.Volumes {
		if v.Name == "map-dir" {
			mapDirIsEmptyDir = v.EmptyDir != nil
		}
	}
	if !mapDirIsEmptyDir {
		t.Errorf("expected map-dir to be an emptyDir volume")
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

func TestDaemon_BuildDaemonSet_MetricsDisabled(t *testing.T) {
	d := NewSharingdDaemon(nil, &v1alpha1.MetricsAgentSpec{Enabled: false})

	spec := d.BuildDaemonSet(defaultOpts()).Spec.Template.Spec
	if len(spec.Containers) != 1 {
		t.Fatalf("expected only the sharingd container when metrics disabled, got %d", len(spec.Containers))
	}
	if spec.Containers[0].Name != "sharingd" {
		t.Errorf("expected sole container to be sharingd, got %q", spec.Containers[0].Name)
	}
}

// The readiness probe must hit the binary's /readyz endpoint so a sharingd
// that never registers with NRI reports Ready=False (and the node condition
// stays false) instead of a false-positive AllDaemonsReady.
func TestDaemon_BuildDaemonSet_ReadinessProbe(t *testing.T) {
	d := NewSharingdDaemon(nil, nil)
	ds := d.BuildDaemonSet(defaultOpts())
	ctr := ds.Spec.Template.Spec.Containers[0]

	probe := ctr.ReadinessProbe
	if probe == nil {
		t.Fatal("expected a readiness probe on the sharingd container")
	}
	httpGet := probe.HTTPGet
	if httpGet == nil {
		t.Fatal("expected an httpGet readiness probe")
	}
	if httpGet.Path != "/readyz" {
		t.Errorf("probe path = %q, expected %q", httpGet.Path, "/readyz")
	}
	if httpGet.Port.IntValue() != readinessPort {
		t.Errorf("probe port = %d, expected %d", httpGet.Port.IntValue(), readinessPort)
	}

	var found bool
	for _, p := range ctr.Ports {
		if p.ContainerPort == readinessPort {
			found = true
		}
	}
	if !found {
		t.Errorf("expected container port %d to be declared, got %v", readinessPort, ctr.Ports)
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
	}, nil)

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
			"metricsd": {
				Repository: "fake.io/org/metricsd",
				Tag:        "v0.1.0",
			},
		},
	}
}

func containerByName(t *testing.T, containers []corev1.Container, name string) corev1.Container {
	t.Helper()
	for _, c := range containers {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("container %q not found", name)
	return corev1.Container{}
}

func hasMount(mounts []corev1.VolumeMount, name, path string, readOnly bool) bool {
	for _, m := range mounts {
		if m.Name == name && m.MountPath == path && m.ReadOnly == readOnly {
			return true
		}
	}
	return false
}
