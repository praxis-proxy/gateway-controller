# Implementation plan: generate a core Policy for Secret-backed API-key auth

Source design: [Gateway authentication](../../design/gateway-auth/gateway-authentication.md).
Task checklist: [Policy-generation tasks](tasks/todo.md#gateway-authentication-implementation).
Follow-up example: [MaaS HTTP validation](../../design/gateway-auth/mass-auth-example.md).
Package implementation contract: [Discovery and rendering](package-contract.md).

## Goal

**Given a Tenant-authorized AIGateway with one `apiKey` authentication instance
and a referenced Secret, the controller creates and maintains one valid,
gateway-owned core `Policy` resource (`praxis.io/v1alpha1`).** Its `spec.document`
contains the generated PPE API-key authentication configuration.

This is a control-plane delivery. It is complete when that Policy object is
correctly generated, accepted by the core API and verified by tests. Proxy
creation, filter-chain attachment, actual Secret mounting, serving readiness and
live traffic enforcement are later core integration work and do not block this
milestone. Creating the Policy must not make the AIGateway report serving readiness.

The agreed scope is one configured `apiKey` instance per AIGateway. Its Secret can
contain multiple verifier records for consumers and rotation. Reject multiple
configured instances before publication; keep the API list extensible for later
OR selection. Keep the generic package contract and discovery, with a second
package fixture proving dispatch without auth-specific controller code.

## Prerequisites and output boundary

Policy generation integrates the Tenant and AIGateway layer with the core Policy
API. The AIGateway reconciler resolves Tenant readiness and authorizes flat
authentication entries by exact type ID. The first delivery rejects more than
one configured instance while the CRD retains an extensible list. S12 extends
this reconciliation path to own the generated Policy. The
[test structure guide](../../../test/README.md#api-layers) describes the two layers.

Spec validation and Tenant readiness do not establish serving readiness.
Policy generation must not be presented as proof of traffic enforcement.

The core Policy API already accepts a YAML `spec.document` and optional filter
settings. Its current controller checks YAML parsing and records acceptance;
that status does not establish PPE runtime validity or traffic enforcement.
Generated-document tests must also assert the expected plugin settings, identity
mapping, authentication requirement and credential stripping.

The Policy API has no workload-mount fields. The controller derives file paths
from the fixed convention below and supplies them to rendering. It retains
selected-key projection requirements in an internal plan for future integration.
This milestone neither adds mounts to Policy nor introduces a new binding resource.
The generated path need not exist in a running container for this control-plane
artifact to be valid. Actual projection must later honor the same path contract.

Related boundaries: [API-key mapping #52](https://github.com/praxis-proxy/gateway-controller/issues/52),
[shared policy wiring #51](https://github.com/praxis-proxy/gateway-controller/issues/51),
[AIGateway API #34](https://github.com/praxis-proxy/gateway-controller/issues/34),
[AIGateway reconciliation #35](https://github.com/praxis-proxy/gateway-controller/issues/35)
and [core control plane #11](https://github.com/praxis-proxy/gateway-controller/issues/11).
These identify responsibilities, not current completion claims. The unfinished
core workload path is a future consumer, not a prerequisite for Policy generation.

## Delivery sequence

1. **Contract and inputs — S01–S03:** establish the AI Gateway input baseline,
   define the package and trusted-context contracts, add the flat auth API/Tenant
   grant handling, and discover packages generically.
2. **Default rendering — S04–S06:** create the API-key chart, resolve Secret
   references, derive fixed paths and group shared projections, then render
   through Helm validation. S05a replaces the earlier per-instance allocation.
3. **Admission assets and packaging — S07–S09:** provide a shared default
   AIGateway VAP/binding with parity tests and administrator instructions, remove
   controller admission ownership, and ship the default template package.
4. **Policy output — S11–S12:** assemble the document and reconcile exactly one
   gateway-owned core Policy from authorized inputs.
5. **Lifecycle and completion — S14, S16:** test changes and errors at the Policy
   boundary and document the Tenant + Secret + AIGateway to Policy workflow.

S10 (core file projection), S13 (listener attachment) and S15 (live gateway tests)
remain explicit deferred integration tasks. Their IDs are retained for tracking;
no current task depends on them. Checkpoints in the checklist assess only the
active control-plane deliverable.

## Contracts to preserve

- Flat auth entries use reserved `name` and `authType`, with package-owned fields
  such as `header`, optional `prefix`, `keySource.secretRef`, `refreshSeconds` and
  `maxStalenessSeconds`. Preserve arbitrary JSON for future types. Tenant grants
  match auth type IDs exactly, including case.
- Discover `aigateway/governance/auth/<authType>/` from default and extension roots
  at startup. Reject duplicate IDs and incomplete packages; use no separate
  registry or auth-specific switch. Default packages are controller-maintained.
- The project provides a shared AIGateway VAP and enforcing binding for default
  auth types, initially `apiKey`. Platform administrators install these resources
  and extend the VAP with rules supplied by extension owners. The controller does
  not manage or check readiness of cluster admission resources. Helm validation
  and Tenant grants are sufficient controller gates for now; shared cases check
  admission/render parity.
- Packages declare Secret input locations. The controller checks same-namespace
  access and data-key existence, derives fixed paths and supplies trusted
  context. Templates cannot override that context or create workload bindings.
  Secret contents stay out of rendering, policy documents, status and logs.
- The internal plan separates per-slot template paths from shared Secret
  projections. Templates specify neither filenames nor filesystem layouts.
  Verifier Secrets remain distinct from upstream credentials.
- The default emits `identity/api-key` with `provider.kind: file`, the resolved
  path and refresh/staleness settings. Map `user` to `subject.id` and `groups` to
  `subject.roles`. The assembler adds global authentication,
  `require(authenticated)` and caller-header stripping to the same document.
- A route-authorization fixture verifies future composition without template
  placeholders or production Model CEL support. Existing model contributions,
  where available, must not be overwritten by the auth generator.
- Invalid input or lost permission cannot generate a new valid-looking fallback
  Policy with authentication removed. Reconcile owned output and input errors
  consistently; Policy generation and AIGateway serving readiness remain distinct.

## Secret path and projection convention

The controller derives each path directly from the validated Secret name and key:

```text
/var/run/praxis/auth/secrets/<secret-name>/<data-key>

consumer-keys / keys.yaml
  -> /var/run/praxis/auth/secrets/consumer-keys/keys.yaml
```

Paths do not depend on auth type, instance/slot names, object UIDs or Secret
contents. The same Secret/key resolves to the same file for every consumer in
the workload; different keys retain their own filenames. No separate mount list
is required in the AIGateway API.

For future workload integration, group references by namespace and Secret name.
Each group has one read-only directory mount containing the union of referenced
keys, in deterministic order. Unreferenced keys are excluded. Volume names are
controller-generated Kubernetes identifiers; they do not determine file paths.
The reserved directory belongs to the controller. Reject incompatible volumes
or overlapping mounts rather than relocating files; repeated references to the
same controller-managed projection are valid sharing.

This contract assumes one source namespace per workload, matching the gateway's
namespace. Combining namespaces in one workload is outside this design. Mount
directories without `subPath` so Secret updates can propagate eventually; PPE's
reload and staleness handling remain separate. Content changes and same-name
Secret replacement retain paths, without claiming immediate runtime updates.
A future ConfigMap source uses a separate `configmaps` directory.

## Contract decisions and remaining lifecycle work

The API-key package defaults are `X-API-Key`, a 5-second refresh and a 30-second
staleness ceiling, with staleness at least refresh. These are explicit package
defaults, checked against the targeted PPE contract. No Helm CLI is assumed in
the controller image, and Secret contents are never template inputs.

The [package contract](package-contract.md) records the chosen reference file,
startup snapshot, separate `entry`/`context` values and structured contribution.
Helm v3.22.1 APIs and the targeted PPE source revision have been inspected. S04
introduces the SDK for chart tests, and S06 adds the production renderer. S07
provides the shared default AIGateway VAP as administrator-installed manifests;
S08 removes the earlier per-package admission requirement. These are new APIs;
existing-resource migration and backward compatibility are outside this work.

S14 defines the disposition of an existing owned Policy when its Tenant grant or
Secret reference becomes invalid.
Tests must distinguish publication of current desired output from the future
runtime's response to Policy removal or changes. Do not claim runtime access
withdrawal in this milestone.

## Verification and definition of done

Unit tests cover JSON preservation, catalog discovery, schema validation,
reference resolution, fixed paths, shared projections, collisions, rendering and
assembly. The generated PPE YAML must contain the expected plugin/header,
file path, mapping, intervals,
`on_error: fail`, global authentication and credential stripping. It must contain
no Secret bytes. Core YAML acceptance alone is not enough to prove those fields.

Integration tests follow [test/README.md](../../../test/README.md) and run the real API
server. They create Tenant, Secret and AIGateway inputs; install the supplied VAP
manifests directly and verify rejection; exercise Helm validation and reference
failures; and read back the owned Policy from the core API. Check repeat
reconciliation, input updates,
ownership conflicts, dependency loss/recovery and cleanup. Run the existing core
Policy controller to observe acceptance without manually writing its status.

Completion requires all of the following:

- A valid, authorized input produces exactly one correctly owned core Policy.
- The Policy passes core API validation and the core controller's current checks;
  focused tests establish the generated PPE configuration's expected structure.
- Invalid input, missing Secret/key, denied grants, failed rendering
  and unsupported auth combinations do not produce partially generated output.
- Paths follow the fixed convention; repeated references share paths/projections
  and only selected keys appear in the internal projection plan. No actual Pod,
  mount or Proxy instance is required for this proof.
- Changes reconcile idempotently, Secret contents remain confidential, and Policy
  generation is not reported as a serving-ready AIGateway.
- The documented control-plane example and relevant unit/integration/build/lint
  checks pass with generated outputs included.

Commands verified for this plan are `make test-unit`, `make test-integration`,
`make lint`, `make lint-extra`, `make build`, `make generate` and `make manifests`.
A live gateway e2e suite is not a completion gate for this milestone. `make verify`
and `make test` invoke Git internally and require permission before execution.
No Git commands are run by this planning change.

## Deferred integration and broader providers

- S10, S13 and S15: core Secret projection, Proxy/filter-chain attachment, serving
  readiness, runtime rotation/staleness, grant withdrawal and live traffic tests.
- Multiple auth instances and general OR selection, credential precedence,
  conflicting identities and mixed provider failures.
- Deployed mounted-extension proof and MaaS HTTP validation; generic fixture
  coverage remains active. OIDC and TokenReview packages are follow-up work.
- Production Model CEL/metadata authorization and subscription lookup, ConfigMap
  references and live catalog reload.


---

# Completed work: Proxy basic end-to-end coverage

## Scope

Exercise a real Proxy through the deployed controller and Praxis runtime in a dedicated kind cluster. Existing controller changes are committed; do not modify unrelated local experiments.

## Acceptance criteria

1. Create a fresh namespace and an inline static-response Proxy. Assert its ConfigMap, Deployment, Service and Ready status, then read its response through the Kubernetes Service proxy.
2. Edit the inline response. Wait for actual traffic to change while Deployment pod template, pod UIDs and container restart counts stay unchanged.
3. Change the listener port. Wait for a new pod set and successful response on the updated Service port.

## Decisions

- Use existing Ginkgo/client-go dependencies. Kubernetes Service proxy avoids an extra curl image; it tests Service backend selection and the running runtime, not ClusterIP routing through kube-proxy.
- Define and document an explicit test image setting; pin the CI runtime by verified digest and allow a deliberate local override.
- ConfigMap projection is asynchronous: use bounded eventual checks with sufficient propagation time.
- No skip on missing prerequisites, no silent image fallback. Clean up test namespaces and isolate kubeconfig from existing clusters.

## Verification

Compile the e2e suite; run it on a dedicated cluster; lint with the repository-pinned linter; run unit tests and vet/build. Review assertions against transient reload and rollout observations.

## Results

- Dedicated kind cluster gateway-controller-dev on Kubernetes1.37.0; isolated kubeconfig /tmp/gateway-controller-pr82-e2e.kubeconfig. Original default context remains kind-kserve-integration.
- Full race-enabled e2e suite: 4/4 specs passed, no skips. New Proxy scenario completed in101seconds, including asynchronous projection and rollout.
- Runtime image: Praxis0.5.6 pinned to verified Linux/amd64 manifest digest in Makefile; execution worked under local ARM emulation.
- Repository-pinned golangci-lint2.15.0: zero issues. Compile, gopls and go vet for e2e passed.
- Namespace cleanup succeeded; dedicated controller cluster remains available. No Git commands, commits, pushes or public posts made for this task.
