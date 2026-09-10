#!/usr/bin/env bash
# Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
#
# Checks that every shipped image is capable of a FIPS build: its Dockerfile has
# to declare ARG GOFIPS140 and thread it into the compiler.
#
# verify-fips-images.sh proves the same thing far more directly, but only from a
# published image — by which point a component built without FIPS has already
# been built. This reads the Dockerfiles instead, so it needs no registry and
# runs on pull requests, catching the omission at review time.
#
#   hack/verify-fips-build-args.sh

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

status=0
checked=0

while read -r svc dockerfile; do
  [[ -n "$svc" ]] || continue
  checked=$((checked + 1))

  if [[ ! -f "$dockerfile" ]]; then
    echo "FAIL  ${svc}: ${dockerfile} does not exist"
    status=1
    continue
  fi

  # The default has to be "off" so that an ordinary build — which passes no
  # build arg at all — stays an ordinary build.
  if grep -qE '^[[:space:]]*ARG[[:space:]]+GOFIPS140=off[[:space:]]*$' "$dockerfile"; then
    echo "ok    ${svc}: declares ARG GOFIPS140=off"
  else
    echo "FAIL  ${svc}: ${dockerfile} does not declare 'ARG GOFIPS140=off'"
    status=1
  fi

  # Declaring the arg is not enough — it has to reach the go build, or the image
  # accepts the arg and quietly ignores it.
  if grep -qE 'GOFIPS140=\$\{?GOFIPS140\}?' "$dockerfile"; then
    echo "ok    ${svc}: passes GOFIPS140 to go build"
  else
    echo "FAIL  ${svc}: ${dockerfile} never passes \$GOFIPS140 to go build"
    status=1
  fi
done < <(make -s print-image-dockerfiles)

if ((checked == 0)); then
  echo "FAIL  'make print-image-dockerfiles' reported no images"
  status=1
fi

exit $status
