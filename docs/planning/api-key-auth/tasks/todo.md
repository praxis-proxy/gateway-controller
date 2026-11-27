# Gateway authentication implementation

**Goal: create and maintain one valid, gateway-owned core `Policy` resource from
an authorized AIGateway's single `apiKey` instance and referenced Secret.**
[Plan](../plan.md) · [Design](../../../design/gateway-auth/gateway-authentication.md).

This milestone ends at the core Policy API. Proxy creation, actual mounts,
filter-chain attachment, serving readiness and live traffic tests are deferred.
Secret paths and projection requirements remain controller-owned internal data;
the current Policy API does not carry workload mounts. Its YAML acceptance is
checked alongside focused tests of the generated PPE fields.

Task IDs are retained: S10, S13 and S15 have moved to deferred core integration.
No active task depends on those tasks. Keep authored changes around 3–5 files,
splitting before coding where needed; include generated output with its source.

Completed items record verified work on the implementation branches; this design
branch does not apply those code changes. S05a replaces the earlier per-instance
file allocation with the fixed Secret convention.

## S01 — Confirm the integration baseline and package contract

- [x] Locate the Tenant/AIGateway input and reconciliation baseline and the core
  Policy API/controller. Record the Policy generation integration points; core
  workload creation and filter-chain wiring are not prerequisites for this milestone.
- [x] Define package Secret-reference declarations, the trusted resolved-path
  context and structured policy contribution. Select and verify the pinned Helm
  Go rendering API, with schema validation enabled and no external CLI assumption.
- [x] Confirm API-key defaults and record the targeted PPE contract revision.
- [x] Define exact, case-sensitive Tenant grants within the single-instance scope.
- [x] Assign cluster admission ownership to platform administrators; keep
  controller checks limited to Tenant grants, Helm validation and references.

Source/API integration points and the rendering contract are recorded in
[the package contract](../package-contract.md). These are new APIs; no migration
or backward-compatibility work is required. The controller does not manage or
check readiness of admission resources. S07 provides the default VAP; S08 removes
the earlier catalog admission requirement.

**Verification:** inspect source and official library/runtime contracts; review a
worked flat entry, reference declaration, resolved context and resulting policy.
**Dependencies:** none. **Likely areas:** [the plan](../plan.md) and a short package author
contract alongside the design. **Size:** small investigation/documentation slice.

## S02 — Add the flat auth envelope in the AI Gateway layer

- [x] Preserve the flat `name`/`authType` envelope and arbitrary nested JSON
  through serialization, CRD storage and read-back. Instance names are unique;
  auth types remain open rather than an enum of built-ins.
- [x] Resolve Tenant authorization by exact type ID. Keep upstream credential
  grants separate.
- [x] Generate the CRD/deepcopy output and use API-server validation for structural
  constraints. Reject more than one configured auth instance before publication
  using a runtime capability check, while keeping the CRD list extensible.

**Verification:** unit serialization tests and `make test-integration` prove
unknown fields survive create/get/update and invalid envelopes/grants are handled
as specified. **Dependencies:** S01's input contract and the AI Gateway API baseline.
**Likely areas:** Tenant/AIGateway types, focused API tests and generated files.
**Size:** medium; split Tenant grant handling from envelope work if required.

Implemented with generated schemas/deepcopies and matching samples. Verified
with `make test-unit` (race), all 76 envtest specs, `make lint`, `go build ./...`
and gopls. Tests cover Kubernetes conversion, DeepCopy isolation, create/get/update,
status patches, exact grants and the single-instance limit. Removing the reserved
field guard makes its collision tests fail.

## S03 — Discover auth packages generically

- [x] Build a startup catalog from default and extension roots under
  `aigateway/governance/auth/<authType>/`, keyed by directory name.
- [x] Validate metadata, schema and reference declarations; reject duplicate IDs
  and incomplete packages without overriding defaults.
- [x] Exercise a second package fixture with different input/reference fields to
  prove discovery needs no auth-specific switch, enum or registry file.

**Verification:** hermetic filesystem tests include projected-volume layouts,
missing artifacts, malformed metadata and collisions. **Dependencies:** the
discovery contract from S01; Tenant grants and cluster admission are outside
the filesystem loader.
**Likely areas:** catalog module, tests and package fixtures. **Size:** medium.

