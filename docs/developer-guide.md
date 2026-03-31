# NStack Developer Guide

This guide covers extending NStack: adding stages, profiles, chart values, and testing. It assumes familiarity with Go, Helm, and Kubernetes concepts.

## Adding a New Stage

Every deployment stage implements the `Stage` interface defined in `pkg/engine/stage.go`:

```go
type Stage interface {
    Number() int
    Name() string
    Dependencies() []int
    Detect(ctx context.Context, kc *kube.Client) (*DetectResult, error)
    Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error
    Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*StagePlan, error)
    Apply(ctx context.Context, kc *kube.Client, hc *helm.Client, site *config.Site, profile *config.Profile, plan *StagePlan, printer *output.Printer) error
    Status(ctx context.Context, kc *kube.Client) (*StageStatus, error)
    Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error
}
```

### Step-by-Step

**1. Create the stage package:**

```bash
mkdir -p pkg/stages/s7_mystage
```

**2. Implement the stage struct:**

```go
// pkg/stages/s7_mystage/stage.go
package s7_mystage

import (
    "context"
    "github.com/todd-chamberlain/nstack/pkg/config"
    "github.com/todd-chamberlain/nstack/pkg/engine"
    "github.com/todd-chamberlain/nstack/pkg/helm"
    "github.com/todd-chamberlain/nstack/pkg/kube"
    "github.com/todd-chamberlain/nstack/pkg/output"
    "github.com/todd-chamberlain/nstack/pkg/state"
)

type MyStage struct{}

func New() *MyStage { return &MyStage{} }

func (s *MyStage) Number() int         { return 7 }
func (s *MyStage) Name() string        { return "My Stage" }
func (s *MyStage) Dependencies() []int { return []int{4} } // depends on GPU Stack
```

**3. Implement Detect:**

Detect probes the cluster for existing installations. Use `engine.DetectDeployment()` for Deployment-based components:

```go
func (s *MyStage) Detect(ctx context.Context, kc *kube.Client) (*engine.DetectResult, error) {
    return &engine.DetectResult{
        Operators: []engine.DetectedOperator{
            engine.DetectDeployment(ctx, kc.Clientset(), "my-namespace", "my-deployment", "my-component"),
        },
    }, nil
}
```

**4. Implement Validate:**

Validate checks prerequisites before applying. At minimum, verify the cluster is reachable:

```go
func (s *MyStage) Validate(ctx context.Context, kc *kube.Client, profile *config.Profile) error {
    return engine.ValidateClusterReachable(ctx, kc.Clientset())
}
```

**5. Implement Plan:**

Plan determines what actions are needed without making changes. Use the shared helpers `engine.PlanDeploymentComponent()` or `engine.PlanHelmComponent()`:

```go
func (s *MyStage) Plan(ctx context.Context, kc *kube.Client, profile *config.Profile, current *state.StageState) (*engine.StagePlan, error) {
    plan := &engine.StagePlan{Stage: s.Number(), Name: s.Name()}

    cs := kc.Clientset()
    plan.Components = append(plan.Components,
        engine.PlanDeploymentComponent(ctx, cs, "my-component", "my-chart", "1.0.0", "my-namespace", "my-deployment"),
    )

    plan.Action = engine.DeterminePlanAction(plan.Components, plan.Patches)
    return plan, nil
}
```

**6. Implement Apply:**

Apply executes the plan. Iterate over `plan.Components`, skip those with `action: "skip"`, and install the rest:

```go
func (s *MyStage) Apply(ctx context.Context, kc *kube.Client, hc *helm.Client, site *config.Site, profile *config.Profile, plan *engine.StagePlan, printer *output.Printer) error {
    total := len(plan.Components)
    for i, comp := range plan.Components {
        idx := i + 1
        switch comp.Action {
        case "skip":
            printer.ComponentSkipped(idx, total, comp.Name, comp.Current, "already installed")
        case "install":
            printer.ComponentStart(idx, total, comp.Name, comp.Version, "installing")
            // Load values and install via Helm
            var overrides map[string]interface{}
            if site != nil && site.Overrides != nil {
                overrides = site.Overrides["my-component"]
            }
            var distribution string
            if profile != nil {
                distribution = profile.Kubernetes.Distribution
            }
            values, err := helm.LoadChartValues("my-component", distribution, overrides)
            if err != nil {
                return err
            }
            err = hc.UpgradeOrInstall(ctx, "my-release", "my-chart-ref", "my-namespace", values)
            printer.ComponentDone(comp.Name, err)
            if err != nil {
                return err
            }
        }
    }
    return nil
}
```

