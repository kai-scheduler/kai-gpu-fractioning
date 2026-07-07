package sharingd

import (
	"fmt"
	"path/filepath"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	v1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/common/daemonmgr"
)

const (
	daemonName          = "sharingd"
	metricsdName        = "metricsd"
	defaultMPSPipeDir   = "/run/nvidia-mps"
	defaultNRISocketDir = "/var/run/nri"
	// defaultMapDir is the shared handoff directory: sharingd writes the
	// container→pod mapping here and the metricsd sidecar reads it. Must match
	// sharingd's and metricsd's built-in default (fsstore.DefaultMapDir).
	defaultMapDir = "/var/run/gpu-sharing/map"
	// metricsPort is the Prometheus exporter port served by the metricsd sidecar.
	metricsPort = 2112

	volumeNRISocket = "nri-socket"
	volumeMPSPipe   = "mps-pipe"
	volumeMapDir    = "map-dir"
)

// daemon implements daemonmgr.ManagedDaemon for the sharingd NRI plugin plus its
// metricsd metrics sidecar. Both run in a single DaemonSet pod.
type daemon struct {
	spec    *v1alpha1.SharingAgentSpec
	metrics *v1alpha1.MetricsAgentSpec
}

// NewSharingdDaemon returns a ManagedDaemon for the sharingd NRI plugin. The
// metricsAgent spec (may be nil) configures the co-located metricsd sidecar.
func NewSharingdDaemon(spec *v1alpha1.SharingAgentSpec, metrics *v1alpha1.MetricsAgentSpec) daemonmgr.ManagedDaemon {
	return &daemon{spec: spec, metrics: metrics}
}

func (d *daemon) Name() string { return daemonName }

// BuildDaemonSet constructs the desired DaemonSet. It is a single DaemonSet with
// two containers:
//
//   - sharingd: the NRI plugin. Runs privileged with host PID visibility so it can
//     register with the host containerd's NRI endpoint and observe container
//     lifecycle. It writes the container→pod mapping to a shared emptyDir.
//   - metricsd: the metrics sidecar. Reads the same mapping (read-only) and exports
//     per-pod GPU metrics on :2112. It relies on the pod's host PID namespace to
//     resolve NVML-reported host PIDs to cgroups (so no /host/proc mount is
//     needed), and runs privileged for NVML device access.
//
// Host paths mounted: the NRI socket directory (sharingd) and the MPS pipe
// directory (shared with mpsd). The map directory is an emptyDir shared between
// the two containers — a single pod owns both writer and reader, and the mapping
// is rebuilt on every NRI (re)connect, so it need not survive a pod restart.
func (d *daemon) BuildDaemonSet(opts daemonmgr.BuildOptions) *appsv1.DaemonSet {
	labels := map[string]string{
		daemonmgr.LabelManagedBy: daemonmgr.ManagedByValue,
		daemonmgr.LabelComponent: daemonName,
	}

	result := daemonmgr.BaseDaemonSet(
		fmt.Sprintf("gpu-sharing-%s", daemonName),
		opts.Namespace,
		labels,
	)

	podSpec := &result.Spec.Template.Spec
	// HostPID is required so sharingd can observe container lifecycle events and
	// so metricsd can resolve NVML host PIDs through its own /proc.
	podSpec.NodeSelector = opts.NodeSelector
	podSpec.HostPID = true

	// Shared handoff volume between sharingd (writer) and metricsd (reader).
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name:         volumeMapDir,
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	})

	sharingdImage := opts.DefaultImages[daemonName]
	if d.spec != nil && d.spec.Image != nil {
		sharingdImage = mergeImageSpec(sharingdImage, *d.spec.Image)
	}
	container, volumes := d.buildSharingdContainer(sharingdImage)
	podSpec.Containers = append(podSpec.Containers, container)
	podSpec.Volumes = append(podSpec.Volumes, volumes...)

	// metricsd sidecar (enabled by default).
	if d.metricsEnabled() {
		metricsImage := opts.DefaultImages[metricsdName]
		if d.metrics != nil && d.metrics.Image != nil {
			metricsImage = mergeImageSpec(metricsImage, *d.metrics.Image)
		}
		podSpec.Containers = append(podSpec.Containers, d.buildMetricsdContainer(metricsImage))

		// Prometheus pod-scrape discovery for the metrics port.
		if result.Spec.Template.Annotations == nil {
			result.Spec.Template.Annotations = map[string]string{}
		}
		result.Spec.Template.Annotations["prometheus.io/scrape"] = "true"
		result.Spec.Template.Annotations["prometheus.io/port"] = strconv.Itoa(metricsPort)
		result.Spec.Template.Annotations["prometheus.io/path"] = "/metrics"
	}

	return result
}