Implemented in `internal/auth`. Verified with the API/controller race suite,
`make lint`, `go build ./...`, `go vet ./internal/auth` and `gopls check`.
Tests include real projected-volume symlinks, independent file snapshots,
alternate reference fields and rejection of malformed/duplicate packages.
Review also verified that removing duplicate detection makes its test fail.
Startup wiring remains S09; schema compilation and rendering remain S06.

## Checkpoint C1 — Inputs and generic discovery are defined

- [x] S01–S03 checks pass on the AI Gateway baseline; the core Policy is the
  agreed output and no workload implementation is required to generate it.

## S04 — Create the API-key chart and schema

- [x] Add the rendering assets under `templates/aigateway/governance/auth/apiKey/`:
  chart metadata, flat input schema, reference declaration and contribution template.
- [x] Configure the selected header, optional prefix, controller-supplied file
  path, refresh/staleness and `user`/`groups` identity mapping. The chart neither
  chooses mount paths nor creates the complete policy document.
- [x] Enforce required Secret name/key, field types, unknown-field rejection and
  interval constraints with the agreed defaults. The package does not require
  admission artifacts; S07 adds independent cluster admission validation.

Carry forward the former fixed-CRD cases: refresh `0`, staleness `-1`, Secret key
`keys/api`, an empty Secret key and the 5/30-second defaults. These belong to
the package schema and S07 admission parity, rather than the generic auth envelope.

**Verification:** focused schema/template cases cover defaults and overrides,
malformed fields and changed supplied paths. **Dependencies:** S01.
**Likely areas:** default chart rendering assets and focused cases. **Size:** medium;
separate schema and template tests if the authoring surface exceeds five files.

S04 introduces the pinned Helm Go SDK for chart tests. S06 adds the production
renderer.

Verified with real Helm rendering through YAML and JSON-number inputs, the full
unit race suite, all 76 envtest specs, lint, build and gopls. Tests cover defaults,
explicit nulls, unknown fields, exact prefix/path quoting and integer intervals
through `2^53 - 1`. Removing the staleness guard makes six cases fail. No Secret
contents or workload bindings enter the contribution.

## S05 — Resolve verifier Secrets

- [x] Resolve package-declared references in the gateway namespace, check Tenant
  access and selected key existence, and return a typed plan without Secret bytes
  in rendering context, diagnostics, status or policy data.
- [x] Resolve generic JSON pointers without hardcoded `keySource` traversal.
  Restrict references to `secretRef`; reject namespace overrides and malformed
  references before reading Secrets.
- [x] Cover current Tenant grants, missing Secrets/keys and safe read errors;
  return no partial result when a dependency cannot be resolved.

**Verification:** resolver unit tests plus API-server reference tests; assert
authorization, metadata-only results and explicit errors before rendering.
**Dependencies:** S01; gateway reference integration also needs S02.
**Likely areas:** reference resolver, authorization helper and focused tests.
**Size:** medium.

Verified on the API-key implementation branch with the full unit race suite,
all 79 envtest specs, lint, build and gopls. Its earlier allocator uses per-instance
hashed directories; S05a remains required before completing the rendering contract.

## S05a — Derive fixed paths and group shared Secret projections

- [ ] Replace per-instance path allocation with
  `/var/run/praxis/auth/secrets/<secret-name>/<data-key>`. Preserve each data key
  as its filename; auth type, instance/slot names, UIDs and Secret contents do not
  affect paths. Packages cannot supply filename or layout requirements.
- [ ] Separate per-slot paths from projection requirements. Group by namespace
  and Secret name, with one intended read-only directory mount and the sorted
  union of referenced keys. Repeated references share files; unreferenced keys
  stay excluded. Volume names remain valid controller-generated identifiers.
- [ ] Reject incompatible volume names and reserved/overlapping paths without
  relocating files. Accept repeated references to the same projection. Keep
  resolution confined to the gateway namespace and actual mounts deferred.