**7. Implement Status and Destroy:**

```go
func (s *MyStage) Status(ctx context.Context, kc *kube.Client) (*engine.StageStatus, error) {
    cs := kc.Clientset()
    compStatus := engine.CheckDeploymentStatus(ctx, cs, "my-namespace", "my-deployment", "my-component")
    status := &engine.StageStatus{
        Stage:      s.Number(),
        Name:       s.Name(),
        Version:    compStatus.Version,
        Components: []engine.ComponentStatus{compStatus},
    }
    status.Status = engine.DetermineOverallStatus(status.Components)
    return status, nil
}

func (s *MyStage) Destroy(ctx context.Context, kc *kube.Client, hc *helm.Client, printer *output.Printer) error {
    return engine.DestroyHelmRelease(hc, "my-component", "my-release", "my-namespace", 1, 1, printer)
}
```

**8. Register the stage in `cmd/nstack/stages.go`:**

```go
import "github.com/todd-chamberlain/nstack/pkg/stages/s7_mystage"

func buildRegistry() *engine.Registry {
    r := engine.NewRegistry()
    // ... existing stages ...
    r.Register(s7_mystage.New())
    return r
}
```

**9. Add embedded chart values:**

```bash
mkdir -p internal/assets/charts/my-component
```

Create `internal/assets/charts/my-component/common.yaml` with default Helm values. Optionally add distribution overlays like `k3s.yaml`.

## Adding a New Profile

Profiles are YAML files embedded in `internal/assets/profiles/`. They define environment-specific defaults for Kubernetes distribution, storage strategy, networking, and container image registries.

### Create the profile YAML

Create `internal/assets/profiles/my-profile.yaml`:

```yaml
name: my-profile
description: Custom environment profile
kubernetes:
  distribution: k3s          # k3s, kubeadm, managed, eks, gke, aks
  multiNode: true
  cgroupV2: true
  containerdSocket: /run/k3s/containerd/containerd.sock
  storageClass: local-path
  runtimeClass: nvidia
patches:
  containerdSocketBind: true  # K3s needs this for kruise-daemon
storage:
  type: pvc                   # "hostPath" or "pvc"
networking:
  overlay: tailscale          # "wireguard", "tailscale", "none"
  fabric: none                # "infiniband", "roce", "none"
images:
  registry: ghcr.io/nebius/soperator
```

### How profiles connect to chart overlays

The `distribution` field in the profile's `kubernetes` section determines which chart overlay file is loaded. When a stage calls `helm.LoadChartValues("gpu-operator", profile.Kubernetes.Distribution, overrides)`, it looks for:

1. `internal/assets/charts/gpu-operator/common.yaml` (always loaded)
2. `internal/assets/charts/gpu-operator/k3s.yaml` (loaded if `distribution = "k3s"`)

If the overlay file does not exist for a given distribution, only the common values are used. This is the correct behavior for distributions that need no special treatment.

### Using the profile

Reference it by name in the site config:

```yaml
sites:
  my-site:
    profile: my-profile
    kubeconfig: ~/.kube/config
```

Profiles are loaded at runtime by `config.LoadProfile()` (`pkg/config/profiles.go`), which reads from the embedded `assets.FS` filesystem.

## Custom Helm Values

### The merge order

Every Helm chart install goes through a four-layer merge:

```
common.yaml -> distribution overlay -> site overrides -> CLI flags
```

The merge logic is in `pkg/helm/values.go`:

- `LoadChartValues(chartName, distribution, overrides)` handles layers 1-3.
- `ParseSetValues(sets)` parses `--set` flags into a nested map.
- `MergeValues(layers...)` does the recursive deep merge.

Nested maps merge recursively. Scalar values and slices are replaced entirely (not appended).

### Site overrides

In the config file, `site.overrides` is a two-level map: component name to values map.

```yaml
sites:
  lab:
    overrides:
      gpu-operator:
        driver:
          enabled: false
        toolkit:
          enabled: true
      slurm-cluster:
        slurmNodes:
          worker:
            slurmd:
              gpuPerNode: 3
```

In the stage's `Apply()` method, it reads overrides like this:

```go
var overrides map[string]interface{}
if site != nil && site.Overrides != nil {
    overrides = site.Overrides["gpu-operator"]
}
values, err := helm.LoadChartValues("gpu-operator", distribution, overrides)
```

