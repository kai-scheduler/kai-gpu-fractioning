// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package driverlabeler

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/kai-scheduler/kai-gpu-fractioning/pkg/driverinfo"
	"k8s.io/apimachinery/pkg/types"
)

const (
	envNodeName      = "NODE_NAME"
	envKubeHost      = "KUBERNETES_SERVICE_HOST"
	envKubePortHTTPS = "KUBERNETES_SERVICE_PORT_HTTPS"
	envKubePort      = "KUBERNETES_SERVICE_PORT"

	defaultServiceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	defaultServiceAccountCAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	defaultRequestTimeout          = 10 * time.Second
	maxErrorResponseBytes          = 4096
)

// config carries the Kubernetes API connection details needed to patch the node.
type config struct {
	NodeName     string
	APIServerURL string
	TokenPath    string
	CAPath       string
	HTTPClient   *http.Client
}

type preflight struct {
	cfg      config
	client   *http.Client
	endpoint string
	token    string
}

// configFromEnv builds config from the downward API and service environment
// variables that Kubernetes injects into pods.
func configFromEnv() config {
	port := os.Getenv(envKubePortHTTPS)
	if port == "" {
		port = os.Getenv(envKubePort)
	}

	host := os.Getenv(envKubeHost)
	apiServerURL := ""
	if host != "" && port != "" {
		apiServerURL = (&url.URL{
			Scheme: "https",
			Host:   net.JoinHostPort(host, port),
		}).String()
	}

	return config{
		NodeName:     os.Getenv(envNodeName),
		APIServerURL: apiServerURL,
		TokenPath:    defaultServiceAccountTokenPath,
		CAPath:       defaultServiceAccountCAPath,
	}
}

// Run reads the local NVIDIA driver version via NVML and labels the current node
// with the parsed driver major branch.
func Run(ctx context.Context, logger *slog.Logger) (int, string, error) {
	return run(ctx, configFromEnv(), logger)
}

func run(ctx context.Context, cfg config, logger *slog.Logger) (int, string, error) {
	pf, err := prepare(cfg)
	if err != nil {
		return 0, "", err
	}

	driverVersion, err := driverVersionFromNVML(logger)
	if err != nil {
		return 0, "", err
	}

	major, err := driverinfo.ParseDriverVersionMajor(driverVersion)
	if err != nil {
		return 0, driverVersion, fmt.Errorf("parse NVIDIA driver version %q: %w", driverVersion, err)
	}

	if err := patchNodeDriverMajor(ctx, pf, major); err != nil {
		return 0, driverVersion, err
	}

	return major, driverVersion, nil
}

func prepare(cfg config) (preflight, error) {
	if cfg.NodeName == "" {
		return preflight{}, fmt.Errorf("%s is required", envNodeName)
	}
	if cfg.APIServerURL == "" {
		return preflight{}, fmt.Errorf("kubernetes API server URL is required")
	}
	if cfg.TokenPath == "" {
		cfg.TokenPath = defaultServiceAccountTokenPath
	}
	if cfg.CAPath == "" {
		cfg.CAPath = defaultServiceAccountCAPath
	}

	endpoint, err := nodePatchURL(cfg.APIServerURL, cfg.NodeName)
	if err != nil {
		return preflight{}, err
	}

	token, err := os.ReadFile(cfg.TokenPath)
	if err != nil {
		return preflight{}, fmt.Errorf("read service account token: %w", err)
	}

	client := cfg.HTTPClient
	if client == nil {
		client, err = inClusterHTTPClient(cfg.CAPath)
		if err != nil {
			return preflight{}, err
		}
	}

	return preflight{cfg: cfg, client: client, endpoint: endpoint, token: strings.TrimSpace(string(token))}, nil
}

func driverVersionFromNVML(logger *slog.Logger) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}

	ret := nvml.Init()
	if ret != nvml.SUCCESS && ret != nvml.ERROR_ALREADY_INITIALIZED {
		return "", fmt.Errorf("initialize NVML: %w", ret)
	}
	defer func() {
		ret := nvml.Shutdown()
		if ret != nvml.SUCCESS && ret != nvml.ERROR_UNINITIALIZED {
			logger.Warn("shutdown NVML", "error", ret.Error())
		}
	}()

	version, ret := nvml.SystemGetDriverVersion()
	if ret != nvml.SUCCESS {
		return "", fmt.Errorf("read NVIDIA driver version from NVML: %w", ret)
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return "", fmt.Errorf("NVML returned an empty NVIDIA driver version")
	}
	return version, nil
}

func patchNodeDriverMajor(ctx context.Context, pf preflight, major int) error {
	body, err := driverMajorLabelPatch(major)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, pf.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build node label patch request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+pf.token)
	// Keep this init helper on the root module's existing Kubernetes dependency
	// surface: use apimachinery's patch type without pulling in client-go.
	req.Header.Set("Content-Type", string(types.MergePatchType))

	resp, err := pf.client.Do(req)
	if err != nil {
		return fmt.Errorf("patch node %s label %q: %w", pf.cfg.NodeName, driverinfo.NVIDIADriverMajorLabel, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorResponseBytes))
		return fmt.Errorf("patch node %s label %q: Kubernetes API returned %s: %s",
			pf.cfg.NodeName, driverinfo.NVIDIADriverMajorLabel, resp.Status, strings.TrimSpace(string(responseBody)))
	}

	return nil
}

func driverMajorLabelPatch(major int) ([]byte, error) {
	if major <= 0 {
		return nil, fmt.Errorf("driver major must be positive")
	}

	patch := map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{
				driverinfo.NVIDIADriverMajorLabel: strconv.Itoa(major),
			},
		},
	}
	return json.Marshal(patch)
}

func nodePatchURL(apiServerURL, nodeName string) (string, error) {
	parsed, err := url.Parse(apiServerURL)
	if err != nil {
		return "", fmt.Errorf("parse Kubernetes API server URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("kubernetes API server URL %q must include scheme and host", apiServerURL)
	}
	parsed.Path = "/api/v1/nodes/" + nodeName
	parsed.RawPath = "/api/v1/nodes/" + url.PathEscape(nodeName)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func inClusterHTTPClient(caPath string) (*http.Client, error) {
	caCert, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read service account CA certificate: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("service account CA certificate %s did not contain any PEM certificates", caPath)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
	}
	return &http.Client{Transport: transport, Timeout: defaultRequestTimeout}, nil
}
