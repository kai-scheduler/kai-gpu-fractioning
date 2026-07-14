/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GpuSharingConfigSpec defines the desired state of GpuSharingConfig.
type GpuSharingConfigSpec struct {
	// nodeSelector determines which nodes the managed DaemonSets (sharingd, mpsd)
	// target. All daemons share the same selector.
	// Example: {"nvidia.com/gpu.present": "true"} (set by the Helm chart's default CR).
	//
	// TODO: Make immutable via a validating webhook. Changing nodeSelector on a
	// live CR would leave stale node conditions on nodes removed from the target
	// set. Until the webhook is in place, nodeSelector must not be changed after
	// initial creation.
	//
	// +required
	NodeSelector map[string]string `json:"nodeSelector"`

	// sharingAgent configures the NRI-based sharing agent (sharingd).
	// +optional
	SharingAgent *SharingAgentSpec `json:"sharingAgent,omitempty"`

	// metricsAgent configures the GPU metrics exporter (metricsd), which runs as
	// a sidecar container in the same DaemonSet as sharingd and reads the
	// container→pod mapping sharingd writes to a shared volume.
	// +optional
	MetricsAgent *MetricsAgentSpec `json:"metricsAgent,omitempty"`

	// mpsDaemon configures the MPS daemon supervisor (mpsd).
	// +optional
	MpsDaemon *MpsDaemonSpec `json:"mpsDaemon,omitempty"`
}

// ImageSpec defines a container image reference.
// The full image is constructed as <repository>:<tag>.
type ImageSpec struct {
	// repository is the full image path including registry and image name
	// (e.g. "gcr.io/run-ai-prod/sharingd").
	// +optional
	Repository string `json:"repository,omitempty"`

	// tag is the image tag (e.g. "v0.1.0"). Defaults to the chart appVersion.
	// +optional
	Tag string `json:"tag,omitempty"`

	// imagePullPolicy controls when the kubelet pulls the image.
	// One of Always, IfNotPresent, Never. Default: IfNotPresent.
	// +optional
	ImagePullPolicy string `json:"imagePullPolicy,omitempty"`
}

// SharingAgentSpec configures the sharingd DaemonSet managed by the controller.
type SharingAgentSpec struct {
	// image overrides the default sharingd container image set via Helm env vars (SHARINGD_IMAGE_*).
	// +optional
	Image *ImageSpec `json:"image,omitempty"`

	// annotationPrefix is the prefix for GPU memory annotations on pods.
	// Default: "nvidia.com/gpu-memory.container."
	// +optional
	AnnotationPrefix string `json:"annotationPrefix,omitempty"`

	// failOpen controls behavior when annotation parsing fails.
	// If true, the container is created without GPU memory limits.
	// Default: false
	// +optional
	FailOpen *bool `json:"failOpen,omitempty"`

	// nriSocketPath is the path to the NRI Unix socket used to communicate
	// with the container runtime (containerd/CRI-O). Default: /var/run/nri/nri.sock.
	// +optional
	NRISocketPath string `json:"nriSocketPath,omitempty"`

	// retroactiveEnforcement controls whether sharingd, on (re)connect to NRI,
	// stops GPU-sharing containers found running without passing through the NRI
	// plugin. Those containers lack the proper GPU-sharing setup and may harm
	// other containers on the GPU. Default: true.
	// +kubebuilder:default=true
	// +optional
	RetroactiveEnforcement bool `json:"retroactiveEnforcement"`

	// criSocketPath is the path to the CRI runtime socket sharingd uses to stop
	// containers during retroactive enforcement. It is mounted into the pod only
	// when retroactiveEnforcement is enabled. Default: /run/containerd/containerd.sock.
	// +optional
	CRISocketPath string `json:"criSocketPath,omitempty"`

	// logLevel controls the logging verbosity of the sharing agent.
	// One of debug, info, warn, error. Default: info.
	// +optional
	LogLevel string `json:"logLevel,omitempty"`

	// retryInterval is the initial wait duration before retrying a failed NRI connection.
	// Backoff doubles on each failure, capped at 60s. Default: 5s.
	// +optional
	RetryInterval *metav1.Duration `json:"retryInterval,omitempty"`

	// stableThreshold is how long an NRI connection must stay up to be considered stable.
	// Once stable, the retry budget and backoff reset. Default: 5m.
	// +optional
	StableThreshold *metav1.Duration `json:"stableThreshold,omitempty"`

	// maxRetries is the maximum number of NRI connection retries before giving up.
	// 0 means unlimited. Default: 0.
	// +optional
	MaxRetries *int32 `json:"maxRetries,omitempty"`

	// readinessPort is the port sharingd serves its /readyz endpoint on. When set,
	// the operator passes --readiness-port to the binary and points the readiness
	// probe at it. When unset, no argument is passed and the probe targets the
	// binary's built-in default (8093), keeping older sharingd images compatible.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	ReadinessPort *int32 `json:"readinessPort,omitempty"`
}

