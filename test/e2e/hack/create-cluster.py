#!/usr/bin/env python3
"""create-cluster.py - k3d cluster + fake-gpu-operator for gpu-sharing-operator e2e tests.

Uses an env-driven pydantic-settings config, a typer CLI, and
retry-on-create-failure, scoped to what this project needs: a k3d cluster with a
single fake-GPU node pool and fake-gpu-operator installed. No other
cluster-level components are deployed here.

Dependencies: see requirements.txt (typer, pydantic-settings, sh — no docker
SDK since this script never builds or pre-pulls images, no rich since plain
stdout is enough for CI logs).

Environment variables (all optional, E2E_ prefix, see ClusterConfig):
    E2E_CLUSTER_NAME               (default: gpu-sharing-e2e)
    E2E_GPU_WORKER_NODES               (default: 2)   # GPU worker nodes
    E2E_NON_GPU_WORKER_NODES       (default: 1)   # plain (no-GPU) worker nodes
    E2E_K3S_IMAGE                  (default: rancher/k3s:v1.31.5-k3s1)
    E2E_GPU_NODE_POOL              (default: default)
    E2E_GPUS_PER_NODE              (default: 2)
    E2E_GPU_PRODUCT                (default: NVIDIA A100-SXM4-40GB)
    E2E_GPU_MEMORY_MIB             (default: 40960)
    E2E_FAKE_GPU_OPERATOR_VERSION  (required unless --skip-fake-gpu-operator)
    E2E_MAX_RETRIES                (default: 3)

Usage:
    ./create-cluster.py
    E2E_GPU_WORKER_NODES=4 E2E_FAKE_GPU_OPERATOR_VERSION=0.0.80 ./create-cluster.py
    ./create-cluster.py --skip-fake-gpu-operator
    ./create-cluster.py --delete

Requires: k3d, kubectl, helm (unless --skip-fake-gpu-operator).
"""

import shutil
import tempfile
import time
from pathlib import Path

import sh
import typer
from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict

app = typer.Typer(help="k3d cluster setup for gpu-sharing-operator e2e tests")

FAKE_GPU_OPERATOR_CHART = "oci://ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator"

# gpu-sharing-plugin (sharing-manager/metricsd/deploy/daemonset.yaml) mounts
# /var/run/nri as a hostPath of type "Directory", which requires the path to
# already exist on the node. Stock k3s images ship with containerd's NRI
# plugin disabled, so that directory/socket is never created and the
# plugin's pods hang forever in ContainerCreating. Override each node's
# containerd config to enable NRI so containerd creates the socket (and its
# parent directory) on startup.
CONTAINERD_NRI_CONFIG_TEMPLATE = """{{ template "base" . }}

[plugins."io.containerd.nri.v1.nri"]
  disable = false
  socket_path = "/var/run/nri/nri.sock"
"""


def write_containerd_nri_template() -> str:
    with tempfile.NamedTemporaryFile("w", suffix=".toml.tmpl", delete=False) as f:
        f.write(CONTAINERD_NRI_CONFIG_TEMPLATE)
        return f.name


class ClusterConfig(BaseSettings):
    """Configuration auto-loaded from E2E_* environment variables."""

    model_config = SettingsConfigDict(env_prefix="E2E_", extra="ignore")

    cluster_name: str = "gpu-sharing-e2e"
    # GPU worker nodes (carry the fake-gpu-operator node-pool label; get
    # nvidia.com/gpu.present=true). Non-GPU workers below are plain agents with
    # no GPU label, so the cluster mirrors a real mixed GPU/CPU topology.
    gpu_worker_nodes: int = Field(default=2, ge=1, le=50)
    non_gpu_worker_nodes: int = Field(default=1, ge=0, le=50)
    k3s_image: str = "rancher/k3s:v1.31.5-k3s1"
    gpu_node_pool: str = "default"
    gpus_per_node: int = Field(default=2, ge=1, le=16)
    gpu_product: str = "NVIDIA A100-SXM4-40GB"
    gpu_memory_mib: int = Field(default=40960, ge=1)
    fake_gpu_operator_version: str = ""
    cluster_timeout: str = "120s"
    max_retries: int = Field(default=3, ge=1, le=10)


def log(msg: str) -> None:
    print(f"[create-cluster] {msg}", flush=True)


def require_command(cmd: str) -> None:
    if shutil.which(cmd) is None:
        log(f"required command '{cmd}' not found. Please install it first.")
        raise typer.Exit(1)


def delete_cluster(config: ClusterConfig) -> None:
    log(f"Deleting k3d cluster '{config.cluster_name}'...")
    sh.k3d("cluster", "delete", config.cluster_name, _ok_code=[0, 1])


def create_cluster(config: ClusterConfig) -> bool:
    log("Cluster configuration:")
    for key, value in config.model_dump().items():
        log(f"  {key:26s}: {value}")

    nri_template_path = write_containerd_nri_template()

    # k3d indexes agents 0..(total-1). GPU agents come first and carry the
    # node-pool label; the remaining agents stay unlabeled (non-GPU workers).
    total_agents = config.gpu_worker_nodes + config.non_gpu_worker_nodes
    gpu_node_filter = ";".join(f"agent:{i}" for i in range(config.gpu_worker_nodes))
    gpu_node_label = f"run.ai/simulated-gpu-node-pool={config.gpu_node_pool}@{gpu_node_filter}"

    try:
        for attempt in range(1, config.max_retries + 1):
            log(f"Cluster creation attempt {attempt}/{config.max_retries}...")

            sh.k3d("cluster", "delete", config.cluster_name, _ok_code=[0, 1])

            try:
                sh.k3d(
                    "cluster", "create", config.cluster_name,
                    "--servers", "1",
                    "--agents", str(total_agents),
                    "--image", config.k3s_image,
                    "--k3s-node-label", gpu_node_label,
                    "--volume", f"{nri_template_path}:/var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl@server:0;agent:*",
                    "--timeout", config.cluster_timeout,
                    "--wait",
                )
                log(f"Cluster created successfully on attempt {attempt}.")
                return True
            except sh.ErrorReturnCode as e:
                log(f"cluster creation failed: {e}")
                if attempt < config.max_retries:
                    log("Retrying in 10s...")
                    time.sleep(10)

        log(f"Cluster creation failed after {config.max_retries} attempts.")
        return False
    finally:
        Path(nri_template_path).unlink(missing_ok=True)


