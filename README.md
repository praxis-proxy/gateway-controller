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

[development conventions]: https://github.com/praxis-proxy/conventions/blob/main/docs/conventions.md
