# Extensible gateway authentication and model authorization

Status: Proposed.

Related work: [gateway authentication](https://github.com/praxis-proxy/gateway-controller/issues/49)
and [API-key authentication mapping](https://github.com/praxis-proxy/gateway-controller/issues/52).

Worked example: [MaaS external API-key authentication](mass-auth-example.md).

An AIGateway combines authentication and model authorization in one policy
document. This proposal refines the [v1alpha1 API design](../api-v1alpha1.md),
replacing fixed authentication configurations with package-defined fields.

## Use case

A Tenant administrator needs authentication across inference and MCP on a shared
listener, with model access rules based on the authenticated caller's identity.
Platform administrators can add auth types by mounting template packages, without
controller code changes or an image rebuild.

- The platform makes auth packages available.
- The Tenant permits types, applying the same allowlist to defaults and extensions.
- Each gateway selects and configures a subset of its Tenant's permitted types.

## Authentication configuration

Each entry has two reserved common fields: `name` identifies an instance and
`authType` selects its package. Package-specific fields appear directly on the
entry, without a `properties` wrapper.

For example, these Tenant and AIGateway specification excerpts allow API-key and
OIDC authentication, then configure an API-key instance:

```yaml
# Tenant.spec
authorizedIdentities:
  - type: apiKey
  - type: oidc
```

```yaml
# AIGateway.spec
governance:
  authentication:
    - name: consumers
      authType: apiKey
      header: X-API-Key
      keySource:
        secretRef:
          name: consumer-keys
          key: keys.yaml
      refreshSeconds: 5
      maxStalenessSeconds: 30
```

Instance names are unique within a gateway, allowing multiple instances of one
type. Tenant type IDs are open strings rather than a built-in enum.

The API preserves package-specific JSON fields through storage and reconciliation.
Each package validates the complete entry, including required settings, types and
unknown fields, while preserving the meaning of the common fields.

Type IDs such as `apiKey`, `oidc` and `companyAuth` identify stable input contracts.
Package releases have independent versions; incompatible input or behaviour
changes require an explicit migration.

### Two validation layers

1. **Admission validation:** the project provides a shared AIGateway
   ValidatingAdmissionPolicy (VAP) and enforcing binding for default auth types.
   The platform administrator installs these cluster-scoped resources and extends
   the VAP with the template owner's validation rules when enabling a custom type.
2. **Render validation:** the controller renders with the package's
   `values.schema.json` validation enabled, plus template checks, before policy
   publication. This runs even when admission has already validated the resource.

VAP rules use CEL; they do not execute Helm or its schema. Default and extension
owners test their admission rules against the same inputs as Helm validation.
A tool or script can help template authors check that their VAP rules and JSON
Schema accept and reject the same test inputs, identifying validation drift.

The controller does not install, watch or check readiness of admission resources.
Tenant grants and Helm validation are sufficient controller gates for now.
Reference authorization and runtime credential validation remain separate checks.

## Template discovery and extension

Templates are organised by resource, governance concern and authentication type:

```text
<default-root>/
  aigateway/governance/auth/
    apiKey/
      Chart.yaml
      values.schema.json
      README.md
      templates/
        contribution.yaml
    oidc/
      ...

<extension-root>/
  aigateway/governance/auth/
    companyAuth/
      ...
```

Default packages ship with the controller. Extensions use a separate mount root
so they do not hide defaults. At startup, the controller builds one catalog from
both roots, keyed by auth type directory name; no separate registry is needed.

The AI Gateway Controller project maintains default templates, schemas, admission
policies, documentation and compatibility with supported runtimes. Defaults are its
regularly tested path, covered by integration and end-to-end tests.

Extension owners maintain, document and test their packages, including identity
mapping and compatibility with their controller/runtime versions. The controller
project tests the shared discovery, validation and policy composition contract.

Each package provides release metadata, an input schema and documentation of
settings, identity mapping and runtime requirements. The controller rejects
catalogs with duplicate type IDs or incomplete packages. Admission resources are
managed separately by the platform administrator and are not required package
artifacts. Unavailable types cause gateway configuration errors.

Package changes require a controller rollout in the first version. Gateway
authors select an auth type and configure its settings in the AIGateway resource.
Packages configure existing PPE capabilities; new runtime mechanisms also need
data-plane support.

## One gateway policy document

Auth packages contribute PPE identity plugins, activation steps and requirements
such as stripping credential headers. The shared assembler combines them with a
gateway-wide authenticated-caller requirement and routes for exposed models. It
rejects conflicts and publishes only complete documents.

Authentication uses OR semantics: any configured method can authenticate the
caller. Requests with no valid credential are rejected, and roles or claims from
unrelated principals must not be combined.

Invalid configuration, missing references or denied Tenant permission must not
silently remove authentication. The gateway reports the failure, and changes to
Tenant grants trigger reassessment of affected gateways.

### Secret files

Packages declare which input fields reference Secret data entries, allowing
custom reference fields without auth-specific controller code. Before rendering,
the controller validates same-namespace references and selected keys, then derives
each file path using a fixed convention:

```text
/var/run/praxis/auth/secrets/<secret-name>/<data-key>
```

For example, `consumer-keys` with key `keys.yaml` resolves to
`/var/run/praxis/auth/secrets/consumer-keys/keys.yaml`. References to the same
Secret/key share the same path, independent of auth type or instance name.
The controller supplies resolved paths as trusted template context. Packages
neither choose filesystem layouts nor generate volumes or mounts.

The workload uses one read-only directory mount per referenced Secret, projecting
only the keys required by its consumers. The controller combines repeated
references, chooses volume names and rejects conflicts with other mounts. This
convention assumes one source namespace per workload, matching the gateway's
namespace; cross-namespace workload sharing requires a separate design.

The `apiKey` default reads verifier records (key hashes and trusted identity
attributes) from a same-namespace Secret. A directory mount allows record updates
to propagate eventually, without `subPath`; PPE separately reloads the file.
Secret content changes and same-name replacement leave paths unchanged. Templates
receive paths, never Secret contents, and those contents stay out of policy documents.

The first version supports `secretRef`. A future `configMapRef` can use the same
model under a separate `configmaps` directory.

## Adding model authorization

Model authorization adds checks against the identity established by gateway
authentication. For example, a Model specification can add a CEL expression:

```yaml
publishedName: alpha-chat
governance:
  authorization:
    cel: "'alpha-model-users' in subject.roles"
```

The assembler places the expression on the model's route and adds its required
PDP configuration. The resulting document could contain:

```yaml
engine_settings:
  dispatch: policy

plugins:
  - name: auth-consumers
    kind: identity/api-key
    hooks: [identity.resolve]
    on_error: fail
    config:
      credential:
        kind: header
        name: X-API-Key
      provider:
        kind: file
        path: /var/run/praxis/auth/secrets/consumer-keys/keys.yaml
        refresh_secs: 5
        max_staleness_secs: 30
      record_map:
        subject:
          id: user
          roles: groups

global:
  authentication: [auth-consumers]
  authorization:
    pre_invocation:
      - "require(authenticated)"
  assertions:
    request:
      strip: [X-API-Key]
  pdp:
    - kind: cel
      on_error: deny

routes:
  - llm: alpha-chat
    authorization:
      pre_invocation:
        - cel:
            expr: "'alpha-model-users' in subject.roles"
```

Here, `user` becomes `subject.id` and `groups` becomes `subject.roles`. Packages
document their normalized identity fields and any additional `claim.*` values;
rules cannot assume every source provides the same claims.

The caller must pass gateway authentication and all applicable gateway and model
checks. Models are selected by published name before any upstream-name rewrite;
requests for unexposed models are rejected. Authorization is assembled centrally,
so auth templates need no model authorization placeholders or changes when later
authorization support is added.

## Delivery scope and remaining decisions

The first delivery covers the extensible API, additive package discovery and the
`apiKey` default. OIDC and Model CEL authorization follow the same package and
assembly boundaries in subsequent work.

Before completing the design, resolve:

- How PPE selects alternative methods when several credentials are supplied,
  identities conflict or an unused method's provider is unavailable.
- Whether an exposed model without an authorization expression permits every
  authenticated caller or requires an explicit access rule.

## References

- [PPE identity resolution and policy scopes](https://github.com/praxis-proxy/policy/blob/main/docs/content/guides/identity-delegation.md)
- [PPE CEL decision points](https://github.com/praxis-proxy/policy/blob/main/docs/content/apl/pdp.md)
- [PPE model routes](https://github.com/praxis-proxy/policy/blob/main/docs/content/guides/llm-routes.md)
- [Helm values schema validation](https://helm.sh/docs/topics/charts/#schema-files)
- [Kubernetes ValidatingAdmissionPolicy and bindings](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/)
- [Kubernetes Secret file projection and updates](https://kubernetes.io/docs/concepts/configuration/secret/#using-secrets-as-files-from-a-pod)
- [Prometheus Operator Secret mount convention](https://github.com/prometheus-operator/prometheus-operator/blob/main/pkg/prometheus/common.go)