**Verification:** unit tests cover exact paths, shared slots and instances,
distinct Secrets/keys, long Secret names, valid data-key filenames, reordered
references, selected-key unions and collisions.
Content changes, instance/slot renames and same-name Secret replacement leave
paths unchanged; changing Secret name/key changes the path. Update API-server
reference tests to assert the new convention and continued safe failure behaviour.
These fixtures do not enable multiple-instance authentication.
**Dependencies:** S05. **Likely areas:** file planner, reference plan and focused
unit/integration tests. **Size:** medium; split planner changes from caller updates
if needed.

## S06 — Render and validate the default through Helm

- [ ] Invoke the generic renderer with the flat entry and controller-resolved
  context; enable Helm schema and template validation before decoding output.
- [ ] Validate the policy contribution and reject workload bindings, global policy
  replacement and attempts to override trusted context. Never fetch Secret
  contents from a template.
- [ ] Render the real API-key chart and a differently shaped package fixture
  through the same renderer. Assert the supplied fixed Secret path is used;
  shared file references do not merge plugin configuration or identity. No schema
  bypass or API-key-specific dispatch exists.

**Verification:** `make test-unit` with schema rejection cases, supplied-path
changes and malformed contributions. Rendering does not depend on cluster
admission installation or readiness.
**Dependencies:** S03–S05, S05a. **Likely areas:** renderer/contribution modules, tests
and pinned module dependencies. **Size:** medium; separate dependency setup if needed.

## Checkpoint C2 — Secret references become valid plugin configuration

- [ ] S04–S06, including S05a, pass: templates consume fixed resolved paths,
  shared references produce one projection per Secret, and no Secret bytes or
  template-generated workload bindings appear in the contribution.

## S07 — Provide the shared default AIGateway VAP

- [ ] Supply a shared AIGateway ValidatingAdmissionPolicy and enforcing binding
  as standalone cluster manifests, initially validating `apiKey` entries. Cover
  required references, field types, unknown fields, prefix/header rules and
  refresh/staleness defaults, bounds and relationships.
- [ ] Scope admission to the intended AIGateway operations/auth type, leaving
  unrelated types and status updates unaffected.
- [ ] Test VAP and Helm validation against shared valid/invalid cases, including
  omitted/null values and whole-number representations. Supply administrator
  installation instructions and an example of extending the VAP for a custom
  auth type using rules maintained by its template owner.

**Verification:** integration tests install the supplied manifests directly and
observe actual API-server admission denial. Helm parity cases agree, including
multiple-entry shapes independently of first-milestone runtime support. The
controller neither installs the resources nor waits for them.
**Dependencies:** S02, S04. **Likely areas:** shared admission manifests, operator
documentation, parity fixtures and integration tests. **Size:** medium.

## S08 — Remove controller ownership of cluster admission

- [x] Remove mandatory per-package admission manifests and admission validation
  from catalog discovery. No controller admission RBAC, watches or readiness
  checks are introduced.
- [x] Prove packages can be discovered and rendered without admission resources;
  keep Helm validation and Tenant authorization independent of cluster admission.

**Verification:** catalog and real-chart tests pass without per-package VAPs;
admission-only artifacts cannot block discovery. **Dependencies:** S03, S04.
**Likely areas:** catalog and focused tests. **Size:** small.

Removed the admission parser and manifest requirement. Catalog tests cover
missing/unrelated admission files and successful discovery of the default chart.

## S09 — Ship the default and wire startup discovery

- [ ] Include the complete `apiKey` package in the controller image and load the
  catalog for the AI Gateway controller path.
- [ ] Define and test startup configuration before using it in deployment
  manifests. Keep default and extension roots separate and compatible with
  non-root/read-only operation.
- [ ] Surface catalog failures and pass discovered packages into
  gateway reconciliation. Package changes require a rollout.

**Verification:** configuration tests and image/startup smoke checks show the
bundled default is discovered without an installed Helm CLI or controller access
to admission resources. **Dependencies:** S03, S06, S08 and the AI controller baseline.
**Likely areas:** manager wiring, Dockerfile, deployment config and tests.
**Size:** medium; split image packaging from startup integration if needed.

## Checkpoint C3 — The default package and administrator admission assets are ready

- [ ] S07–S09 checks pass: the default package ships with the controller, Helm
  validates rendering, and separately installed VAP/binding manifests reject
  invalid default-type inputs. Tenant grants remain required.

## S11 — Assemble one policy for the API-key instance

- [ ] Combine the identity contribution, global authenticated-caller requirement
  and credential stripping in one complete document with deterministic names.
