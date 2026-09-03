// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const renderedManifest = `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gpu-fractioning
spec:
  template:
    spec:
      initContainers:
        - name: duplicate-operator-reference
          image: ghcr.io/kai-scheduler/kai-gpu-fractioning/operator:v1.2.3
      containers:
        - name: manager
          image: ghcr.io/kai-scheduler/kai-gpu-fractioning/operator:v1.2.3
`

func TestImageRefs(t *testing.T) {
	got, err := imageRefs([]byte(renderedManifest))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		imageRepositoryPrefix + "operator:v1.2.3",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("imageRefs = %v, want %v", got, want)
	}
}

func TestImageRefsPropagatesParseError(t *testing.T) {
	if _, err := imageRefs([]byte("foo: [unterminated")); err == nil {
		t.Fatal("want parse error for malformed document, got nil")
	}
}

func TestRepoOf(t *testing.T) {
	cases := map[string]string{
		imageRepositoryPrefix + "operator:v1.2.3":                            imageRepositoryPrefix + "operator",
		"localhost:5000/foo/bar:tag":                                         "localhost:5000/foo/bar",
		imageRepositoryPrefix + "operator@sha256:" + strings.Repeat("a", 64): imageRepositoryPrefix + "operator",
	}
	for input, want := range cases {
		if got := repoOf(input); got != want {
			t.Errorf("repoOf(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestValidateManifestImagesRejectsUnknown(t *testing.T) {
	manifest := renderedManifest + `
---
kind: Pod
spec:
  containers:
    - image: docker.io/library/nginx:v1.2.3
`
	err := validateManifestImages([]byte(manifest), "v1.2.3")
	if err == nil || !strings.Contains(err.Error(), "unknown image") {
		t.Fatalf("want unknown image error, got %v", err)
	}
}

func TestValidateManifestImagesRejectsConflictingRefs(t *testing.T) {
	manifest := renderedManifest + `
---
kind: Pod
spec:
  containers:
    - image: ghcr.io/kai-scheduler/kai-gpu-fractioning/operator:v9.9.9
