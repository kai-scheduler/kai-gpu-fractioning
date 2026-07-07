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

package main

import (
	"crypto/tls"
	"os"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/config"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/controller"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	cfg := config.ParseFlags()

	ctrl.SetLogger(zap.New(zap.UseDevMode(cfg.Development)))

	// ── TLS configuration ────────────────────────────────────────────────
	// HTTP/2 is disabled by default to mitigate the Rapid Reset CVE.
	var tlsOpts []func(*tls.Config)
	if !cfg.EnableHTTP2 {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			c.NextProtos = []string{"http/1.1"}
		})
	}

	// ── Metrics server ───────────────────────────────────────────────────
	metricsServerOptions := metricsserver.Options{
		BindAddress:   cfg.MetricsAddr,
		SecureServing: cfg.SecureMetrics,
		TLSOpts:       tlsOpts,
	}
	if cfg.SecureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}
	if len(cfg.MetricsCertPath) > 0 {
		metricsServerOptions.CertDir = cfg.MetricsCertPath
		metricsServerOptions.CertName = cfg.MetricsCertName
		metricsServerOptions.KeyName = cfg.MetricsCertKey
	}

	// ── Operator namespace ───────────────────────────────────────────────
	podNamespace := os.Getenv("POD_NAMESPACE")
	if podNamespace == "" {
		podNamespace = "default"
	}
	setupLog.Info("operator namespace", "namespace", podNamespace)

	// ── Controller manager ───────────────────────────────────────────────
	// We do not configure a Pod (or Node) cache. The controller reads Pods and
	// Nodes only in the rare unhealthy/recovery path (node-condition patching)
	// and does so via the manager's uncached API reader, so it never maintains
	// cluster-scale Pod/Node informers. Reconciles are driven by GpuSharingConfig
	// and DaemonSet events, not pod events. Only DaemonSets and the CR — small,
	// bounded object sets — are served from the default cache.
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		HealthProbeBindAddress: cfg.ProbeAddr,
		LeaderElection:         cfg.EnableLeaderElect,
		LeaderElectionID:       "gpu-sharing-operator.run.ai",
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	// ── Component images (defaults from Helm, overridable via CRD) ──────
	sharingdImage := controller.ReadImageFromEnv("SHARINGD_IMAGE")
	setupLog.Info("sharingd default image", "image", sharingdImage.FullImage())

	mpsdImage := controller.ReadImageFromEnv("MPSD_IMAGE")
	setupLog.Info("mpsd default image", "image", mpsdImage.FullImage())

	// ── Register controllers ─────────────────────────────────────────────
	if err := controller.NewGpuSharingConfigReconciler(
		mgr.GetClient(),
		mgr.GetAPIReader(),
		mgr.GetScheme(),
		mgr.GetEventRecorderFor("gpusharingconfig-controller"),
		podNamespace,
		map[string]v1alpha1.ImageSpec{
			"sharingd": sharingdImage,
			"mpsd":     mpsdImage,
		},
	).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "gpusharingconfig")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	// ── Health probes ────────────────────────────────────────────────────
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	// ── Start ────────────────────────────────────────────────────────────
	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