- [ ] Retain existing model routes and reject conflicts; an auth contribution
  cannot replace controller-owned global enforcement.
- [ ] Compose an independent model-authorization fixture in that same document
  without editing the auth template or implementing the production Model CEL API.

**Verification:** unit assembly tests preserve the auth requirement and mapped
identity contract while adding route/PDP contributions. Assert the generated
PPE settings against the targeted contract; live enforcement is deferred with S15.
**Dependencies:** S01, S06.
**Likely areas:** shared assembler, tests and concrete contribution fixtures.
**Size:** medium. General OR/precedence is not implemented by this task.

## S12 — Generate the gateway-owned Policy from authorized inputs

- [ ] Resolve the gateway's ready Tenant, require an available and permitted
  `apiKey` type, then resolve the Secret and derive fixed paths before invoking Helm.
- [ ] Assemble the document and grouped selected-key projection requirements
  before writes;
  create/update exactly one gateway-owned `praxis.io/v1alpha1 Policy` with the
  generated YAML in `spec.document`, without adopting user-owned resources.
- [ ] Report denied permission, unavailable package, invalid input or unresolved
  references through the gateway's condition contract with current generation
  and actionable reasons. Reject unsupported combinations before publication;
  creating a Policy must not make the AIGateway report serving readiness.

**Verification:** envtest creates Tenant + Secret + AIGateway, reads back the core
Policy and checks its owner and generated document. Run the core Policy controller
and verify its acceptance independently of AIGateway serving readiness. Cover
repeat/update, reference/permission failures and no partial output; no Proxy
instance or workload is required.
**Dependencies:** S02, S05a, S09, S11 and the AIGateway reconciliation baseline.
**Likely areas:** gateway desired-state generation and focused integration tests.
**Size:** medium; S13 is deferred and does not block Policy generation.

## Checkpoint C4 — The controller outputs a valid core Policy

- [ ] S11–S12 checks pass: exactly one owned Policy is read back from the core
  API, passes its current checks and contains the expected PPE configuration.
  Its creation is not reported as a serving-ready AIGateway.

## S14 — Reconcile Policy output when inputs change

- [ ] Watch Tenant grants, Secret references/data and owned
  Policies; reconcile affected outputs without an AIGateway spec edit.
- [ ] Define and test the disposition of an existing owned Policy when permission
  or a required reference is lost. Never publish a fallback document with auth
  removed, and do not claim that a status change withdraws runtime access.
- [ ] Handle reference/instance changes, Policy deletion and input loss/recovery
  idempotently. Preserve user-owned resources and keep serving readiness separate
  from successful output generation. Content-only Secret updates must not embed
  verifier data or cause unnecessary Policy spec changes when its inputs are unchanged.
  Rebuild the selected-key plan from current references; removing a consumer must
  retain files still referenced by another consumer.

**Verification:** event-mapping tests and envtest cover generation, loss, recovery,
ownership and cleanup at the Policy boundary. Verify unchanged desired Policy
specs remain stable; actual file reload/traffic revocation stays in deferred S15.
**Dependencies:** S12. **Likely areas:** event/lifecycle handling and focused
integration tests. **Size:** medium.

## S16 — Document and verify the Policy-generation workflow

- [ ] Supply Tenant, verifier Secret and AIGateway examples that produce the
  expected core Policy. Explain package defaults, optional prefix and identity mapping.
- [ ] Explain administrator installation/extension of the shared VAP, Helm
  validation, the fixed Secret path convention, selected-key sharing and
  error feedback. State that mounts, filter-chain attachment and runtime enforcement
  await core integration; a generated Policy does not establish gateway readiness.
- [ ] Run the documented control-plane workflow and relevant unit/integration,
  build and lint checks. Include generated artifacts and the verified Policy output.

**Verification:** apply the inputs against the integration API server and read back
exactly one owned, accepted Policy with the expected PPE configuration. No running
gateway is required. Use source-verified fields/options and obtain permission for
Git-backed checks. **Dependencies:** S12, S14. **Likely areas:** default README,
control-plane guide and samples. **Size:** medium.

## Checkpoint C5 — The control-plane delivery is complete