`
	err := validateManifestImages([]byte(manifest), "v1.2.3")
	if err == nil || !strings.Contains(err.Error(), "two references") {
		t.Fatalf("want conflicting-refs error, got %v", err)
	}
}

func TestValidateManifestImagesRejectsWrongReleaseTag(t *testing.T) {
	err := validateManifestImages([]byte(renderedManifest), "v9.9.9")
	if err == nil || !strings.Contains(err.Error(), `expected release tag "v9.9.9"`) {
		t.Fatalf("want wrong-release-tag error, got %v", err)
	}
}

func TestReleaseImages(t *testing.T) {
	got := releaseImages("v1.2.3")
	wantNames := []string{"fractiond", "metricsd", "mpsd", "operator"}
	if len(got) != len(wantNames) {
		t.Fatalf("releaseImages returned %d images, want %d: %+v", len(got), len(wantNames), got)
	}
	for index, wantName := range wantNames {
		if got[index].name != wantName || got[index].tag != "v1.2.3" {
			t.Errorf("image[%d] = %+v, want name %q with release tag", index, got[index], wantName)
		}
	}
}

func TestParsePlatformsRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"", ",", "linux/", "/amd64", "linux/amd64/extra", "linux"} {
		if _, err := parsePlatforms([]string{bad}); err == nil {
			t.Errorf("parsePlatforms(%q): want error, got nil", bad)
		}
	}
	if _, err := parsePlatforms([]string{"linux/amd64", "linux/arm64"}); err != nil {
		t.Errorf("valid platforms rejected: %v", err)
	}
}

func TestParseFlagsVersion(t *testing.T) {
	for _, good := range []string{"v1.2.3", "v0.2.2", "v0.2.2-rc.1"} {
		opts, err := parseFlags([]string{"--version", good})
		if err != nil {
			t.Errorf("--version %q rejected: %v", good, err)
		} else if opts.version != good {
			t.Errorf("--version %q normalized to %q, want exact preservation", good, opts.version)
		}
	}
	for _, bad := range []string{"", "1.2.3", "latest", "vlatest", "v01.2.3", "v1.2.3+build.1", "v1.2.3!"} {
		if _, err := parseFlags([]string{"--version", bad}); err == nil {
			t.Errorf("--version %q: want error, got nil", bad)
		}
	}

	opts, err := parseFlags([]string{"--verify-only"})
	if err != nil {
		t.Fatalf("--verify-only rejected: %v", err)
	}
	if opts.version != verifyVersion {
		t.Errorf("verify version = %q, want %q", opts.version, verifyVersion)
	}
}

func TestParseFlagsRejectsBadStabilityReads(t *testing.T) {
	if _, err := parseFlags([]string{"--version", "v1.2.3", "--stability-reads", "0"}); err == nil {
		t.Fatal("want error for --stability-reads 0, got nil")
	}
	if _, err := parseFlags([]string{"--version", "v1.2.3", "--stability-reads", "5"}); err != nil {
		t.Fatalf("valid stability-reads rejected: %v", err)
	}
}

func TestResolveMultiArch(t *testing.T) {
	registryServer := httptest.NewServer(registry.New())
	defer registryServer.Close()
	ref := mustHost(t, registryServer.URL) + "/gpu-fractioning/operator:v1.2.3"

	amd64 := mustRandomImage(t)
	arm64 := mustRandomImage(t)
	attestation := mustRandomImage(t)
	index := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: amd64, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}}},
		mutate.IndexAddendum{Add: arm64, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}}},
		mutate.IndexAddendum{Add: attestation, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "unknown", Architecture: "unknown"}}},
	)
	writeIndex(t, ref, index)

	platforms := []platform{{OS: "linux", Architecture: "amd64"}, {OS: "linux", Architecture: "arm64"}}
	indexDigest, perPlatform, err := (remoteResolver{stabilityReads: 1}).resolve(ref, platforms)
	if err != nil {
		t.Fatal(err)
	}

	wantIndex, _ := index.Digest()
	if indexDigest != wantIndex.String() {
		t.Errorf("index digest = %s, want %s", indexDigest, wantIndex)
	}
	amd64Digest, _ := amd64.Digest()
	arm64Digest, _ := arm64.Digest()
	if perPlatform[platforms[0]] != amd64Digest.String() {
		t.Errorf("amd64 digest = %s, want %s", perPlatform[platforms[0]], amd64Digest)
	}
	if perPlatform[platforms[1]] != arm64Digest.String() {
		t.Errorf("arm64 digest = %s, want %s", perPlatform[platforms[1]], arm64Digest)
	}
}

func TestResolveRejectsSingleArch(t *testing.T) {
	registryServer := httptest.NewServer(registry.New())
	defer registryServer.Close()
	ref := mustHost(t, registryServer.URL) + "/gpu-fractioning/operator:v1.2.3"
	tag, err := name.NewTag(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, mustRandomImage(t)); err != nil {
		t.Fatal(err)
	}

	_, _, err = (remoteResolver{stabilityReads: 1}).resolve(ref, []platform{{OS: "linux", Architecture: "amd64"}})
	if err == nil || !strings.Contains(err.Error(), "not a multi-platform image index") {
		t.Fatalf("want single-architecture rejection, got %v", err)
	}
}

func TestResolveRejectsMissingOrAmbiguousPlatform(t *testing.T) {
	tests := []struct {
		name       string
		platforms  []platform
		wantErrMsg string
	}{
		{
			name:       "missing arm64",
			platforms:  []platform{{OS: "linux", Architecture: "amd64"}},
			wantErrMsg: "no linux/arm64 manifest",
		},
		{
			name: "ambiguous amd64",
			platforms: []platform{
				{OS: "linux", Architecture: "amd64"},
				{OS: "linux", Architecture: "amd64"},
				{OS: "linux", Architecture: "arm64"},
			},
			wantErrMsg: "ambiguous linux/amd64",
		},
	}

	requested := []platform{{OS: "linux", Architecture: "amd64"}, {OS: "linux", Architecture: "arm64"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registryServer := httptest.NewServer(registry.New())
			defer registryServer.Close()
			ref := mustHost(t, registryServer.URL) + "/gpu-fractioning/operator:v1.2.3"

			var manifests []mutate.IndexAddendum
			for _, imagePlatform := range test.platforms {
				manifests = append(manifests, mutate.IndexAddendum{
					Add: mustRandomImage(t),
					Descriptor: v1.Descriptor{Platform: &v1.Platform{
						OS: imagePlatform.OS, Architecture: imagePlatform.Architecture,
					}},
				})
			}
			writeIndex(t, ref, mutate.AppendManifests(empty.Index, manifests...))

			_, _, err := (remoteResolver{stabilityReads: 1}).resolve(ref, requested)
			if err == nil || !strings.Contains(err.Error(), test.wantErrMsg) {
				t.Fatalf("want error containing %q, got %v", test.wantErrMsg, err)
			}
		})
	}
}

func TestBuildLockPreservesReleaseTag(t *testing.T) {
	amd64 := platform{OS: "linux", Architecture: "amd64"}
	indexDigest := "sha256:" + strings.Repeat("a", 64)
	childDigest := "sha256:" + strings.Repeat("b", 64)
	resolved := []resolvedImage{{
		chartImage:        chartImage{name: "operator", repo: imageRepositoryPrefix + "operator", tag: "v1.2.3"},
		indexDigest:       indexDigest,
		digestPerPlatform: map[platform]string{amd64: childDigest},
	}}

	lock := buildLock("v1.2.3", amd64, resolved)
	if lock.Metadata.Name != lockName || lock.Metadata.Version != "v1.2.3" {
		t.Fatalf("metadata = %+v", lock.Metadata)
	}
	if len(lock.Spec.Images) != 1 {
		t.Fatalf("images = %+v", lock.Spec.Images)
	}
	image := lock.Spec.Images[0]
	if image.Source != imageRepositoryPrefix+"operator:v1.2.3" || image.IndexDigest != indexDigest ||
		image.Image != imageRepositoryPrefix+"operator@"+childDigest {
		t.Fatalf("locked image = %+v", image)
	}
}

func TestWriteLocks(t *testing.T) {
	amd64 := platform{OS: "linux", Architecture: "amd64"}
	arm64 := platform{OS: "linux", Architecture: "arm64"}
	resolved := []resolvedImage{{
		chartImage:  chartImage{name: "operator", repo: imageRepositoryPrefix + "operator", tag: "v1.2.3"},
		indexDigest: "sha256:" + strings.Repeat("a", 64),
		digestPerPlatform: map[platform]string{
			amd64: "sha256:" + strings.Repeat("b", 64),
			arm64: "sha256:" + strings.Repeat("c", 64),
		},
	}}

	var firstOutput map[string][]byte
	for run := 0; run < 2; run++ {
		outDir := t.TempDir()
		opts := options{version: "v1.2.3", platforms: []platform{amd64, arm64}, outDir: outDir}
		if err := writeLocks(opts, resolved); err != nil {
			t.Fatal(err)
		}

		currentOutput := map[string][]byte{}
		for _, imagePlatform := range opts.platforms {
			lock := buildLock(opts.version, imagePlatform, resolved)
			path := filepath.Join(outDir, lockFileName(lock))
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o644 {
				t.Errorf("mode for %s = %o, want 644", path, info.Mode().Perm())
			}
			currentOutput[filepath.Base(path)] = contents
		}
		if matches, err := filepath.Glob(filepath.Join(outDir, ".imagelock-*.tmp")); err != nil || len(matches) != 0 {
			t.Errorf("staged files after successful write = %v, err = %v", matches, err)
		}
		for name, want := range firstOutput {
			if !bytes.Equal(currentOutput[name], want) {
				t.Errorf("%s differs between identical generations", name)
			}
		}
		firstOutput = currentOutput
	}
}

func TestWriteLocksCleansPartialPublication(t *testing.T) {
	amd64 := platform{OS: "linux", Architecture: "amd64"}
	arm64 := platform{OS: "linux", Architecture: "arm64"}
	outDir := t.TempDir()
	opts := options{version: "v1.2.3", platforms: []platform{amd64, arm64}, outDir: outDir}
	resolved := []resolvedImage{{
		chartImage:  chartImage{name: "operator", repo: imageRepositoryPrefix + "operator", tag: "v1.2.3"},
		indexDigest: "sha256:" + strings.Repeat("a", 64),
		digestPerPlatform: map[platform]string{
			amd64: "sha256:" + strings.Repeat("b", 64),
			arm64: "sha256:" + strings.Repeat("c", 64),
		},
	}}

	amd64Path := filepath.Join(outDir, lockFileName(buildLock(opts.version, amd64, resolved)))
	arm64Path := filepath.Join(outDir, lockFileName(buildLock(opts.version, arm64, resolved)))
	if err := os.Mkdir(arm64Path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeLocks(opts, resolved); err == nil {
		t.Fatal("want publication failure, got nil")
	}
	if _, err := os.Stat(amd64Path); !os.IsNotExist(err) {
		t.Fatalf("partial amd64 lock remains after failure: %v", err)
	}
	if matches, err := filepath.Glob(filepath.Join(outDir, ".imagelock-*.tmp")); err != nil || len(matches) != 0 {
		t.Errorf("staged files after failed write = %v, err = %v", matches, err)
	}
}

func mustRandomImage(t *testing.T) v1.Image {
	t.Helper()
	image, err := random.Image(256, 1)
	if err != nil {
		t.Fatal(err)
	}
	return image
}

func writeIndex(t *testing.T, ref string, index v1.ImageIndex) {
	t.Helper()
	tag, err := name.NewTag(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.WriteIndex(tag, index); err != nil {
		t.Fatal(err)
	}
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Host
}
