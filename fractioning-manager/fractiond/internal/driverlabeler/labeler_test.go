// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package driverlabeler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kai-scheduler/kai-gpu-fractioning/pkg/driverinfo"
)

func TestPatchNodeDriverMajor(t *testing.T) {
	tokenDir := t.TempDir()
	tokenPath := filepath.Join(tokenDir, "token")
	if err := os.WriteFile(tokenPath, []byte("test-token"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	var gotMethod, gotPath, gotAuthorization, gotContentType string
	var gotBody map[string]any

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})}

	err := PatchNodeDriverMajor(context.Background(), Config{
		NodeName:     "node-a",
		APIServerURL: "https://kubernetes.default",
		TokenPath:    tokenPath,
		HTTPClient:   client,
	}, 615)
	if err != nil {
		t.Fatalf("PatchNodeDriverMajor() error = %v", err)
	}

	if gotMethod != http.MethodPatch {
		t.Fatalf("method = %q, expected %q", gotMethod, http.MethodPatch)
	}
	if gotPath != "/api/v1/nodes/node-a" {
		t.Fatalf("path = %q, expected /api/v1/nodes/node-a", gotPath)
	}
	if gotAuthorization != "Bearer test-token" {
		t.Fatalf("authorization = %q, expected bearer token", gotAuthorization)
	}
	if gotContentType != "application/merge-patch+json" {
		t.Fatalf("content-type = %q, expected merge patch", gotContentType)
	}

	metadata := gotBody["metadata"].(map[string]any)
	labels := metadata["labels"].(map[string]any)
	if got := labels[driverinfo.NVIDIADriverMajorLabel]; got != "615" {
		t.Fatalf("driver label = %v, expected 615", got)
	}
}

func TestPatchNodeDriverMajorEscapesNodeName(t *testing.T) {
	tokenDir := t.TempDir()
	tokenPath := filepath.Join(tokenDir, "token")
	if err := os.WriteFile(tokenPath, []byte("test-token"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	var gotPath string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.EscapedPath()
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})}

	err := PatchNodeDriverMajor(context.Background(), Config{
		NodeName:     "node/name",
		APIServerURL: "https://kubernetes.default",
		TokenPath:    tokenPath,
		HTTPClient:   client,
	}, 615)
	if err != nil {
		t.Fatalf("PatchNodeDriverMajor() error = %v", err)
	}

	if gotPath != "/api/v1/nodes/node%2Fname" {
		t.Fatalf("escaped path = %q, expected node name to be path-escaped", gotPath)
	}
}

func TestPatchNodeDriverMajorRequiresNodeName(t *testing.T) {
	err := PatchNodeDriverMajor(context.Background(), Config{APIServerURL: "https://kubernetes.default"}, 615)
	if err == nil {
		t.Fatal("PatchNodeDriverMajor() error = nil, expected error")
	}
}

func TestNodeLabelPatchRejectsInvalidMajor(t *testing.T) {
	if _, err := nodeLabelPatch(0); err == nil {
		t.Fatal("nodeLabelPatch(0) error = nil, expected error")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