- [ ] The documented inputs generate and maintain the accepted Policy, lifecycle
  and error cases pass, and relevant build/lint/unit/integration checks are complete.
  No Proxy resource, actual Secret mount or live traffic proof is required here.

## Deferred core integration

The following tasks consume the generated Policy once the core workload API and
reconciliation path are ready. They are not completion gates for this delivery.
Serving readiness, access withdrawal and runtime validation claims belong here.

### S10 — Project selected Secret files through the core workload path

- [ ] Add or reuse the minimal typed same-namespace Secret file-projection support
  between the AI Gateway generation path and backing Proxy/workload reconciler.
- [ ] Turn the S05a plan into one read-only directory mount per referenced Secret
  at `/var/run/praxis/auth/secrets/<secret-name>/`, projecting only the selected
  keys under their original names. Combine shared references without duplicate
  volumes/mounts; use no `subPath` or Secret bytes in generated configuration.
- [ ] Validate collisions, reject invalid projections and preserve update
  propagation. Keep existing upstream credential/environment handling separate.

**Verification:** core API/renderer tests and envtest inspect generated mounts,
selected-key unions, reference changes and matching policy paths. Removing one
consumer retains keys still in use. Remove a key after its last reference, and
remove the mount when no keys remain. Assert the workload uses the gateway's
namespace; combining source namespaces needs a separate design.
**Dependencies:** S01, S05a and the core workload baseline.
**Likely areas:** core API, projection renderer, validation and
tests, plus generated output. **Size:** split into API/validation and workload
rendering slices if more than five authored files are required.

### S13 — Attach policy and file projections to the shared listener

- [ ] Feed the owned Policy and controller-generated projections through the
  existing gateway-to-Proxy path, with an unconditional policy filter before
  model rewrites and provider credential injection.
- [ ] Coordinate policy/workload publication so the listener is not exposed
  without its required security configuration or verifier files.
- [ ] Follow backing Policy/Proxy readiness for the intended generation; do not
  equate a created resource or an available pod with an enforced configuration.

**Verification:** integration tests assert the filter chain, mounts, ownership,
publication failure/recovery and readiness reasons. **Dependencies:** S10, S12
and the existing gateway/core publication path. **Likely areas:** policy wiring,
projection handoff, readiness and focused tests. **Size:** medium.

### S15 — Prove Secret-backed API-key enforcement on a running gateway

- [ ] Through the deployed controller, declare Tenant + Secret + AIGateway and
  show valid keys pass while missing, invalid and expired keys fail across
  inference/MCP on the shared listener. Verify header stripping and mapped identity.
- [ ] Rotate/remove verifier records and exercise malformed-file reload plus the
  staleness ceiling. Allow for projection delay, use bounded eventual assertions
  and record the supported Praxis/PPE version used.
- [ ] Demonstrate Tenant grant withdrawal and recovery from traffic, and verify
  prerequisite errors do not create an unprotected listener. Clean up the scenario.

**Verification:** `make test-e2e` using the existing isolated Kind setup and a
functional upstream. Envtest or successful rendering is not a substitute. Secret
deletion alone does not prove the old projected file disappeared. **Dependencies:**
S07, S13, S14. **Likely areas:** API-key e2e scenario, runtime fixtures and harness
integration. **Size:** split initial access/header assertions from rotation and
withdrawal scenarios if necessary to keep each review bounded.

## Deferred follow-up work

- [ ] Prove and implement multiple-instance OR selection, header precedence,
  conflicting-identity handling and mixed provider-failure behaviour.
- [ ] Demonstrate a mounted production extension without rebuilding, including
  the [MaaS HTTP validator](../../../design/gateway-auth/mass-auth-example.md).
  Generic discovery/rendering fixture coverage remains in this delivery.
- [ ] Add OIDC and Kubernetes TokenReview packages.
- [ ] Implement production Model CEL and external metadata/subscription
  authorization, including the model-without-an-access-rule decision.
- [ ] Add ConfigMap references and, if required, live catalog reload.


---

## Completed work: Proxy e2e tasks

- [x] Implement basic create/traffic, hot reload and listener rollout scenario.
- [x] Supply verified runtime image, isolated cluster and reproducible CI/local setup.
- [x] Execute the live e2e and resolve any observed failures.
- [x] Run required checks and review changes.