// MetricsAgentSpec configures the metricsd sidecar in the sharingd DaemonSet.
type MetricsAgentSpec struct {
	// image overrides the default metricsd container image set via Helm env vars
	// (METRICSD_IMAGE_*).
	// +optional
	Image *ImageSpec `json:"image,omitempty"`

	// enabled controls whether the metricsd sidecar is added to the DaemonSet.
	// When false, no metrics container is deployed.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// logLevel controls the logging verbosity of the metrics agent.
	// One of debug, info, warn, error. Default: info.
	// +optional
	LogLevel string `json:"logLevel,omitempty"`

	// address is the Prometheus exporter listen address. Default: :2112.
	// +optional
	Address string `json:"address,omitempty"`

	// path is the HTTP path on which metrics are served. Default: /metrics.
	// +optional
	Path string `json:"path,omitempty"`

	// runtimeClassName sets the RuntimeClass for the sharingd+metricsd pod so
	// the NVIDIA container runtime injects libnvidia-ml.so (required for NVML).
	// Defaults to "nvidia". Set to "" to use the node's default runtime class
	// (only safe when the default runtime already injects NVIDIA driver libraries).
	// +optional
	RuntimeClassName *string `json:"runtimeClassName,omitempty"`
}

// MpsDaemonSpec configures the mpsd DaemonSet managed by the controller.
// mpsd supervises nvidia-cuda-mps-control, providing GPU multi-process
// service to containers sharing a GPU.
type MpsDaemonSpec struct {
	// image overrides the default mpsd container image set via Helm env vars (MPSD_IMAGE_*).
	// +optional
	Image *ImageSpec `json:"image,omitempty"`

	// logLevel controls the logging verbosity of the MPS daemon supervisor.
	// One of debug, info, warn, error. Default: info.
	// +optional
	LogLevel string `json:"logLevel,omitempty"`

	// backoff is the initial delay before restarting the MPS daemon after a crash.
	// Doubles on each failure, capped at 60s. Default: 5s.
	// +optional
	Backoff *metav1.Duration `json:"backoff,omitempty"`

	// maxRetries is the maximum number of MPS daemon restart attempts before giving up.
	// 0 means unlimited. Default: 0.
	// +optional
	MaxRetries *int32 `json:"maxRetries,omitempty"`

	// stableThreshold is how long the MPS daemon must run to be considered stable.
	// Once stable, the retry budget and backoff reset. Default: 5m.
	// +optional
	StableThreshold *metav1.Duration `json:"stableThreshold,omitempty"`

	// gracefulStopDelay is how long to wait after sending "quit" to the MPS daemon
	// before force-killing it. Default: 60s.
	// +optional
	GracefulStopDelay *metav1.Duration `json:"gracefulStopDelay,omitempty"`
}

// GpuSharingConfigStatus defines the observed state of GpuSharingConfig.
type GpuSharingConfigStatus struct {
	// observedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the latest available observations of the config's state.
	// Known condition types: SharingdReady, MpsdReady, Ready.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'default'",message="GpuSharingConfig must be named 'default'"

// GpuSharingConfig is the Schema for the gpusharingconfigs API
type GpuSharingConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of GpuSharingConfig
	// +required
	Spec GpuSharingConfigSpec `json:"spec"`

	// status defines the observed state of GpuSharingConfig
	// +optional
	Status GpuSharingConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GpuSharingConfigList contains a list of GpuSharingConfig
type GpuSharingConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GpuSharingConfig `json:"items"`
}