### CLI flags

The `--set` flag uses dot-separated paths with a component prefix:

```bash
nstack deploy --site lab --set gpu-operator.driver.enabled=false
```

The `deploy_cmd.go` handler routes these into `site.Overrides` before passing them to the pipeline.

## Version Management

### How it works

Each stage defines a compiled default version as a Go constant:

```go
// pkg/stages/s5_slurm/soperator.go
const soperatorVersion = "3.0.2"
```

At apply time, the stage calls `config.ResolveVersion()` to check if the site config has an override:

```go
gitTag := config.ResolveVersion(site, "soperator", soperatorVersion)
```

The `ResolveVersion` function (`pkg/config/versions.go`):

```go
func ResolveVersion(site *Site, component, defaultVersion string) string {
    if site != nil && site.Versions != nil {
        if v, ok := site.Versions[component]; ok && v != "" {
            return v
        }
    }
    return defaultVersion
}
```

### Site config override

```yaml
sites:
  lab:
    versions:
      gpu-operator: "v26.3.0"
      soperator: "3.1.0"
      cert-manager: "v1.14.0"
```

### Adding a new versioned component

1. Define a `const` in your stage source with the default version.
2. In `Apply()`, use `config.ResolveVersion(site, "component-name", defaultVersion)`.
3. Document the component name in the README or config examples so users know what key to use.

## Cluster Identity

### How it threads through

`site.cluster.name` and `site.cluster.namespace` define the Slurm cluster identity. These are resolved by `config.ResolveCluster()` (`pkg/config/types.go:194`) with defaults of `name: slurm1`, `namespace: slurm`.

The cluster config is used for:

- **Helm release naming** -- the slurm-cluster chart is installed with values referencing `cluster.Name`.
- **PV naming** -- hostPath PVs are prefixed with the namespace (e.g., `slurm-controller-spool-pv`) to avoid collisions since PVs are cluster-scoped.
- **State ConfigMap isolation** -- each site gets its own ConfigMap named `nstack-state-<siteName>` in the `nstack-system` namespace.
- **Federation identity** -- `cluster.Name` is used as the Slurm cluster name in sacctmgr commands.

### Multiple clusters on one K8s cluster

You can run multiple Slurm clusters on the same Kubernetes cluster by using different namespaces:

```yaml
sites:
  dev-cluster:
    cluster:
      name: slurm-dev
      namespace: slurm-dev
  prod-cluster:
    cluster:
      name: slurm-prod
      namespace: slurm-prod
```

Each will get its own set of PVs, PVCs, and state tracking.

## Multi-Site Federation

### Config schema

Federation is configured per-site under `site.federation`:

```yaml
federation:
  name: nstack-fed              # Shared federation name
  features:                     # Cluster features for job routing
    - site-dc-east
    - has-h100
    - has-imagenet
  accounting:
    host: slurmdbd.tailnet.ts.net   # slurmdbd hostname (MagicDNS)
    port: 6819                       # Default 6819
    deploy: true                     # Only ONE site should deploy slurmdbd
    database:                        # Only when deploy=true
      host: mariadb.slurm.svc.cluster.local
      name: slurm_acct_db
      user: slurm
      password: changeme
  telemetry:
    type: thanos                     # "thanos", "prometheus-federation", "none"
    remoteWriteUrl: http://thanos-receive:19291/api/v1/receive
```

### How sacctmgr commands are executed

Federation setup happens in `pkg/stages/s5_slurm/federation.go:30`. After the Slurm cluster is deployed and reconciled, NStack:

1. Finds a running controller pod by label selector `app.kubernetes.io/component=controller`.
2. Executes sacctmgr commands via `kubectl exec` inside the `slurmctld` container:
   - `sacctmgr -i add federation <name>` -- creates the federation (idempotent).
   - `sacctmgr -i modify cluster <name> set federation=<fed>` -- adds this cluster.
   - `sacctmgr -i modify cluster <name> set features=<f1>,<f2>` -- sets data locality features.
3. All names are validated against `[a-zA-Z0-9_-]+` before being interpolated into commands.

### Cluster features for data locality

Features are arbitrary strings that describe what a cluster has. Users submit jobs with `--cluster-constraint` to target specific features:

```bash
# Route to a cluster that has ImageNet data cached locally
sbatch --cluster-constraint=has-imagenet train.sh

# Route to a cluster with H100 GPUs
sbatch --cluster-constraint=has-h100 --constraint=gpu train.sh
```

