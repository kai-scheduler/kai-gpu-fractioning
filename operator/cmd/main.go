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
	"flag"
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

	gpusharingv1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/controller"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(gpusharingv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// config holds all CLI-configurable settings for the operator.
// We use CLI flags rather than a ConfigMap because these values are
// deployment-time constants (addresses, TLS paths, leader election) that
// are set once at pod creation and never change at runtime.
type config struct {
	MetricsAddr       string // address the metrics endpoint binds to ("0" disables)
	ProbeAddr         string // address the health/readiness probe binds to
	EnableLeaderElect bool   // enable leader election for HA deployments
	SecureMetrics     bool   // serve metrics over HTTPS
	EnableHTTP2       bool   // allow HTTP/2 (disabled by default for Rapid Reset CVE)
	MetricsCertPath   string // directory containing the metrics TLS certificate
	MetricsCertName   string // filename of the metrics TLS certificate
	MetricsCertKey    string // filename of the metrics TLS private key
	Development       bool   // enable development-mode logging (debug, human-readable)
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.MetricsAddr, "metrics-bind-address", "0",
		"The address the metrics endpoint binds to. Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable.")
	flag.StringVar(&cfg.ProbeAddr, "health-probe-bind-address", ":8081",
		"The address the probe endpoint binds to.")
	flag.BoolVar(&cfg.EnableLeaderElect, "leader-elect", false,
		"Enable leader election for controller manager.")
	flag.BoolVar(&cfg.SecureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS.")
	flag.StringVar(&cfg.MetricsCertPath, "metrics-cert-path", "", "The directory that contains the metrics server certificate.")
	flag.StringVar(&cfg.MetricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&cfg.MetricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&cfg.EnableHTTP2, "enable-http2", false, "If set, HTTP/2 will be enabled for the metrics server.")
	flag.BoolVar(&cfg.Development, "development", false, "Enable development-mode logging (debug level, human-readable).")
	flag.Parse()
	return cfg
}

func main() {
	cfg := parseFlags()

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

	// ── Controller manager ───────────────────────────────────────────────
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

	// ── Register controllers ─────────────────────────────────────────────
	if err := (&controller.GpuSharingConfigReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
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
