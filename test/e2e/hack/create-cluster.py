#!/usr/bin/env python3
"""create-cluster.py - k3d cluster + fake-gpu-operator for gpu-sharing-operator e2e tests.

Uses an env-driven pydantic-settings config, a typer CLI, and
retry-on-create-failure, scoped to what this project needs: a k3d cluster with a
single fake-GPU node pool and fake-gpu-operator installed. No other
cluster-level components are deployed here.

Dependencies: see requirements.txt (typer, pydantic-settings, sh — no docker
SDK since this script never builds or pre-pulls images, no rich since plain
stdout is enough for CI logs).

Environment variables (E2E_ prefix, see ClusterConfig):
    E2E_FAKE_GPU_OPERATOR_VERSION  (required) e.g. "0.2.0"
    E2E_CLUSTER_NAME               (default: gpu-sharing-e2e)
    E2E_GPU_WORKER_NODES           (default: 2)   # GPU worker nodes
    E2E_NON_GPU_WORKER_NODES       (default: 1)   # plain (no-GPU) worker nodes
    E2E_K3S_IMAGE                  (default: rancher/k3s:v1.31.5-k3s1)
    E2E_GPU_NODE_POOL              (default: default)
    E2E_GPUS_PER_NODE              (default: 2)
    E2E_GPU_PRODUCT                (default: NVIDIA A100-SXM4-40GB)
    E2E_GPU_MEMORY_MIB             (default: 40960)
    E2E_MAX_RETRIES                (default: 3)
    E2E_KUBECONFIG                 (default: ~/.kube/<cluster_name>.yaml)

The cluster's kubeconfig is written to E2E_KUBECONFIG (not ~/.kube/config) via
docker, because the NRI config.toml.tmpl volume this script mounts breaks k3d's
own `k3d kubeconfig` retrieval. Every downstream e2e step reads that file.

Usage:
    E2E_FAKE_GPU_OPERATOR_VERSION=0.2.0 ./create-cluster.py
    E2E_FAKE_GPU_OPERATOR_VERSION=0.2.0 E2E_GPU_WORKER_NODES=4 ./create-cluster.py
    ./create-cluster.py --skip-fake-gpu-operator --skip-gpu-mock
    ./create-cluster.py --delete

Requires: k3d, kubectl, docker, helm (unless --skip-fake-gpu-operator).
"""

import os
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

# nvml-mock: NVIDIA's mock libnvidia-ml.so DaemonSet (supplements fake-gpu-operator).
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
    # Where to write the cluster's kubeconfig (E2E_KUBECONFIG). Empty -> a
    # default derived from cluster_name (see main). This file — not ~/.kube/config
    # — is what every downstream e2e step uses.
    kubeconfig: str = ""


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
    # nvidia.com/gpu.present=true label; the remaining agents stay unlabeled
    # (non-GPU workers). This label is what everything downstream selects on: the
    # nvml-mock DaemonSet's nodeSelector (which must match before it can schedule
    # and patch the node), the operator's default-CR nodeSelector, and the
    # sharingd/mpsd DaemonSets.
    total_agents = config.gpu_worker_nodes + config.non_gpu_worker_nodes
    gpu_node_filter = ";".join(f"agent:{i}" for i in range(config.gpu_worker_nodes))
    gpu_node_label = f"nvidia.com/gpu.present=true@{gpu_node_filter}"

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
                    # Don't touch the user's default kubeconfig. The NRI config.toml.tmpl
                    # volume above breaks `k3d kubeconfig get/merge` (the tools node fails
                    # to copy over the mounted file), so we extract the kubeconfig
                    # ourselves via docker in write_kubeconfig() instead.
                    "--kubeconfig-update-default=false",
                    "--kubeconfig-switch-context=false",
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


