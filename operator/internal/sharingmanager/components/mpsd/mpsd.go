package mpsd

import (
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/common/daemonmgr"
)

const (
	daemonName = "mpsd"

	defaultMPSPipeDir = "/run/nvidia-mps"
	defaultMPSLogDir  = "/var/log/nvidia-mps"

	volumeMPSPipe = "mps-pipe"
	volumeMPSLog  = "mps-log"

	nvidiaRuntimeClass = "nvidia"
)

type daemon struct {
	spec *v1alpha1.MpsDaemonSpec
}

// NewMpsdDaemon returns a ManagedDaemon for the MPS daemon supervisor.
func NewMpsdDaemon(spec *v1alpha1.MpsDaemonSpec) daemonmgr.ManagedDaemon {
	return &daemon{spec: spec}
}

func (d *daemon) Name() string { return daemonName }

// BuildDaemonSet constructs the desired DaemonSet for mpsd.
// The pod runs privileged with the nvidia runtime class so it has
// access to GPU devices and the nvidia-cuda-mps-control binary.
// Two host paths are mounted:
//   - The MPS pipe directory, shared with sharingd and GPU workload containers.
//   - The MPS log directory, for nvidia-cuda-mps-control daemon logs.
func (d *daemon) BuildDaemonSet(opts daemonmgr.BuildOptions) *appsv1.DaemonSet {
	result := daemonmgr.BaseDaemonSet(daemonName, opts.Namespace)

	result.Spec.Template.Spec.NodeSelector = opts.NodeSelector
	result.Spec.Template.Spec.RuntimeClassName = ptr.To(nvidiaRuntimeClass)

	image := opts.DefaultImages[daemonName]
	if d.spec != nil && d.spec.Image != nil {
		image = image.MergeWith(*d.spec.Image)
	}

	container, volumes := d.buildContainer(image)
	result.Spec.Template.Spec.Containers = append(result.Spec.Template.Spec.Containers, container)
	result.Spec.Template.Spec.Volumes = append(result.Spec.Template.Spec.Volumes, volumes...)

	return result
}

func (d *daemon) buildContainer(image v1alpha1.ImageSpec) (corev1.Container, []corev1.Volume) {
	pullPolicy := corev1.PullIfNotPresent
	if image.ImagePullPolicy != "" {
		pullPolicy = corev1.PullPolicy(image.ImagePullPolicy)
	}

	hostPathDirOrCreate := corev1.HostPathDirectoryOrCreate

	// The MPS control socket at /run/nvidia-mps/control is created by
	// nvidia-cuda-mps-control once it finishes initialising. Checking for
	// its existence is the lightest and most reliable readiness signal:
	// no GPU driver calls, no extra binaries, and it directly proves the
	// daemon is accepting client connections.
	mpsControlSocketProbe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			Exec: &corev1.ExecAction{
				Command: []string{"test", "-S", defaultMPSPipeDir + "/control"},
			},
		},
		InitialDelaySeconds: 5,
		PeriodSeconds:       10,
	}

	container := corev1.Container{
		Name:            daemonName,
		Image:           image.FullImage(),
		ImagePullPolicy: pullPolicy,
		SecurityContext: daemonmgr.PrivilegedSecurityContext(),
		Args:            d.buildArgs(),
		ReadinessProbe:  mpsControlSocketProbe,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      volumeMPSPipe,
				MountPath: defaultMPSPipeDir,
			},
			{
				Name:      volumeMPSLog,
				MountPath: defaultMPSLogDir,
			},
		},
	}

	volumes := []corev1.Volume{
		{
			Name: volumeMPSPipe,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: defaultMPSPipeDir,
					Type: &hostPathDirOrCreate,
				},
			},
		},
		{
			Name: volumeMPSLog,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: defaultMPSLogDir,
					Type: &hostPathDirOrCreate,
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

	if spec.LogLevel != "" {
		args = append(args, "--log-level", spec.LogLevel)
	}
	if spec.Backoff != nil {
		args = append(args, "--backoff", spec.Backoff.Duration.String())
	}
	if spec.MaxRetries != nil {
		args = append(args, "--max-retries", strconv.Itoa(int(*spec.MaxRetries)))
	}
	if spec.StableThreshold != nil {
		args = append(args, "--stable-threshold", spec.StableThreshold.Duration.String())
	}
	if spec.GracefulStopDelay != nil {
		args = append(args, "--graceful-stop-delay", spec.GracefulStopDelay.Duration.String())
	}

	return args
}
