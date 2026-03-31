# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| 0.4.x   | Yes       |
| 0.3.x   | Yes       |
| < 0.3   | No        |

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly:

1. **Do NOT** open a public GitHub issue
2. Use [GitHub Security Advisories](https://github.com/todd-chamberlain/nstack/security/advisories/new) to report privately
3. Include steps to reproduce and potential impact

We will acknowledge receipt within 48 hours and provide a timeline for a fix.

## Security Considerations

NStack manages Kubernetes infrastructure and executes Helm operations with cluster-admin privileges. Key security properties:

- **No secrets in state**: The nstack-state ConfigMap contains only deployment metadata, never credentials
- **No shell interpolation**: All `os/exec` calls pass arguments as separate array elements, preventing injection
- **Kubeconfig isolation**: Each site uses its own kubeconfig; multi-site operations never cross credentials
- **Config file trust**: `~/.nstack/config.yaml` is trusted input (same trust level as `~/.kube/config`)
- **Input validation**: Federation names, cluster names, and features are validated against `[a-zA-Z0-9_-]+` before use in exec calls
- **File-based writes**: Systemd unit files are written via `os.WriteFile`, not shell interpolation
- **Site-scoped state**: Each site gets its own state ConfigMap (`nstack-state-<site>`), preventing cross-site interference