// metricsEnabled reports whether the metricsd sidecar should be deployed. It
// defaults to true and is disabled only by an explicit Enabled=false.
func (d *daemon) metricsEnabled() bool {
	if d.metrics != nil && d.metrics.Enabled != nil {
		return *d.metrics.Enabled
	}
	return true
}

// buildSharingdContainer returns the main sharingd container and its required
// host-path volumes, plus a mount of the shared map directory it writes to.
func (d *daemon) buildSharingdContainer(image v1alpha1.ImageSpec) (corev1.Container, []corev1.Volume) {
	container := corev1.Container{
		Name:            daemonName,
		Image:           image.FullImage(),
		ImagePullPolicy: pullPolicy(image),
		SecurityContext: daemonmgr.PrivilegedSecurityContext(),
		Args:            d.buildArgs(),
		VolumeMounts: []corev1.VolumeMount{
			{Name: volumeNRISocket, MountPath: d.nriSocketDir()},
			{Name: volumeMPSPipe, MountPath: defaultMPSPipeDir},
			{Name: volumeMapDir, MountPath: defaultMapDir},
		},
	}

	// Host paths so the container can reach containerd's NRI endpoint and the MPS
	// control pipe shared with the mpsd daemon.
	volumes := []corev1.Volume{
		{
			Name: volumeNRISocket,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: d.nriSocketDir()},
			},
		},
		{
			Name: volumeMPSPipe,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: defaultMPSPipeDir},
			},
		},
	}

	return container, volumes
}

// buildMetricsdContainer returns the metricsd sidecar. It reads the shared map
// directory (read-only) and exports GPU metrics on metricsPort. All of metricsd's
// defaults (map dir, :2112, /proc) already match this deployment, so it needs no
// arguments; PID→pod attribution works via the pod's host PID namespace.
func (d *daemon) buildMetricsdContainer(image v1alpha1.ImageSpec) corev1.Container {
	return corev1.Container{
		Name:            metricsdName,
		Image:           image.FullImage(),
		ImagePullPolicy: pullPolicy(image),
		SecurityContext: daemonmgr.PrivilegedSecurityContext(),
		Ports: []corev1.ContainerPort{
			{Name: "metrics", ContainerPort: metricsPort, Protocol: corev1.ProtocolTCP},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: volumeMapDir, MountPath: defaultMapDir, ReadOnly: true},
		},
	}
}

func (d *daemon) buildArgs() []string {
	spec := d.spec
	if spec == nil {
		return nil
	}

	var args []string

	// Pod annotation key prefix used to read per-container GPU memory requests.
	if spec.AnnotationPrefix != "" {
		args = append(args, "--annotation-prefix", spec.AnnotationPrefix)
	}
	// When set, allow container creation even if GPU sharing setup fails.
	if spec.FailOpen != nil && *spec.FailOpen {
		args = append(args, "--fail-open")
	}
	// Path to the NRI socket file on the host for registering as a containerd plugin.
	if spec.NRISocketPath != "" {
		args = append(args, "--socket-path", spec.NRISocketPath)
	}
	// Logging verbosity (e.g. "debug", "info", "warn", "error").
	if spec.LogLevel != "" {
		args = append(args, "--log-level", spec.LogLevel)
	}
	// How long to wait between MPS control retries on transient failures.
	if spec.RetryInterval != nil {
		args = append(args, "--retry-interval", spec.RetryInterval.Duration.String())
	}
	// Duration a GPU must remain stable before sharingd considers it healthy.
	if spec.StableThreshold != nil {
		args = append(args, "--stable-threshold", spec.StableThreshold.Duration.String())
	}
	// Maximum number of MPS control retries before giving up.
	if spec.MaxRetries != nil {
		args = append(args, "--max-retries", strconv.Itoa(int(*spec.MaxRetries)))
	}

	return args
}

// nriSocketDir returns the directory containing the NRI socket.
// The volume mount needs the directory, not the socket file itself.
func (d *daemon) nriSocketDir() string {
	if d.spec == nil || d.spec.NRISocketPath == "" {
		return defaultNRISocketDir
	}

	return filepath.Dir(d.spec.NRISocketPath)
}

// pullPolicy resolves the image pull policy, defaulting to IfNotPresent.
func pullPolicy(image v1alpha1.ImageSpec) corev1.PullPolicy {
	if image.ImagePullPolicy != "" {
		return corev1.PullPolicy(image.ImagePullPolicy)
	}
	return corev1.PullIfNotPresent
}

// mergeImageSpec returns base with any non-empty fields from override applied.
func mergeImageSpec(base, override v1alpha1.ImageSpec) v1alpha1.ImageSpec {
	if override.Repository != "" {
		base.Repository = override.Repository
	}
	if override.Tag != "" {
		base.Tag = override.Tag
	}
	if override.ImagePullPolicy != "" {
		base.ImagePullPolicy = override.ImagePullPolicy
	}
	return base
}
