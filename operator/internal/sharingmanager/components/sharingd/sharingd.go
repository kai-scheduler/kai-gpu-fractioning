package sharingd

import (
	"log/slog"
	"net"
	"path/filepath"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	v1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/common/daemonmgr"
)

const (
	daemonName          = "sharingd"
	metricsdName        = "metricsd"
	defaultMPSPipeDir   = "/run/nvidia-mps"
	defaultNRISocketDir = "/var/run/nri"
	// containerPodMapDir is the shared handoff directory: sharingd writes the
	// container→pod mapping here and the metricsd sidecar reads it. Must match
	// sharingd's and metricsd's built-in default (fsstore.DefaultMapDir).
	containerPodMapDir = "/var/run/gpu-sharing/map"
	// metricsPort is the Prometheus exporter port served by the metricsd sidecar.
	metricsPort = 2112

	volumeNRISocket = "nri-socket"
	volumeMPSPipe   = "mps-pipe"
	volumeMapDir    = "map-dir"

	// defaultReadinessPort must match the sharingd binary's default
	// (sharing-manager/sharingd): it serves /readyz on --readiness-port. Used
	// when spec.readinessPort is unset, in which case no argument is passed to
	// the binary — keeping older sharingd images (without the flag) compatible.
	defaultReadinessPort = 8093
	readyzPath           = "/readyz"
)

// daemon implements daemonmgr.ManagedDaemon for the sharingd NRI plugin plus its
// metricsd metrics sidecar. Both run in a single DaemonSet pod.
type daemon struct {
	sharingSpec *v1alpha1.SharingAgentSpec
	metricsSpec *v1alpha1.MetricsAgentSpec
}

// NewSharingdDaemon returns a ManagedDaemon for the sharingd NRI plugin. The
// metricsAgent spec (may be nil) configures the co-located metricsd sidecar.
func NewSharingdDaemon(spec *v1alpha1.SharingAgentSpec, metrics *v1alpha1.MetricsAgentSpec) daemonmgr.ManagedDaemon {
	return &daemon{sharingSpec: spec, metricsSpec: metrics}
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
	result := daemonmgr.BaseDaemonSet(daemonName, opts.Namespace)

	podSpec := &result.Spec.Template.Spec
	// HostPID is required so sharingd can observe container lifecycle events and
	// so metricsd can resolve NVML host PIDs through its own /proc.
	podSpec.NodeSelector = opts.NodeSelector
	podSpec.HostPID = true

	sharingdImage := opts.DefaultImages[daemonName]
	if d.sharingSpec != nil && d.sharingSpec.Image != nil {
		sharingdImage = sharingdImage.MergeWith(*d.sharingSpec.Image)
	}
	sharingdContainer, sharingdVolumes := d.buildSharingdContainer(sharingdImage)
	podSpec.Volumes = append(podSpec.Volumes, sharingdVolumes...)

	metricsOn := d.metricsEnabled()
	if metricsOn {
		vol, mount := metricsSharedVolume()
		podSpec.Volumes = append(podSpec.Volumes, vol)
		sharingdContainer.VolumeMounts = append(sharingdContainer.VolumeMounts, mount)
	}
	// sharingdContainer is a value type: append must happen after the map-dir mount
	// is added so the VolumeMount is included in the copy placed into the slice.
	podSpec.Containers = append(podSpec.Containers, sharingdContainer)
	if metricsOn {
		d.applyMetricsSidecar(result, opts.DefaultImages)
	}

	return result
}

func (d *daemon) metricsEnabled() bool {
	return d.metricsSpec != nil && d.metricsSpec.Enabled
}

