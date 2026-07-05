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
	defaultMPSPipeDir   = "/run/nvidia-mps"
	defaultNRISocketDir = "/var/run/nri"

	volumeNRISocket = "nri-socket"
	volumeMPSPipe   = "mps-pipe"
)

// daemon implements daemonmgr.ManagedDaemon for the sharingd NRI plugin.
type daemon struct {
	spec *v1alpha1.SharingAgentSpec
}

// NewSharingdDaemon returns a ManagedDaemon for the sharingd NRI plugin.
func NewSharingdDaemon(spec *v1alpha1.SharingAgentSpec) daemonmgr.ManagedDaemon {
	return &daemon{spec: spec}
}

func (d *daemon) Name() string { return daemonName }

// BuildDaemonSet constructs the desired DaemonSet for the sharingd
// The pod runs privileged with host PID visibility so that sharingd can
// register as an NRI plugin in the host containerd and observe container
// lifecycle events. Two host paths are mounted:
//   - The NRI socket directory, so sharingd can connect to containerd's NRI endpoint.
//   - The MPS pipe directory, shared with the mpsd daemon for GPU multiplexing control.
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

	// HostPID is required so sharingd can observe container lifecycle events on the node.
	result.Spec.Template.Spec.NodeSelector = opts.NodeSelector
	result.Spec.Template.Spec.HostPID = true

	// Merge the Helm default with any CRD-level image override.
	image := opts.DefaultImages[daemonName]
	if d.spec != nil && d.spec.Image != nil {
		image = mergeImageSpec(image, *d.spec.Image)
	}

	container, volumes := d.buildSharingdContainer(image)
	result.Spec.Template.Spec.Containers = append(result.Spec.Template.Spec.Containers, container)
	result.Spec.Template.Spec.Volumes = append(result.Spec.Template.Spec.Volumes, volumes...)

	return result
}

// buildSharingdContainer returns the main sharingd container and its required
// host-path volumes. The container runs privileged to access the NRI socket
// and MPS pipe on the host. CLI arguments are derived from the CRD SharingAgentSpec.
func (d *daemon) buildSharingdContainer(image v1alpha1.ImageSpec) (corev1.Container, []corev1.Volume) {
	pullPolicy := corev1.PullIfNotPresent
	if image.ImagePullPolicy != "" {
		pullPolicy = corev1.PullPolicy(image.ImagePullPolicy)
	}

	container := corev1.Container{
		Name:            daemonName,
		Image:           image.FullImage(),
		ImagePullPolicy: pullPolicy,
		SecurityContext: daemonmgr.PrivilegedSecurityContext(),
		Args:            d.buildArgs(),
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      volumeNRISocket,
				MountPath: d.nriSocketDir(),
			},
			{
				Name:      volumeMPSPipe,
				MountPath: defaultMPSPipeDir,
			},
		},
	}

	// Host paths so the container can reach containerd's NRI endpoint
	// and the MPS control pipe shared with the mpsd daemon.
	volumes := []corev1.Volume{
		{
			Name: volumeNRISocket,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: d.nriSocketDir(),
				},
			},
		},
		{
			Name: volumeMPSPipe,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: defaultMPSPipeDir,
				},
			},
		},
	}

	return container, volumes
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
