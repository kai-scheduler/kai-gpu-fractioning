#!/usr/bin/env python3
"""create-cluster.py - k3d cluster + fake-gpu-operator for gpu-sharing-operator e2e tests.

Uses the env-driven pydantic-settings config / typer CLI / retry-on-create-failure
pattern from grove's create-e2e-cluster.py, scoped down to what this project
needs: a k3d cluster with a single fake-GPU node pool and fake-gpu-operator
installed. No other cluster-level components are deployed here.

Dependencies: see requirements.txt (typer, pydantic-settings, sh — no docker
SDK since this script never builds or pre-pulls images, no rich since plain
stdout is enough for CI logs).

Environment variables (all optional, E2E_ prefix, see ClusterConfig):
    E2E_CLUSTER_NAME               (default: gpu-sharing-e2e)
    E2E_WORKER_NODES               (default: 2)
    E2E_K3S_IMAGE                  (default: rancher/k3s:v1.31.5-k3s1)
    E2E_GPU_NODE_POOL              (default: default)
    E2E_GPUS_PER_NODE              (default: 2)
    E2E_GPU_PRODUCT                (default: NVIDIA A100-SXM4-40GB)
    E2E_GPU_MEMORY_MIB             (default: 40960)
    E2E_MAX_RETRIES                (default: 3)

Usage:
    ./create-cluster.py
    E2E_WORKER_NODES=4 ./create-cluster.py
    ./create-cluster.py --skip-gpu-mock
    ./create-cluster.py --delete

Requires: k3d, kubectl.
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

# nvml-mock: NVIDIA's mock libnvidia-ml.so DaemonSet (replaces fake-gpu-operator).
# Applied as a static manifest — its setup.sh self-labels nodes
# nvidia.com/gpu.present=true (the gpu-sharing-plugin nodeSelector) and installs a
# real libnvidia-ml.so into /var/lib/nvml-mock/driver, so the plugin's NVML calls
# resolve against the mock instead of falling back to deadCollector. No Helm
# release and no device plugin are needed for the metrics e2e suite: attribution
# keys off the fractional annotation + cgroup + NVML UUID, not a scheduled
# nvidia.com/gpu resource.
NVML_MOCK_MANIFEST = "sharing-manager/metricsd/deploy/fake-gpu-cluster/nvml-mock.yaml"

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
    worker_nodes: int = Field(default=2, ge=1, le=50)
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
    try:
        for attempt in range(1, config.max_retries + 1):
            log(f"Cluster creation attempt {attempt}/{config.max_retries}...")

            sh.k3d("cluster", "delete", config.cluster_name, _ok_code=[0, 1])

            try:
                sh.k3d(
                    "cluster", "create", config.cluster_name,
                    "--servers", "1",
                    "--agents", str(config.worker_nodes),
                    "--image", config.k3s_image,
                    "--k3s-node-label", "nvidia.com/gpu.present=true@agent:*",
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


def install_nvml_mock(config: ClusterConfig) -> None:
    log("Installing nvml-mock (real mock libnvidia-ml.so + node labels)...")

    # test/e2e/hack -> repo root (hack, e2e, test, <root>).
    manifest = Path(__file__).resolve().parents[3] / NVML_MOCK_MANIFEST

    # The manifest is namespaced to gpu-operator; create it up-front so apply
    # doesn't race the namespace. _ok_code tolerates AlreadyExists.
    sh.kubectl("create", "namespace", "gpu-operator", _ok_code=[0, 1])
    sh.kubectl("apply", "-f", str(manifest))

    # Guard against a silent no-op: a DaemonSet whose nodeSelector matches zero
    # nodes reports "rollout status" success with desiredNumberScheduled=0, so
    # without this check a selector mismatch looks like success and only surfaces
    # later as a confusing GPU-node-label timeout.
    desired = int(str(sh.kubectl(
        "get", "daemonset/nvml-mock", "-n", "gpu-operator",
        "-o", "jsonpath={.status.desiredNumberScheduled}",
    )).strip() or "0")
    if desired < config.worker_nodes:
        log(f"nvml-mock scheduled onto {desired} node(s), expected {config.worker_nodes} "
            f"— check the DaemonSet nodeSelector matches the GPU nodes' labels.")
        raise typer.Exit(1)

    log("Waiting for nvml-mock daemonset rollout...")
    sh.kubectl("rollout", "status", "daemonset/nvml-mock", "-n", "gpu-operator", "--timeout=180s")

    # Agents are pre-labeled nvidia.com/gpu.present=true at cluster creation and
    # nvml-mock's setup.sh re-applies it; confirm the label is present before the
    # suite (and the plugin's nodeSelector) relies on it.
    wait_for_gpu_node_labels(config)


def wait_for_gpu_node_labels(config: ClusterConfig, max_retries: int = 30, interval_seconds: int = 2) -> None:
    log("Waiting for status-updater to label GPU nodes (nvidia.com/gpu.present=true)...")
    for attempt in range(1, max_retries + 1):
        out = str(sh.kubectl(
            "get", "nodes", "-l", "nvidia.com/gpu.present=true",
            "--no-headers", _ok_code=[0, 1],
        )).strip()
        count = len(out.splitlines()) if out else 0
        if count >= config.worker_nodes:
            log(f"{count} node(s) labeled.")
            return
        if attempt == max_retries:
            log(f"timed out waiting for GPU node labels (found {count}/{config.worker_nodes})")
            raise typer.Exit(1)
        time.sleep(interval_seconds)


@app.command()
def main(
    delete: bool = typer.Option(False, "--delete", help="Delete the cluster and exit"),
    skip_gpu_mock: bool = typer.Option(
        False, "--skip-gpu-mock", help="Create the cluster only, skip nvml-mock install"
    ),
) -> None:
    """Create (or delete) a k3d cluster with nvml-mock for gpu-sharing-operator e2e tests."""
    config = ClusterConfig()

    require_command("k3d")
    require_command("kubectl")

    if delete:
        delete_cluster(config)
        return

    if not create_cluster(config):
        raise typer.Exit(1)

    wait_for_nodes(config)

    if skip_gpu_mock:
        log("Skipping nvml-mock installation (--skip-gpu-mock).")
        log(f"Cluster '{config.cluster_name}' is ready.")
        return

    install_nvml_mock(config)

    log(f"Cluster '{config.cluster_name}' is ready for e2e tests.")
    log("Next steps:")
    log(f"  make e2e-load-plugin-image E2E_CLUSTER_NAME={config.cluster_name}")
    log(f"  make test-e2e E2E_EXPECTED_GPU_NODES={config.worker_nodes}")


if __name__ == "__main__":
    app()
