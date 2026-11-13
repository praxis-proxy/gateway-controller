# AI Gateway API design — v1alpha1

Status: Draft.

API group/version: `aigateway.praxis.io/v1alpha1`.

Source: [AIGateway API proposal](api.md).

This document defines v1alpha1 scope. The source proposal provides broader
context; inclusion there does not imply inclusion here. Items marked **Out of
scope** may still be included by v1.

All `*Config` resource types, including `AIGatewayConfig`, `ModelProviderConfig`,
and `ModelConfig`, are out of scope for v1alpha1, along with their configuration
inheritance through `baseRef`. These types can be introduced later as an optional
composition layer without disrupting the core API resources.

`spec.tenantRef` and Model's `spec.publishedName` are immutable after creation.
Update validation must reject changes; changing either requires deleting and
recreating the resource. This preserves its isolation and authorization basis.

## Status and readiness

Tenant, AIGateway, ModelProvider, and Model expose one condition type in v1alpha1:
`Ready`. The controller writes `status.conditions` through the status subresource.
Conditions use the standard Kubernetes
[`metav1.Condition`](https://github.com/kubernetes/apimachinery/blob/master/pkg/apis/meta/v1/types.go)
shape, with one entry per condition `type`.

| Field | Meaning |
| --- | --- |
| `type` | `Ready`. |
| `status` | `"True"`, `"False"`, or `"Unknown"`. |
| `observedGeneration` | The resource's `metadata.generation` assessed by the controller. |
| `reason` | A machine-readable cause, such as `Ready`, `DependencyNotReady`, or `Conflict`. |
| `message` | A human-readable explanation identifying any blocking resource or configuration. |
| `lastTransitionTime` | When the condition's `status` last changed. |

`Ready=True` means the criteria below are met; `False` means a known requirement
is unmet; `Unknown` means readiness is undetermined. A missing condition or an
`observedGeneration` that differs from `metadata.generation` does not establish
readiness for the current specification.

### Readiness by resource

| Resource | Criteria for `Ready=True` |
| --- | --- |
| Tenant | Its configuration is valid and its namespace ownership is accepted without conflict. |
| AIGateway | Its Tenant is ready, gateway configuration and required references are valid, the generated configuration is applied, and the backing runtime reports ready for that configuration. |
| ModelProvider | Its Tenant is ready, its format and connection target are valid, any Service reference resolves, and configured credential references and Secret keys resolve and are authorized. |
| Model | Its Tenant and referenced ModelProviders are ready, its configuration is valid, it wins any published-name conflict, and it is configured on every inference-enabled gateway belonging to its Tenant, each of which must be ready. |

A Model with no inference-enabled gateways has `Ready=False`, reason
`DependencyNotReady`, and a message explaining why. Tenant and ModelProvider
readiness require no consumers.

### Example

Controller-written AIGateway status after observing generation 1:

```yaml
status:
  conditions:
    - type: Ready
      status: "True"
      observedGeneration: 1
      reason: Ready
      message: Gateway configuration is applied and the runtime is ready.
      lastTransitionTime: "2026-11-06T12:00:00Z"
```

## Tenant

### Definition

A Tenant represents the organisation or team that owns and administers one or
more gateways. Its administrators configure those gateways and publish models
through them. It is the long-lived anchor for gateways and models, intended to
outlive individual, potentially ephemeral gateway definitions. A gateway cannot
be shared by multiple Tenants.

Users and clients accessing a Tenant's gateways may belong to many different
organisations or companies. Their access is governed by the Tenant's configured
authentication types and the gateway and Model authorization policies. Many
external organisations can access resources exposed through the gateway as long
as they meet the Tenant's access criteria.

- `authorizedIdentities` lists allowed authentication identities by `type`:
  `OIDC` or `APIKey`.
- `authorizedCredentials` lists credential Secrets the gateway may use to access
  upstream services.

In v1alpha1, identity validation checks only `type`. Future versions are intended
to add optional constraints, such as `issuerURL`, without changing the entry
shape.

### Namespace and isolation

Tenant is namespaced, and a namespace can only belong to a single Tenant. In v1alpha1,
its membership and isolation boundary are its own namespace. Tenant resources
and their Kubernetes references remain in that namespace, except for a
ModelProvider's backend Service reference.

Future namespace membership and cross-namespace `tenantRef` resolution are
intended as additive extensions, allowing a Tenant to span workload namespaces
and be defined outside them. They are out of scope for v1alpha1.

### Example

This Tenant allows OIDC and API key authentication. Its authorized upstream
credential Secret `model-provider-key` is in `team-alpha`.

```yaml
apiVersion: aigateway.praxis.io/v1alpha1
kind: Tenant
metadata:
  name: team-alpha
  namespace: team-alpha
spec:
  authorizedIdentities:
    - type: OIDC
    - type: APIKey
  authorizedCredentials:
    - secretRef:
        name: model-provider-key
```

### Out of scope

- Credential revocation, which is not yet supported in PPE.
- Specific validation of identity provider properties, such as `issuerURL`.
- A Tenant namespace field letting one Tenant span multiple namespaces.

## AIGatewayConfig

### Definition

A deferred composition resource for reusable gateway configuration.

## AIGateway

### Definition

An AIGateway declares a gateway that exposes AI services for one Tenant. A Tenant
may have multiple AIGateways. Each gateway declares its runtime, shared endpoint,
inference and MCP capabilities, and governance directly on the resource in v1alpha1.

| Field | Purpose |
| --- | --- |
| `tenantRef` | Required, immutable reference to the Tenant the gateway belongs to. |
| `runtime` | Gateway runtime settings, such as replica count. |
| `endpoint` | Shared gateway exposure settings: port and optional TLS certificate reference. |
| `inference` | Optional inference capability with explicitly selected inference APIs. |
| `mcp` | Optional MCP capability; an empty stanza enables MCP. |
| `governance` | Inline governance configuration, including authentication identities. |

Gateway governance applies to inference and MCP processing on the shared listener.
Authentication is configured on the gateway; capability-specific governance
overrides, custom API extension properties, and templates are deferred.

### Capability configuration

`spec.endpoint` configures the shared HTTP listener. It requires a `port` in the
range 1–65535, with no default. Optional `endpoint.tls.certificateRef` selects a
TLS certificate Secret in the gateway's namespace.

`spec.inference` and `spec.mcp` select capabilities served through that endpoint.
At least one must be configured; omission disables that capability. An empty
`mcp: {}` stanza enables MCP without additional capability settings.

Under `inference`, `chatCompletions` and `responses` are named API configuration
stanzas. Omission disables that API; an empty stanza selects it without
additional settings. An `inference` block must select at least one API. API
stanzas can carry traffic-specific settings and dependency references, such as
`inference.responses.conversationState.secretRef`.

Selected inference APIs and HTTP-based MCP share the listener. The controller
generates request method/path dispatch to their processing chains. Port and TLS
settings belong to `endpoint`, not to either capability. Models and ModelProviders
supply model access and upstream configuration; enabling inference alone does
not publish a model.

### Namespace and tenancy

An AIGateway must reference its Tenant through `spec.tenantRef`, which resolves
by name in the gateway's namespace.

### Validation

- `tenantRef` is required, immutable after creation, and must resolve to a Tenant
  in the gateway's namespace.
- At least one of `inference` or `mcp` must be configured. An `inference` block
  must select at least one supported API.
- `endpoint` is required, with a port in the range 1–65535.
- A configured `endpoint.tls` block requires `certificateRef`, resolving in the
  gateway's namespace.
- Each `governance.authentication` entry uses a `type` discriminator matching a
  Tenant `authorizedIdentities` entry. A subset of authorized types is allowed.
- Tenant identity constraints validate type membership only; provider properties
  such as `issuerURL` are not checked against Tenant constraints in v1alpha1.

### Example

This gateway serves Chat Completions, Responses and MCP through one TLS endpoint
on port 8443. Its gateway authentication configuration applies to all three APIs.

```yaml
apiVersion: aigateway.praxis.io/v1alpha1
kind: AIGateway
metadata:
  name: team-alpha-gw
  namespace: team-alpha
spec:
  tenantRef:
    name: team-alpha
  runtime:
    replicas: 2
  endpoint:
    port: 8443
    tls:
      certificateRef:
        name: gateway-tls
  inference:
    chatCompletions: {}
    responses:
      conversationState:
        secretRef:
          name: responses-state
  mcp: {}
  governance:
    authentication: #gateway access auth not gateway administration auth
      - type: OIDC
        issuerURL: https://sso.example.com
        audiences:
          - team-alpha
      - type: APIKey
        header: X-API-Key
        keySource:
          secretRef:
            name: consumer-keys
            key: keys.yaml
        refreshSeconds: 5
        maxStalenessSeconds: 30    
```

The TLS certificate Secret `gateway-tls` and storage connection Secret
`responses-state` must also be in the `team-alpha` namespace.

### Out of scope

- Configuration inheritance through `baseRef` or `AIGatewayConfig`.
- Referencing a Tenant or other Kubernetes resources in a different namespace.
- Tenant authorization checks on identity provider properties beyond the
  authentication type.
- Enforcing gateway governance at the Tenant level.
- Capability-specific governance overrides, custom API extension properties,
  and templates.
- Multiple endpoints per gateway or capability-specific port/TLS settings.

## ModelProviderConfig

### Definition

A deferred composition resource for reusable model provider configuration.

## ModelProvider

### Definition

A ModelProvider registers a reusable upstream model service, such as vLLM, llm-d,
or an external provider. It declares the connection target, credentials, and
native API format used to select protocol translation. Models reference it to
publish consumer-facing model names.

| Field | Purpose |
| --- | --- |
| `tenantRef` | Required, immutable reference to the Tenant the provider belongs to. |
| `format` | The upstream's native API format, such as `OpenAI`. |
| `backend.serviceRef` | An in-cluster Service reference, including its port and optional namespace. The Service may be in another namespace. |
| `endpoint` | An external upstream URL, as an alternative to `backend.serviceRef`. |
| `credentials.source.secretRef` | Reference to an upstream credential Secret authorized by the Tenant. |
| `credentials.inject.header` | The upstream HTTP header used to present the credential, such as `Authorization`. |
| `credentials.inject.valuePrefix` | Header value prefix; defaults to `Bearer`. |

### Namespace and tenancy

A ModelProvider must be in the same namespace as its Tenant and must reference
that Tenant through `spec.tenantRef`. Tenant and credential Secret references
resolve in the ModelProvider's namespace.

`backend.serviceRef.namespace` may identify a Service in another namespace. If
omitted, it defaults to the ModelProvider's namespace. Access is expected to be
controlled through user-provided NetworkPolicy for network connectivity and
ValidatingAdmissionPolicy (VAP) for permitted Service references. These policies
are provided and managed by the user in v1alpha1.

Configured credential Secrets must be in the Tenant's `authorizedCredentials`.
The gateway presents them to the upstream service. Credential values remain in
the Secret, outside the ModelProvider resource.

### Validation

- `tenantRef` is required, immutable after creation, and must resolve to a Tenant
  in the provider's namespace.
- Exactly one connection target must be configured: `backend.serviceRef` or
  `endpoint`.
- `format` must identify a supported upstream API format.
- A configured credential Secret reference must resolve in the provider's
  namespace and be authorized by the referenced Tenant.

### Example

The credential Secret is in `team-alpha`; the backend Service is in
`model-serving`. Users provide NetworkPolicy and VAP controls for Service access.

```yaml
apiVersion: aigateway.praxis.io/v1alpha1
kind: ModelProvider
metadata:
  name: llmd-prod
  namespace: team-alpha
spec:
  tenantRef:
    name: team-alpha
  format: OpenAI
  backend:
    serviceRef:
      name: llmd-gateway
      namespace: model-serving
      port: 8000
  credentials:
    source:
      secretRef:
        name: model-provider-key
        key: token
    inject:
      header: Authorization
      valuePrefix: Bearer
```

### Out of scope

- Configuration inheritance through `baseRef` or `ModelProviderConfig`.
- Cross-namespace Tenant or credential Secret references.
- Controller-provided NetworkPolicy or VAP resources for backend Service access.
- Provisioning or managing the upstream model-serving service itself.

## ModelConfig

### Definition

A deferred composition resource for reusable model configuration.

## Model

### Definition

A Model maps a consumer-facing `publishedName` to ModelProviders and their upstream
model names. Consumers send that name in the request's `model` field. The Model
belongs to a Tenant, carries inline governance, and exists independently of
individual gateway definitions.

| Field | Purpose |
| --- | --- |
| `tenantRef` | Required, immutable reference to the Tenant the model belongs to. |
| `publishedName` | The immutable model name exposed to consumers. |
| `providers` | One or more upstream provider mappings for the published model. |
| `providers[].providerRef` | Reference to a ModelProvider belonging to the same Tenant. |
| `providers[].upstreamModel` | The model name sent to that provider. |
| `governance` | Inline model authorization, with guardrails and observability as stretch goals. |
| `governance.authorization.cel` | CEL expression defining the model's authorization check. |

Selection among multiple provider mappings remains to be defined; weighting and
failover are out of scope.

### Governance scope

| Capability | v1alpha1 scope |
| --- | --- |
| `authentication` | Out of scope on Model; configured on the gateway. |
| `authorization` | In scope: access rules for the model. |
| `guardrails` | Stretch goal; supported controls remain to be defined. |
| `observability` | Stretch goal; supported controls remain to be defined. |
| `rateLimits` | Out of scope for v1alpha1, including request-count and token limits. |

### Authentication and authorization

Authentication (AuthN) is configured on the AIGateway in v1alpha1. Model-level
authentication configuration and overrides are not supported in this version.

Model authorization (AuthZ) uses a boolean `governance.authorization.cel`
expression. Its `claim` map contains verified claims from gateway authentication.
The Model's authorization check succeeds only when the expression is `true`.

### Governance composition

Model authorization adds to gateway authorization; a request must satisfy both.
Model guardrails and observability, if supported, are also additive and do not
replace or disable the gateway's corresponding controls.

### Namespace and tenancy

A Model must be in the same namespace as its Tenant and must reference that
Tenant through `spec.tenantRef`. ModelProvider references resolve in the Model's
namespace and must belong to the same Tenant.

By default, a Model is exposed through the selected inference APIs on every
inference-enabled AIGateway belonging to its Tenant, all in the same namespace.
MCP-only gateways do not expose Models. No explicit gateway reference is provided
in v1alpha1.

### Validation

- `tenantRef` is required, immutable after creation, and must resolve to a Tenant
  in the Model's namespace.
- The Model and each gateway exposing it must be in the same namespace and
  belong to the same Tenant.
- `publishedName` is required, must be non-empty, and is immutable after creation.
- `publishedName` must be unique on a gateway; conflicts follow the rule below.
- `providers` must contain at least one entry. Each entry must specify a
  `providerRef` that resolves to a ModelProvider of the same Tenant and an
  `upstreamModel` name.
- Model-level `governance.authentication` must not be specified in v1alpha1.

### Published-name conflicts

For conflicting published names on a gateway, the earliest
`metadata.creationTimestamp` wins; ties use `metadata.name` in lexicographic
ascending order. Only the winner is exposed. Losing Models report `Ready=False`,
reason `Conflict`, and a message identifying the winner.

### Example

This Model publishes `alpha-chat` through every inference-enabled gateway owned
by `team-alpha`. It uses gateway OIDC authentication and requires the verified `groups` claim to
include `alpha-model-users`; a missing claim or group fails authorization.

```yaml
apiVersion: aigateway.praxis.io/v1alpha1
kind: Model
metadata:
  name: alpha-chat
  namespace: team-alpha
spec:
  tenantRef:
    name: team-alpha
  publishedName: alpha-chat
  providers:
    - providerRef:
        name: llmd-prod
      upstreamModel: meta-llama/Llama-3.1-70B
  governance:
    authorization:
      cel: >-
        has(claim.groups) && "alpha-model-users" in claim.groups
```

### Out of scope

- Configuration inheritance through `baseRef` or `ModelConfig`.
- Cross-namespace Tenant, ModelProvider, or gateway references.
- Model-level authentication configuration and overrides.
- Request-count and token limits.
- Provider weighting and failover.

Spikes may assess PPE rate-limit support and filter changes needed for weighting
and failover in a later iteration.

## MCPServer

### Definition

Pending discussion.

### Example

Pending discussion.

### Out of scope

Pending discussion; no exclusions agreed yet.

## Agent

### Definition

A deferred resource for registering and governing agent endpoints.

The entire Agent resource is out of scope for v1alpha1.