def write_kubeconfig(config: ClusterConfig) -> str:
    """Extract the cluster's kubeconfig via docker and write it to config.kubeconfig.

    We can't use `k3d kubeconfig get`: the NRI config.toml.tmpl volume mount makes
    k3d's tools node fail to copy the kubeconfig out. Instead we read k3s's own
    kubeconfig from the server container and rewrite:
      - the API server URL to the host-published serverlb port, and
      - the "default" context/cluster/user names to `k3d-<cluster>`, so Skaffold
        recognises it as a k3d cluster and auto-loads built images into it.

    The path is also set as KUBECONFIG for the rest of this process (so the
    fake-gpu-operator install runs against it).
    """
    server = f"k3d-{config.cluster_name}-server-0"
    serverlb = f"k3d-{config.cluster_name}-serverlb"
    ctx = f"k3d-{config.cluster_name}"

    port = str(sh.docker("port", serverlb, "6443/tcp")).strip().splitlines()[0].rsplit(":", 1)[-1]
    raw = str(sh.docker("exec", server, "cat", "/etc/rancher/k3s/k3s.yaml"))

    out = []
    for line in raw.splitlines():
        line = line.replace("https://127.0.0.1:6443", f"https://127.0.0.1:{port}")
        line = line.replace("https://0.0.0.0:6443", f"https://127.0.0.1:{port}")
        stripped = line.strip()
        # k3s.yaml names the context/cluster/user all "default"; rename to k3d-<cluster>.
        if stripped in ("name: default", "cluster: default", "user: default", "- name: default"):
            line = line.replace("default", ctx)
        elif stripped == "current-context: default":
            line = f"current-context: {ctx}"
        out.append(line)

    path = Path(config.kubeconfig).expanduser()
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("\n".join(out) + "\n")
    os.environ["KUBECONFIG"] = str(path)
    log(f"Wrote kubeconfig to {path} (context {ctx})")
    return str(path)


def wait_for_nodes(config: ClusterConfig) -> None:
    log("Waiting for all nodes to be ready...")
    sh.kubectl("wait", "--for=condition=Ready", "nodes", "--all", "--timeout=120s")
    log("All nodes are ready.")


def install_fake_gpu_operator(config: ClusterConfig) -> None:
    log(f"Installing fake-gpu-operator {config.fake_gpu_operator_version}...")

    values = f"""
# k3s pre-creates RuntimeClass "nvidia"; Helm can't adopt it (missing ownership
# annotations). Disable — nvml-mock doesn't need it.
runtimeClass:
  enabled: false
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
    if desired < config.gpu_worker_nodes:
        log(f"nvml-mock scheduled onto {desired} node(s), expected {config.gpu_worker_nodes} "
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
        False, "--skip-fake-gpu-operator", help="Skip fake-gpu-operator install"
    ),
    skip_gpu_mock: bool = typer.Option(
        False, "--skip-gpu-mock", help="Skip nvml-mock install"
    ),
) -> None:
    """Create (or delete) a k3d cluster with fake-gpu-operator and nvml-mock for gpu-sharing-operator e2e tests."""
    config = ClusterConfig()
    if not config.kubeconfig:
        config.kubeconfig = str(Path.home() / ".kube" / f"{config.cluster_name}.yaml")

    require_command("k3d")
    require_command("kubectl")
    require_command("docker")

    if not skip_fake_gpu_operator:
        require_command("helm")
        if not config.fake_gpu_operator_version:
            log("E2E_FAKE_GPU_OPERATOR_VERSION must be set (or pass --skip-fake-gpu-operator).")
            log("See https://github.com/run-ai/fake-gpu-operator/releases for available versions.")
            raise typer.Exit(1)

    if delete:
        delete_cluster(config)
        Path(config.kubeconfig).expanduser().unlink(missing_ok=True)
        return

    if not create_cluster(config):
        raise typer.Exit(1)

    # Extract the kubeconfig (and point this process at it) before any kubectl/helm.
    write_kubeconfig(config)

    wait_for_nodes(config)

    if not skip_fake_gpu_operator:
        install_fake_gpu_operator(config)

    if not skip_gpu_mock:
        install_nvml_mock(config)

    log(f"Cluster '{config.cluster_name}' is ready for e2e tests.")
    log("Next steps (make targets set KUBECONFIG for you):")
    log(f"  make e2e-deploy         # build + load images + install operator")
    log(f"  make test-e2e-metrics   # run the metrics suite")
    log(f"Or point your shell at it directly: export KUBECONFIG={config.kubeconfig}")


if __name__ == "__main__":
    app()