### Tailscale subnet router and service exposure

The Tailscale overlay (`pkg/stages/s3_networking/overlay.go`) deploys the Tailscale Kubernetes Operator with subnet routes configured to expose pod and service CIDRs across the tailnet. This allows slurmdbd on Site A to be reachable from Site B via MagicDNS.

When `accounting.deploy: true` and the overlay is Tailscale, NStack annotates the slurmdbd service (`pkg/stages/s5_slurm/federation.go:103`) with:

```yaml
tailscale.com/expose: "true"
tailscale.com/hostname: "<cluster-name>-slurmdbd"
```

### Thanos telemetry integration

The monitoring stage (`pkg/stages/s6_mlops/monitoring.go`) configures Prometheus with `remote_write` to the Thanos Receive endpoint specified in `telemetry.remoteWriteUrl`. Each site's Prometheus adds `cluster` and `site` external labels so Grafana can filter and aggregate across sites.

## Plugin Architecture (Exec-Based)

NStack supports external plugins via an exec-based protocol. Plugins are standalone executables that communicate with NStack over stdin/stdout using JSON-RPC.

### Writing an external plugin

A plugin is any executable that:

1. Accepts JSON-RPC messages on stdin.
2. Writes JSON-RPC responses to stdout.
3. Implements the Stage interface contract (the same methods as `pkg/engine/stage.go`).

### JSON-RPC protocol

Messages follow JSON-RPC 2.0:

```json
{"jsonrpc": "2.0", "method": "Plan", "params": {"profile": {...}, "current": {...}}, "id": 1}
```

Response:

```json
{"jsonrpc": "2.0", "result": {"stage": 7, "name": "MyPlugin", "action": "install", "components": [...]}, "id": 1}
```

### Plugin registration in config

Plugins are registered in the site's `hooks` section:

```yaml
hooks:
  stage5:
    post-apply:
      - script: /usr/local/bin/my-plugin
        timeout: 5m
```

### The Stage interface contract

External plugins must handle these methods:

| Method | Input | Output |
|--------|-------|--------|
| `Number` | none | `int` |
| `Name` | none | `string` |
| `Dependencies` | none | `[]int` |
| `Detect` | kube context | `DetectResult` |
| `Validate` | kube context, profile | error or null |
| `Plan` | kube context, profile, current state | `StagePlan` |
| `Apply` | kube context, site, profile, plan | error or null |
| `Status` | kube context | `StageStatus` |
| `Destroy` | kube context | error or null |

## Chart Caching

The soperator chart is cloned from GitHub and cached locally to avoid re-downloading on every deploy.

### Cache location

```
~/.nstack/cache/soperator/<tag>/
```

For example, version 3.0.2 is cached at `~/.nstack/cache/soperator/3.0.2/`.

### Validation

Cache validity is checked by `isCacheValid()` in `pkg/stages/s5_slurm/soperator.go:113`:

1. `.git/HEAD` must exist and contain at least 10 characters.
2. `.git/refs/tags/<tag>` should exist (but detached HEAD clones may not have it, so HEAD is accepted as a fallback).
3. `helm/soperator/charts/` must exist (indicates `helm dep update` has been run).

### When cache is invalidated

The cache is invalidated (directory deleted and re-cloned) when:

- The tag directory does not exist.
- Any of the validation checks above fail.
- The user changes the soperator version in `site.versions`.

On cache miss, `cloneSoperatorRepo()` performs a shallow `git clone --depth 1 --branch <tag>` followed by `helm dep update` on all chart subdirectories. If the cache directory cannot be created (permissions, etc.), it falls back to a temp directory that is cleaned up after the deploy.

## Testing

### Unit tests with fake clientsets

NStack uses `k8s.io/client-go/kubernetes/fake` for unit tests. This provides an in-memory Kubernetes API that supports CRUD operations without a real cluster.

```go
import "k8s.io/client-go/kubernetes/fake"

func TestMyFeature(t *testing.T) {
    cs := fake.NewSimpleClientset()
    // cs implements kubernetes.Interface -- pass to functions that need it
}
```

### Testing stages with mock Helm clients

The pipeline tests in `pkg/engine/pipeline_test.go` use `recordingStage` -- a mock that records which methods were called and can be configured to return specific plans or errors:

```go
s := &recordingStage{
    num:      7,
    name:     "my-stage",
    deps:     []int{4},
    applyErr: nil, // or errors.New("fail") to simulate failure
    plan: &StagePlan{
        Stage:  7,
        Name:   "my-stage",
        Action: "install",
    },
}
```

