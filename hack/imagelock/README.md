<!-- Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# ImageLock generator

This tool generates the architecture-specific, digest-pinned ImageLock files
published with KAI GPU Fractioning releases. The locks let air-gapped users
mirror the exact container manifests required by a release instead of relying
on mutable tags.

It implements [GitHub issue #81](https://github.com/kai-scheduler/kai-gpu-fractioning/issues/81)
and lives in its own Go module so release-tool dependencies do not enter the
shipped binaries' dependency or license graph.

## Release image inventory

Every generated lock contains these four release images:

- `operator`
- `fractiond`
- `metricsd`
- `mpsd`

The inventory is explicit because some containers are created by the operator
and do not appear as literal `image:` fields in the rendered Helm resources.
When a production image is added or removed, update `knownImages` in `main.go`.

The tool also renders the chart and validates every literal `image:` reference.
This makes a new sidecar, init container, or hook fail verification until its
repository is deliberately included in the release inventory.

## How it works

1. Validate the exact `v`-prefixed release version.
2. Render the Helm chart with the release tag and supported optional features.
3. Validate literal chart images against the release inventory.
4. Resolve every release tag through the default Docker credential keychain.
5. Require a multi-platform index containing exactly one `linux/amd64` and one
   `linux/arm64` manifest.
6. Record the shared index digest and each architecture's child digest.
7. Stage and publish one deterministic ImageLock YAML file per architecture.

The project does not ship distinct FIPS images, so the tool generates only the
`standard` profile.

## Usage

```bash
# from the repo root
GOWORK=off go -C hack/imagelock run . --verify-only
GOWORK=off go -C hack/imagelock test ./...
GOWORK=off go -C hack/imagelock run . --version vX.Y.Z
```

Generation requires registry access to all four release tags. Authenticate with
Docker, for example with `docker login ghcr.io`, before running it locally.

By default, generation writes:

```text
dist/
  imagelock-kai-gpu-fractioning-vX.Y.Z-linux-amd64.yaml
  imagelock-kai-gpu-fractioning-vX.Y.Z-linux-arm64.yaml
```

Use `--out-dir`, repeatable `--platform`, `--chart`, or `--helm` to override the
defaults. `--verify-only` performs the chart checks without registry access or
file writes.

## ImageLock contents

Each entry records:

- `source`: the exact tagged release reference;
- `indexDigest`: the digest of its shared multi-platform index;
- `image`: the digest-pinned manifest for the lock's architecture.

Choose the lock matching the target cluster's node architecture. The amd64 and
arm64 locks have the same image names, source tags, and index digests, but
different platform manifest digests.

## Air-gap install

Download the Helm chart and the matching architecture's lock from the GitHub
Release. A registry-copy tool such as `skopeo`, `crane`, or `regctl` can then
mirror every digest-pinned `image` reference into the private registry.

```bash
LOCK=imagelock-kai-gpu-fractioning-vX.Y.Z-linux-amd64.yaml
yq -r '.spec.images[] | .image + " " + .source' "$LOCK" | while read -r digest_ref tag_ref; do
  echo "$digest_ref -> $tag_ref"
done
```

Use the first value as the immutable source to copy. Preserve or map the tagged
`source` name according to the private registry layout used by the Helm values.
