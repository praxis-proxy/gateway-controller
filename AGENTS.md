# AGENTS.md

This file provides guidance to coding agents when working with code in this repository.

AI tools may assist with implementation, but do not add Claude or another AI tool as a commit collaborator, co-author, or signatory. Commit sign-off belongs to the human contributor responsible for the change.

## What This Is

A Kubernetes controller managing gateway policies and
configuration, written in Go. It implements the control
plane for AI Gateway data plane implementations.

## Requirements

- Go 1.20+
- kubectl for cluster interaction
- kind for local testing
- golangci-lint for linting
- controller-gen for CRD and RBAC generation
- envtest for integration tests

## Key Commands

```console
make build              # build the manager binary
make test               # unit tests (hermetic)
make test-integration   # integration tests with envtest
make lint               # run golangci-lint
make fmt                # run go fmt
make vet                # run go vet
make manifests          # generate CRD and RBAC manifests
make generate           # generate deepcopy implementations
make verify             # verify generated code is committed
make kind-up            # create kind cluster and deploy
make kind-down          # delete kind cluster
make test-e2e           # run e2e tests (requires kind cluster)
make lint-extra         # lint workflows and scripts
```

## Code Quality Standards

From praxis-proxy conventions:

- All commits must be cryptographically signed (GPG/SSH)
- All commits must have `Signed-off-by` trailer (`git commit -s`)
- Conventional commit format: `type(scope): summary`
- New features require unit and integration tests
- No AI-authored or AI-signed-off commits accepted
- PRs limited to 750 lines of production code (tests/docs excluded)
- PR descriptions required (what & why)

## Generated Code

CRD and RBAC manifests in `config/crd/bases/` and
`config/rbac/` are generated. When modifying API types:

```console
make generate   # generate deepcopy and stubs
make manifests  # regenerate CRD and RBAC
make verify     # fail if changes not committed
```

All generated code must be committed.

## Project Conventions

See [development conventions] for the full guide:
- Code style and organization
- Testing requirements
- Documentation standards
- PR review process

[development conventions]: https://github.com/praxis-proxy/conventions/blob/main/docs/conventions.md
