# Test Structure

This directory contains two complementary test suites with distinct purposes and
scopes.

## API layers

The repository ships two API layers, and the tests are organised around that
split:

- **Core** (`praxis.io`): Proxy, Policy. The gateway runtime and its
  configuration. This is the layer `main` carries.
- **AI Gateway** (`aigateway.praxis.io`): Tenant, AIGateway, ModelProvider,
  Model and the rest of the v1alpha1 design. Developed on the `aigateway`
  branch.

Both layers are user-facing. Neither is an implementation detail of the other,
and both earn e2e scenarios of their own.

Kubernetes built-ins the controllers create — Deployments, Services, ConfigMaps
— are the implementation detail. Assert on them only where a scenario cannot
otherwise tell whether the controller did its job.

## Integration Tests (`./integration`)

**Purpose:** Validate API resources (CRDs) and their controllers using `envtest`,
without a real Kubernetes cluster.

**Scope:**

- Schema validation: required fields, enum values, min/max constraints
- CEL validation rules: business logic enforced at the apiserver level
- Defaulting, and what survives a write: a field the schema does not describe
  is pruned silently, so only a read-back proves it persists
- Controller reconciliation logic: state management, dependency resolution,
  condition updates
- Fast and local. `make test-integration` runs this suite; `make test` runs
  everything, including e2e. All controllers in the repository run together in
  one envtest environment

**Test organization:**

- Two files per resource type: `{resource}_crd_test.go` for apiserver-level
  behaviour, `{resource}_controller_test.go` for reconciliation behaviour
- CRD tests verify that invalid specs are rejected, valid specs are accepted,
  defaults are applied, and submitted fields are read back unchanged
- Controller tests verify conditions, error handling, dependency resolution
- Assert a field in whichever suite owns the behaviour. A field the controller
  never reads belongs in the CRD tests: reconciling it would assert nothing
  and would imply controller involvement that does not exist
- Assert on `reason` as well as `status`; a condition that is False for the
  wrong reason is a defect the status alone will not catch
- Reach for the unstructured client only when the typed one cannot produce the
  absent key under test. `Proxy.spec` is untagged, so the typed client always
  marshals it and an absent `spec` needs unstructured; a struct tagged
  `omitzero` is omitted at its zero value and the typed client is enough

**Expected tests:**

- Schema constraint violations (required fields, range checks, enums)
- CEL validation rule violations (business logic constraints)
- Applied defaults, and nested optional fields surviving a write
- Controller state transitions
- Dependency resolution between resources

## E2E Tests (`./e2e`)

**Purpose:** Test real user scenarios end-to-end against a real Kubernetes
cluster, with the manager deployed as it ships.

**Scope:**

- Real Kubernetes environment: RBAC permissions, service accounts, the
  manager's own Deployment, actual controller reconciliation
- Functional assertions: does the thing the resource describes actually work?
  Resource readiness alone is an integration-suite question
- Operational validation: resource cleanup, finalizers, ownership semantics
- Multi-resource workflows and user journeys within a layer
- Slower, integration-heavy, runs before release

**Test organization:**

- One test file per scenario (e.g. `proxy_test.go`)
- Scenario-driven naming: each test file tells a story about what users can do
- Tests exercise multiple resources working together
- Keep each scenario's specs to the fields its own story needs. A scenario is
  a minimal happy path for that story, not a survey of every field the
  resources accept — exercising a populated spec is the integration suite's
  job. Capabilities belonging to another story arrive with their own
  scenario rather than being bolted onto an existing one

**Expected test scenarios:**

- **manager_test.go**: the deployed manager becomes available and stays healthy
- **proxy_test.go**: a Proxy serves traffic, and configuration changes reach
  the runtime

## Test Execution

```bash
# Integration tests (fast, frequent)
make test-integration

# E2E tests (slow, pre-release)
# test-e2e neither starts a cluster nor deploys the manager: it runs against
# whichever kubeconfig context is selected, so bring one up first.
make kind-up
make test-e2e
```

## Philosophy

- **Integration tests** answer: "Does this resource validate and behave
  correctly in isolation?"
- **E2E tests** answer: "Can a user successfully deploy and operate the
  system?"

Key difference: integration tests use `envtest`, which runs a real
`kube-apiserver` and `etcd` without the rest of a cluster. That is why schema
and CEL validation are meaningful there. E2E tests use a full Kubernetes
cluster, adding real RBAC, networking, a deployed manager, and operational
semantics.

As new resources are added:

1. Add integration tests in `./integration/{resource}_crd_test.go` and
   `./integration/{resource}_controller_test.go`
2. Extend an e2e scenario to include the new resource where it belongs to an
   existing story, or add a scenario where it tells a new one
3. Add functional assertions as the system becomes testable end-to-end
