#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

kubectl delete --ignore-not-found -f "$SCRIPT_DIR/test-fractional-gpu-pods.yaml"
kubectl delete --ignore-not-found -f "$SCRIPT_DIR/podmonitor.yaml"
kubectl delete --ignore-not-found -f "$SCRIPT_DIR/daemonset.yaml"
