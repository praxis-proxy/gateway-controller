# Control Plane for Praxis Gateways

This controller will manage the Kubernetes based control plane API for Gateway implementations such as AI Gateway.

## Development

Run `make help` for the full target list. The common ones:

```console
make build             # build the manager binary
make test              # comprehensive test suite (verify, lint, test-unit, test-integration, kind-up, test-e2e)
make test-unit         # unit tests (hermetic, no cluster)
make test-integration  # integration tests against envtest
make test-e2e          # e2e tests (requires kind cluster)
make kind-up           # create a kind cluster and deploy the manager into it
make kind-down         # delete the cluster
make lint              # golangci-lint
make verify            # fail if generated or derived output is uncommitted
```

Development follows the praxis-proxy [development conventions].

## Two-Branch Development Model

This repository maintains two long-lived branches reflecting two API layers:

- **`main`**: The core Praxis APIs (`praxis.io`). Proxy and Policy resources
  for configuring the data plane gateway runtime.
- **`aigateway`**: The full AI Gateway stack, adding the `aigateway.praxis.io`
  layer (Tenant, AIGateway, ModelProvider, Model, MCP, Agent) on top of core.

Both branches run the full CI suite.

### Why Two Branches?

The core APIs present a smaller integration surface and will stabilise sooner.
The AI Gateway layer is a larger, still-evolving design. Keeping them on
separate branches lets consumers integrate the core layer independently,
without taking a dependency on the full stack before it is ready.

### Development Workflow

- **Core APIs and fixes** target `main`
- **AI Gateway features** target `aigateway`
- The `aigateway` branch regularly rebases onto `main` to stay current with core
  improvements
- Never merge `aigateway` back to `main`; the history flows one direction only

Shared changes (CI, documentation, shared utilities) land on `main` first, then
flow to `aigateway` via rebase.

[development conventions]: https://github.com/praxis-proxy/conventions/blob/main/docs/conventions.md