def wait_for_nodes(config: ClusterConfig) -> None:
    log("Waiting for all nodes to be ready...")
    sh.kubectl("wait", "--for=condition=Ready", "nodes", "--all", "--timeout=120s")
    log("All nodes are ready.")


def install_fake_gpu_operator(config: ClusterConfig) -> None:
    log(f"Installing fake-gpu-operator {config.fake_gpu_operator_version}...")

    # Scoped to device-plugin + status-updater + status-exporter — no
    # DRA/KWOK/mock-NVML/gpu-operator, which this suite does not need for
    # metrics e2e.
    values = f"""
devicePlugin:
  enabled: true
statusUpdater:
  enabled: true
statusExporter:
  enabled: true
topologyServer:
  enabled: false
draPlugin:
  enabled: false
kwokDraPlugin:
  enabled: false
kwokGpuDevicePlugin:
  enabled: false
migFaker:
  enabled: false
gpuOperator:
  enabled: false
# Some k3s node images auto-provision a cluster-scoped RuntimeClass named
# "nvidia" at startup (unrelated to this chart). Helm then refuses to adopt
# it during install ("invalid ownership metadata") since it has no Helm
# annotations. We don't need GPU RuntimeClass handling for the plain
# devicePlugin path, so disable it — matches fake-gpu-operator's own e2e
# fixtures (test/e2e/fixtures/values.yaml upstream).
runtimeClass:
  enabled: false
computeDomainController:
  enabled: false
computeDomainDraPlugin:
  enabled: false
kwokComputeDomainDraPlugin:
  enabled: false
topology:
  nodePoolLabelKey: run.ai/simulated-gpu-node-pool
  nodePools:
    {config.gpu_node_pool}:
      gpuProduct: "{config.gpu_product}"
      gpuCount: {config.gpus_per_node}
      gpuMemory: {config.gpu_memory_mib}
"""

    with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False) as f:
        f.write(values)
        values_path = f.name

    try:
        sh.helm(
            "upgrade", "-i", "fake-gpu-operator",
            FAKE_GPU_OPERATOR_CHART,
            "--version", config.fake_gpu_operator_version,
            "--namespace", "gpu-operator",
            "--create-namespace",
            "-f", values_path,
        )
    finally:
        Path(values_path).unlink(missing_ok=True)

    log("Waiting for status-updater to be ready...")
    sh.kubectl("wait", "--for=condition=Ready", "pod", "-l", "app=status-updater", "-n", "gpu-operator", "--timeout=120s")

    log("Waiting for device-plugin daemonset rollout...")
    sh.kubectl("rollout", "status", "daemonset/device-plugin", "-n", "gpu-operator", "--timeout=180s")

    wait_for_gpu_node_labels(config)


def wait_for_gpu_node_labels(config: ClusterConfig, max_retries: int = 30, interval_seconds: int = 2) -> None:
    log("Waiting for status-updater to label GPU nodes (nvidia.com/gpu.present=true)...")
    for attempt in range(1, max_retries + 1):
        out = str(sh.kubectl(
            "get", "nodes", "-l", "nvidia.com/gpu.present=true",
            "--no-headers", _ok_code=[0, 1],
        )).strip()
        count = len(out.splitlines()) if out else 0
        if count >= config.gpu_worker_nodes:
            log(f"{count} node(s) labeled.")
            return
        if attempt == max_retries:
            log(f"timed out waiting for GPU node labels (found {count}/{config.gpu_worker_nodes})")
            raise typer.Exit(1)
        time.sleep(interval_seconds)


@app.command()
def main(
    delete: bool = typer.Option(False, "--delete", help="Delete the cluster and exit"),
    skip_fake_gpu_operator: bool = typer.Option(
        False, "--skip-fake-gpu-operator", help="Create the cluster only, skip fake-gpu-operator install"
    ),
) -> None:
    """Create (or delete) a k3d cluster with fake-gpu-operator for gpu-sharing-operator e2e tests."""
    config = ClusterConfig()

    require_command("k3d")
    require_command("kubectl")

    if delete:
        delete_cluster(config)
        return

    if not skip_fake_gpu_operator:
        require_command("helm")
        if not config.fake_gpu_operator_version:
            log("E2E_FAKE_GPU_OPERATOR_VERSION must be set (or pass --skip-fake-gpu-operator).")
            log("See https://github.com/run-ai/fake-gpu-operator/releases for available versions.")
            raise typer.Exit(1)

    if not create_cluster(config):
        raise typer.Exit(1)

    wait_for_nodes(config)

    if skip_fake_gpu_operator:
        log("Skipping fake-gpu-operator installation (--skip-fake-gpu-operator).")
        log(f"Cluster '{config.cluster_name}' is ready.")
        return

    install_fake_gpu_operator(config)

    log(f"Cluster '{config.cluster_name}' is ready for e2e tests.")
    log("Next steps:")
    log(f"  make e2e-load-plugin-image E2E_CLUSTER_NAME={config.cluster_name}")
    log(f"  make test-e2e E2E_EXPECTED_GPU_NODES={config.gpu_worker_nodes}")


if __name__ == "__main__":
    app()
