# Contributing to NStack

## Development

```bash
# Build
make build

# Run tests
make test

# Lint
make lint

# Run all checks
make build test lint
```

## Requirements

- Go 1.23+
- golangci-lint (for linting)

## Pull Request Process

1. Fork the repository
2. Create a feature branch from `master`
3. Write tests for new functionality
4. Ensure `make build test lint` passes
5. Open a PR against `master`

## Code Style

- Run `gofmt` on all Go files
- Pass `golangci-lint run`
- Follow existing patterns in the codebase
- Add tests for new packages

## Reporting Issues

Open an issue on GitHub with:
- NStack version (`nstack version`)
- Kubernetes version and distribution
- Steps to reproduce
- Expected vs actual behavior
