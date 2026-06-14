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

// Package config holds all CLI-configurable settings for the operator.
// We use CLI flags rather than a ConfigMap because these values are
// deployment-time constants (addresses, TLS paths, leader election) that
// are set once at pod creation and never change at runtime.
package config

import "flag"

// Config holds the operator's runtime settings parsed from CLI flags.
type Config struct {
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

// ParseFlags registers CLI flags, parses them, and returns the populated Config.
func ParseFlags() Config {
	var cfg Config
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
