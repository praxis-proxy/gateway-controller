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
make test               # comprehensive test suite (verify, lint, test-unit, test-integration, kind-up, test-e2e)
make test-unit          # unit tests (hermetic, no cluster)
make test-integration   # integration tests with envtest
make test-e2e           # e2e tests (requires kind cluster)
make lint               # run golangci-lint
make lint-extra         # lint workflows and scripts
make fmt                # run go fmt
make vet                # run go vet
make manifests          # generate CRD and RBAC manifests
make generate           # generate deepcopy implementations
make verify             # verify generated code is committed
make kind-up            # create kind cluster and deploy
make kind-down          # delete kind cluster
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

## Testing

See [test structure guide](test/README.md) for the complete testing strategy:

- **Integration tests** (`make test-integration`): Resource validation and controller logic using envtest
  - CRD schema/CEL validation
  - Controller reconciliation in isolation
  - Fast, run on every commit
- **E2E tests** (`make test-e2e`): Real user scenarios on real Kubernetes cluster
  - Multi-resource workflows (Tenant → AIGateway → ModelProvider → Model)
  - Public-facing API testing (aigateway.praxis.io group)
  - Operational validation (RBAC, cleanup, finalizers)
  - Slower, runs before release

New tests should be added to the appropriate suite based on scope. See [test/README.md](test/README.md) for details.

## API Types

- Comments on exported fields become the CRD `description` and appear in
  `kubectl explain`. Describe what the field is, not why it changed

## Controller Implementation Patterns

When modifying controllers or adding new ones:

**Status updates:**
- Use `Patch` with `client.MergeFrom(resource.DeepCopy())` instead of `Update`
- Safer for concurrent updates; multiple controllers can update different status fields
- Example: `r.Status().Patch(ctx, gw, patch)`

**Validation:**
- Keep controller validation minimal: only runtime dependencies and business logic
- Schema constraints (Minimum, Maximum, Enum, required fields) are enforced by the apiserver
- CEL validation rules (x-kubernetes-validations) are enforced at admission time
- Don't duplicate apiserver validation in the controller

**Logging:**
- Use `log.V(1).Info("reconciling {resource}", "name", req.Name, "namespace", req.Namespace)` at the start (debug level)
- Use `log.Error(err, "...")` only for actual failures
- Include resource identifiers (name, namespace) in log messages

**Status conditions:**
- Use the `setCondition` helper from `helpers.go` for consistent condition updates
- Reason codes: `Ready`, `Invalid`, `DependencyNotReady`, `Conflict` (see aigatewayv1alpha1 constants)
- Always include a human-readable message
- Set `observedGeneration` to `resource.Generation`

## Project Conventions

See [development conventions] for the full guide:
- Code style and organization
- Testing requirements
- Documentation standards
- PR review process

[development conventions]: https://github.com/praxis-proxy/conventions/blob/main/docs/conventions.md
