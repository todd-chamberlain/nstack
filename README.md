# NStack

**Scale to N** — deploy NVIDIA GPU infrastructure + Slurm on any Kubernetes cluster.

NStack is a single Go binary that deploys the full NVIDIA GPU stack (GPU Operator, Soperator/Slurm, MLflow, monitoring) via a staged pipeline with environment detection and profile-based adaptation.

## Install

```bash
go install github.com/todd-chamberlain/nstack/cmd/nstack@latest
```

Or download a binary from [Releases](https://github.com/todd-chamberlain/nstack/releases).

## Quick Start

```bash
# Initialize config
nstack init --site lab --profile k3s-single --kubeconfig /etc/rancher/k3s/k3s.yaml

# Detect cluster hardware
nstack detect --site lab

# Deploy everything
nstack deploy --site lab

# Check status
nstack status --site lab
```

## What It Deploys

| Stage | Components |
|-------|-----------|
| **4: GPU Stack** | cert-manager, NVIDIA GPU Operator |
| **5: Slurm** | Nebius Soperator, Slurm cluster, NodeSets, K3s patches |
| **6: MLOps** | MLflow, kube-prometheus-stack, Grafana dashboards |

Jump in wherever your infrastructure starts:

```bash
nstack deploy --site lab --from stage4   # Full pipeline
nstack deploy --site lab --only stage5   # Just Slurm
nstack deploy --site lab --stages 4,6    # Cherry-pick
```

## Supported Environments

- **K3s** (single-node and multi-node) with automatic cgroup v2 patches
- **kubeadm** (standard Kubernetes)
- **EKS / GKE / AKS** (managed cloud)
- **Nebius Cloud** (native Soperator support)

## Profiles

Profiles define environment-specific behavior. NStack ships with:

- `k3s-single` — single-node K3s with hostPath storage
- `k3s-multi` — multi-node K3s with dynamic PVC storage
- `kubeadm` — standard Kubernetes
- `nebius` — Nebius AI Cloud managed Kubernetes

## Roadmap

- **v0.1** (current): Stages 4-6, detection, profiles
- **v0.2**: Stage 3 (NVIDIA Network Operator, Multus, DOCA/DPU, WireGuard overlay)
- **v0.3**: Stages 0-2 (IPMI discovery, Metal3 provisioning, K8s bootstrap)

## License

Apache 2.0
