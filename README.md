# NStack

Scale to N -- deploy NVIDIA GPU infrastructure and Slurm on any Kubernetes cluster.

NStack is a CLI tool that automates the deployment of a complete GPU compute stack:
NVIDIA GPU Operator, Slurm via Soperator, MLflow, and monitoring -- all on Kubernetes.

## Quick Start

```bash
# Initialize a site configuration
nstack init --site my-cluster --profile nebius

# Detect cluster capabilities (distro, GPUs, existing operators)
nstack detect --site my-cluster

# Deploy the full stack
nstack deploy --site my-cluster
```

## What It Deploys

NStack organizes deployment into a pipeline of stages, each with dependency resolution
and idempotent apply/destroy lifecycle:

| Stage | Name | Components |
|-------|------|------------|
| 4 | GPU Stack | cert-manager, NVIDIA GPU Operator |
| 5 | Slurm | Soperator (NVIDIA Slurm operator), Slurm cluster CR, K3s patches |
| 6 | MLOps | MLflow, kube-prometheus-stack, Soperator Grafana dashboards |

Stages 0-3 (networking, storage, security, scheduling) are planned for v0.2/v0.3.

## Supported Environments

- **K3s** (tested, with automatic containerd/RuntimeClass patches)
- **kubeadm**
- **EKS** (Amazon)
- **GKE** (Google)
- **AKS** (Azure)
- **Nebius** (with built-in profile)

## Installation

```bash
go install github.com/todd-chamberlain/nstack/cmd/nstack@latest
```

Or build from source:

```bash
git clone https://github.com/todd-chamberlain/nstack.git
cd nstack
make build
# Binary is at ./bin/nstack
```

## Commands

| Command | Description |
|---------|-------------|
| `nstack init` | Create a new site configuration from a profile |
| `nstack detect` | Detect cluster distro, GPU hardware, installed operators |
| `nstack plan` | Show what stages and components would be deployed |
| `nstack validate` | Pre-flight checks (connectivity, RBAC, resource availability) |
| `nstack deploy` | Deploy stages in dependency order |
| `nstack status` | Show deployment state for each stage |
| `nstack upgrade` | Upgrade deployed components to newer versions |
| `nstack destroy` | Tear down stages in reverse dependency order |

## Architecture

```
cmd/nstack/         CLI entry point (Cobra + Viper)
pkg/config/          Site config types, YAML loader, embedded profiles
pkg/detect/          Cluster detection (distro, GPUs, operators)
pkg/engine/          Stage interface, registry, dependency-resolving pipeline
pkg/helm/            Helm SDK client with values merge
pkg/kube/            Kubernetes client wrapper
pkg/output/          Text/JSON output with TTY detection
pkg/stages/          Stage implementations (s4_gpu, s5_slurm, s6_mlops)
pkg/state/           ConfigMap-backed state management
internal/assets/     Embedded Helm value overlays
```

## Configuration

NStack uses YAML site configs stored in `~/.nstack/`:

```bash
nstack init --site my-cluster --profile nebius
# Creates ~/.nstack/sites/my-cluster/config.yaml
```

Global flags: `--output json`, `--verbose`, `--quiet`, `--yes`.

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.