// metricsSharedVolume returns the map-dir emptyDir volume and the corresponding
// mount for the sharingd container (writer side of the container→pod mapping handoff).
func metricsSharedVolume() (corev1.Volume, corev1.VolumeMount) {
	return corev1.Volume{
			Name:         volumeMapDir,
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
		corev1.VolumeMount{Name: volumeMapDir, MountPath: containerPodMapDir}
}

// applyMetricsSidecar adds the metricsd container and Prometheus scrape
// annotations to the DaemonSet.
func (d *daemon) applyMetricsSidecar(result *appsv1.DaemonSet, defaultImages map[string]v1alpha1.ImageSpec) {
	metricsImage := defaultImages[metricsdName]
	if d.metricsSpec != nil && d.metricsSpec.Image != nil {
		metricsImage = metricsImage.MergeWith(*d.metricsSpec.Image)
	}
	result.Spec.Template.Spec.Containers = append(result.Spec.Template.Spec.Containers, d.buildMetricsdContainer(metricsImage))
	if d.metricsSpec != nil {
		result.Spec.Template.Spec.Volumes = append(result.Spec.Template.Spec.Volumes, d.metricsSpec.ExtraVolumes...)
	}

	if result.Spec.Template.Annotations == nil {
		result.Spec.Template.Annotations = map[string]string{}
	}
	result.Spec.Template.Annotations["prometheus.io/scrape"] = "true"
	result.Spec.Template.Annotations["prometheus.io/port"] = d.metricsAnnotationPort()
	result.Spec.Template.Annotations["prometheus.io/path"] = d.metricsAnnotationPath()
}

// metricsAnnotationPort returns the port string for the prometheus.io/port
// annotation, derived from the configured address or falling back to the default.
func (d *daemon) metricsAnnotationPort() string {
	if d.metricsSpec != nil && d.metricsSpec.Address != "" {
		_, port, err := net.SplitHostPort(d.metricsSpec.Address)
		if err != nil {
			slog.Warn("invalid metricsAgent.address, falling back to default port", "address", d.metricsSpec.Address, "err", err)
		} else if port != "" {
			return port
		}
	}
	return strconv.Itoa(metricsPort)
}

// metricsAnnotationPath returns the path for the prometheus.io/path annotation.
func (d *daemon) metricsAnnotationPath() string {
	if d.metricsSpec != nil && d.metricsSpec.Path != "" {
		return d.metricsSpec.Path
	}
	return "/metrics"
}

// buildSharingdContainer returns the main sharingd container and its required
// host-path volumes. The map-dir mount is added by the caller when metricsd is enabled.
func (d *daemon) buildSharingdContainer(image v1alpha1.ImageSpec) (corev1.Container, []corev1.Volume) {
	// sharingd retries its NRI connection indefinitely by default, so the
	// process stays Running even when it never registers with containerd. The
	// binary serves /readyz on the readiness port, flipping to ready only once
	// the NRI plugin is registered and synchronized — probing it keeps the pod
	// (and the node's gpu-sharing Ready condition) not-ready until sharingd
	// actually works.
	readinessProbe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: readyzPath,
				Port: intstr.FromInt32(d.readinessPort()),
			},
		},
		InitialDelaySeconds: 5,
		PeriodSeconds:       10,
	}

	container := corev1.Container{
		Name:            daemonName,
		Image:           image.FullImage(),
		ImagePullPolicy: pullPolicy(image),
		SecurityContext: daemonmgr.PrivilegedSecurityContext(),
		Args:            d.buildArgs(),
		ReadinessProbe:  readinessProbe,
		Ports: []corev1.ContainerPort{
			{
				Name:          "readiness",
				ContainerPort: d.readinessPort(),
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: volumeNRISocket, MountPath: d.nriSocketDir()},
			{Name: volumeMPSPipe, MountPath: defaultMPSPipeDir},
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
// directory (read-only) and exports GPU metrics on metricsPort. PID→pod
// attribution works via the pod's host PID namespace.
func (d *daemon) buildMetricsdContainer(image v1alpha1.ImageSpec) corev1.Container {
	container := corev1.Container{
		Name:            metricsdName,
		Image:           image.FullImage(),
		ImagePullPolicy: pullPolicy(image),
		SecurityContext: daemonmgr.PrivilegedSecurityContext(),
		Args:            d.buildMetricsdArgs(),
		Ports: []corev1.ContainerPort{
			{Name: "metrics", ContainerPort: metricsPort, Protocol: corev1.ProtocolTCP},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: volumeMapDir, MountPath: containerPodMapDir, ReadOnly: true},
		},
	}

	// Advanced pass-through: append caller-supplied env and mounts (empty by
	// default). Used by non-standard deployments to point the collector at an
	// alternate driver library; the matching volumes are added in BuildDaemonSet.
	if d.metricsSpec != nil {
		container.Env = append(container.Env, d.metricsSpec.ExtraEnv...)
		container.VolumeMounts = append(container.VolumeMounts, d.metricsSpec.ExtraVolumeMounts...)
	}

	return container
}

func (d *daemon) buildMetricsdArgs() []string {
	if d.metricsSpec == nil {
		return nil
	}
	var args []string
	if d.metricsSpec.LogLevel != "" {
		args = append(args, "--log-level", d.metricsSpec.LogLevel)
	}
	if d.metricsSpec.Address != "" {
		args = append(args, "--metrics-address", d.metricsSpec.Address)
	}
	if d.metricsSpec.Path != "" {
		args = append(args, "--metrics-path", d.metricsSpec.Path)
	}
	return args
}

func (d *daemon) buildArgs() []string {
	spec := d.sharingSpec
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
	// Port for the /readyz readiness endpoint. Only passed when explicitly
	// configured; otherwise the binary's default is left to match
	// defaultReadinessPort, so older images without the flag keep working.
	if spec.ReadinessPort != nil {
		args = append(args, "--readiness-port", strconv.Itoa(int(*spec.ReadinessPort)))
	}

	return args
}

// readinessPort returns the port the readiness probe (and container port)
// target: the CRD override when set, the binary's default otherwise.
func (d *daemon) readinessPort() int32 {
	if d.sharingSpec != nil && d.sharingSpec.ReadinessPort != nil {
		return *d.sharingSpec.ReadinessPort
	}
	return defaultReadinessPort
}

// nriSocketDir returns the directory containing the NRI socket.
// The volume mount needs the directory, not the socket file itself.
func (d *daemon) nriSocketDir() string {
	if d.sharingSpec == nil || d.sharingSpec.NRISocketPath == "" {
		return defaultNRISocketDir
	}

	return filepath.Dir(d.sharingSpec.NRISocketPath)
}

// pullPolicy resolves the image pull policy, defaulting to IfNotPresent.
func pullPolicy(image v1alpha1.ImageSpec) corev1.PullPolicy {
	if image.ImagePullPolicy != "" {
		return corev1.PullPolicy(image.ImagePullPolicy)
	}
	return corev1.PullIfNotPresent
}