### Pipeline tests

The `newTestPipeline()` helper (`pkg/engine/pipeline_test.go:95`) sets up a Pipeline with a fake clientset and in-memory state store:

```go
p, reg := newTestPipeline(t, stage1, stage2, stage3)
err := p.Run(context.Background(), RunOpts{
    KubeClient: kube.NewClientFromInterfaces(fake.NewSimpleClientset(), nil, nil),
    Profile:    &config.Profile{Name: "test"},
    Site:       &config.Site{Name: "test"},
})
```

Key test patterns:

- **TestPipeline_RunAll** -- verifies all stages are applied in order.
- **TestPipeline_SkipDeployed** -- pre-populates state and verifies deployed stages are skipped.
- **TestPipeline_Force** -- verifies `--force` re-applies deployed stages.
- **TestPipeline_StopOnFailure** -- verifies pipeline halts and records failure state.
- **TestPipeline_Parallel** -- verifies diamond dependency graph runs correctly with concurrent execution.
- **TestAssignLevels** -- unit tests the level assignment algorithm for various dependency graphs.

### Running the full test suite

```bash
# Run all tests
make test

# Run tests with verbose output
go test ./... -v

# Run tests for a specific package
go test ./pkg/engine/ -v -run TestPipeline

# Run tests with race detection
go test ./... -race
```

### Writing tests for stage implementations

Each stage package has its own `_test.go` files. Follow these patterns:

1. Use `fake.NewSimpleClientset()` for the Kubernetes client.
2. Pre-create any resources the stage expects (namespaces, deployments, ConfigMaps).
3. Test both the `Plan()` output and the `Apply()` side effects.
4. Test `Detect()` and `Status()` with pre-existing resources.
5. Test `Destroy()` to verify cleanup is correct.

Example from `pkg/stages/s4_gpu/stage_test.go`:

```go
func TestGPUStage_Plan_AllNew(t *testing.T) {
    cs := fake.NewSimpleClientset()
    kc := kube.NewClientFromInterfaces(cs, nil, nil)
    stage := New()

    plan, err := stage.Plan(context.Background(), kc, &config.Profile{Name: "test"}, nil)
    if err != nil {
        t.Fatalf("Plan: %v", err)
    }

    // All components should be "install" since nothing exists.
    for _, comp := range plan.Components {
        if comp.Action != "install" {
            t.Errorf("component %s: action = %q, want install", comp.Name, comp.Action)
        }
    }
}
```

## Contributing

### How to add a new feature without breaking existing deploys

1. **Add, don't change.** New components should be additive. Existing stage behavior should not change unless explicitly intended.
2. **Use feature detection.** Check whether a component is already installed before touching it. The `PlanDeploymentComponent()` and `PlanHelmComponent()` helpers handle this.
3. **Default to skip.** If a new feature requires opt-in, check for its presence in site overrides before installing. See the KAI Scheduler in `pkg/stages/s4_gpu/stage.go:93` for an example.
4. **Preserve state compatibility.** The state ConfigMap schema is append-only. New fields are fine; removing or renaming fields breaks existing deploys.

### The review/simplification/hardening loop

NStack development follows this pattern:

1. **Build the feature.** Get it working end-to-end.
2. **Simplify.** Extract shared patterns into `pkg/engine/helpers.go`. Reduce duplication across stages.
3. **Harden.** Add error handling, timeouts, idempotency checks, and input validation (e.g., the sacctmgr name validation in federation.go).
4. **Test.** Write unit tests for the new code paths using fake clientsets and recording stages.

### Writing tests for new code paths

Every new feature should have tests that cover:

- **Happy path** -- the feature works as expected with valid input.
- **Already-deployed** -- the feature skips gracefully when the component already exists.
- **Failure handling** -- errors are propagated correctly and state is recorded as failed.
- **Edge cases** -- nil configs, empty overrides, missing profiles.

### Code organization conventions

- Stage packages are named `s<N>_<name>` (e.g., `s5_slurm`).
- Each stage has a `stage.go` with the main struct and interface methods.
- Sub-components get their own files (e.g., `soperator.go`, `federation.go`, `storage.go`).
- Constants for chart names, versions, release names, and namespaces go at the top of the relevant file.
- Shared helpers that are used by multiple stages go in `pkg/engine/helpers.go`.
- Stage-specific helpers stay in the stage package.
