# NStack

**Scale to N** — deploy NVIDIA GPU infrastructure + Slurm on any Kubernetes cluster.

NStack is a single Go binary that deploys the full NVIDIA GPU stack (GPU Operator, Network Operator, DPU/DOCA, KAI Scheduler, Soperator/Slurm, MLflow, monitoring) via a staged pipeline with environment detection and profile-based adaptation. Supports multi-site Slurm federation over Tailscale with data-locality-aware job scheduling and cross-site telemetry.

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
| **0: Discovery** | IPMI/Redfish BMC scanning, GPU/NIC/DPU hardware inventory |
| **1: Provisioning** | Metal3/Ironic bare metal OS install, BMH CRD management |
| **2: Kubernetes** | K3s/kubeadm bootstrap, managed cluster validation |
| **3: Networking** | NVIDIA Network Operator, Multus CNI, DOCA/DPU, WireGuard/Tailscale overlay |
| **4: GPU Stack** | cert-manager, NVIDIA GPU Operator, KAI Scheduler |
| **5: Slurm** | Nebius Soperator, Slurm cluster, NodeSets, federation setup |
| **6: MLOps** | MLflow, kube-prometheus-stack, Grafana dashboards, Thanos telemetry |

Jump in wherever your infrastructure starts:

```bash
nstack deploy --site lab --from stage4   # Full pipeline from GPU stack
nstack deploy --site lab --only stage5   # Just Slurm
nstack deploy --site lab --stages 4,6    # Cherry-pick stages
nstack deploy --site lab --parallel      # Run independent stages concurrently
```

## Site Configuration

```yaml
version: v1
sites:
  lab-home:
    profile: k3s-single
    kubeconfig: /etc/rancher/k3s/k3s.yaml
    cluster:
      name: slurm1
      namespace: slurm
    versions:
      gpu-operator: "v26.3.0"
      soperator: "3.0.2"
    overrides:
      slurm-cluster:
        slurmNodes:
          worker:
            slurmd:
              gpuPerNode: 3
```

## Profiles

- `k3s-single` — single-node K3s with hostPath storage
- `k3s-multi` — multi-node K3s with dynamic PVC storage
- `kubeadm` — standard Kubernetes
- `nebius` — Nebius AI Cloud managed Kubernetes

## Multi-Site Federation

Connect clusters across sites with Tailscale overlay, federated Slurm job scheduling, and unified telemetry:

```yaml
sites:
  home-lab:
    overlay:
      type: tailscale
    federation:
      name: nstack-fed
      features: [site-home, has-small-gpu]
      accounting:
        host: slurmdbd.tailnet.ts.net
        deploy: true
      telemetry:
        type: thanos
        remoteWriteUrl: http://thanos-receive:19291/api/v1/receive

  dc-east:
    overlay:
      type: tailscale
    federation:
      name: nstack-fed
      features: [site-dc-east, has-h100, has-imagenet]
      accounting:
        host: slurmdbd.tailnet.ts.net
        deploy: false
```

Jobs route automatically via data locality:

```bash
sbatch --cluster-constraint=has-imagenet --constraint=gpu train.sh
```

See [docs/examples/multi-site-federation.yaml](docs/examples/multi-site-federation.yaml) for a complete two-site example.

## Key Features

- **Configurable versions** — override any component version via `site.versions` without rebuilding
- **Cluster identity** — configurable namespace, cluster name, site-scoped state isolation
- **Chart caching** — soperator repo cached at `~/.nstack/cache/` for fast re-deploys
- **Parallel execution** — `--parallel` flag runs independent stages concurrently
- **Label-based discovery** — no hardcoded pod names, works with any replica count
- **Registry override** — `profile.images.registry` overrides all soperator image paths
- **Federation** — Slurm federation with sacctmgr, cluster features for data locality
- **Cross-site telemetry** — Prometheus remote_write to Thanos Receive with cluster/site labels

## Supported Environments

- **K3s** (single-node and multi-node)
- **kubeadm** (standard Kubernetes)
- **EKS / GKE / AKS** (managed cloud)
- **Nebius Cloud** (native Soperator support)

## License

Apache 2.0
