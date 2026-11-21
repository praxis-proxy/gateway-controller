# Test Structure

This directory contains two complementary test suites with distinct purposes and scopes.

## Integration Tests (`./integration`)

**Purpose:** Validate individual API resources (CRDs) and their controllers in isolation using `envtest`.

**Scope:**
- Schema validation: required fields, enum values, min/max constraints
- CEL validation rules: business logic enforced at the apiserver level
- Defaulting, and what survives a write: a field the schema does not describe
  is pruned silently, so only a read-back proves it persists
- Controller reconciliation logic: state management, dependency resolution, condition updates
- Fast and isolated. `make test-integration` runs this suite; `make test` runs
  everything, including e2e. CI runs it on pushes and pull requests targeting
  `main`

**Test organization:**
- Two files per resource type: `{resource}_crd_test.go` for apiserver-level
  behaviour, `{resource}_controller_test.go` for reconciliation behaviour
- CRD tests verify that invalid specs are rejected, valid specs are accepted,
  defaults are applied, and submitted fields are read back unchanged
- Controller tests verify Ready conditions, error handling, dependency resolution
- Assert a field in whichever suite owns the behaviour. A field the controller
  never reads belongs in the CRD tests: reconciling it would assert nothing
  and would imply controller involvement that does not exist
- Assert on `reason` as well as `status`; a condition that is False for the
  wrong reason is a defect the status alone will not catch
- Reach for the unstructured client only when the typed one cannot produce the
  absent key under test. A zero-valued struct tagged `omitzero`, such as
  `TenantSpec`, is omitted by the typed client; an untagged field such as
  `AIGateway.spec` is always marshalled and needs unstructured

**Expected tests:**
- Schema constraint violations (required fields, range checks, enums)
- CEL validation rule violations (business logic constraints)
- Applied defaults, and nested optional fields surviving a write
- Controller state transitions (Ready→Invalid, Waiting→Ready)
- Dependency resolution (Tenant not ready, TenantRef mismatch, etc.)

## E2E Tests (`./e2e`)

**Purpose:** Test real user scenarios end-to-end using a real Kubernetes cluster, focusing on public-facing APIs.

**Scope:**
- **Public-facing APIs only**: Tenant, AIGateway, ModelProvider, Model (aigateway.praxis.io group)
  - These are what users interact with and care about
  - Full validation of multi-resource workflows and user journeys
- **Internal APIs as side effects**: Core APIs (Deployment, Service, ConfigMap, etc.) may be verified for correctness but are not the focus
  - Implementation details; E2E tests verify the user-facing layer, not the internals
- Real Kubernetes environment: RBAC permissions, service accounts, actual controller reconciliation
- Functional assertions: does the gateway actually work as expected?
- Operational validation: proper resource cleanup, finalizers, ownership semantics
- Slower, integration-heavy, runs before release

**Test organization:**
- One test file per scenario (e.g., `minimal_gateway_stack_test.go`)
- Scenario-driven naming: each test file tells a story about what users can do
- Tests exercise multiple resources working together
- Tests verify end-to-end functionality, not just resource readiness
- Keep each scenario's specs to the fields its own story needs. A scenario is
  a minimal happy path for that story, not a survey of every field the
  resources accept — exercising a populated spec is the integration suite's
  job. Capabilities belonging to another story arrive with their own
  scenario rather than being bolted onto an existing one

**Expected test scenarios:**
- **minimal_gateway_stack_test.go**: Deploy minimal Tenant + AIGateway, verify Ready
  - Future: Add ModelProvider and Model, verify complete stack is Ready
  - Future: Send inference request, verify response from gateway
- **model_conflict_resolution_test.go** (future): Create two Models with same published name, verify conflict handling
- **tenant_model_lifecycle_test.go** (future): Create Tenant, add Models, delete Tenant, verify cleanup
- **request_flow_test.go** (future): Send various inference requests, verify routing and responses

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

- **Integration tests** answer: "Does this resource validate and behave correctly in isolation?"
- **E2E tests** answer: "Can a user successfully deploy and operate a complete gateway system?"

Key difference: integration tests use `envtest`, which runs a real `kube-apiserver`
and `etcd` without the rest of a cluster. That is why schema and CEL validation
are meaningful there. E2E tests use a full Kubernetes cluster, adding real RBAC,
networking, a deployed manager, and operational semantics.

As new resources (ModelProvider, Model) are added:
1. Add integration tests in `./integration/{resource}_crd_test.go`
2. Extend E2E scenarios to include the new resource in multi-resource workflows
3. Add functional assertions as the system becomes testable end-to-end
