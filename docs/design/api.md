# Proposal: AI Gateway Kubernetes control plane

Authors: [Guilherme Cassolato](mailto:mcassola@redhat.com) / [Craig Brookes](mailto:cbrookes@redhat.com)

Date: 2026-10-28

Property names and shared field shapes follow the [v1alpha1 design](api-v1alpha1.md).
This proposal retains broader capabilities and alternatives; the v1alpha1 design
defines which capabilities are included in the first version.

## 1. What this proposes, in one breath

A small set of Kubernetes CRDs in group aigateway.praxis.io/v1alpha1 that let operators declare a fronted AI estate in AI-domain nouns (tenant, gateway, model, model provider, MCP server, agent) and AI-domain governance (auth, guardrails, rate limiting, observability, credentials) — composed through inheritance (baseRef → shared *Config), extension references (extRef → connectors for non-standard cases), generation templates (editable, code-decoupled scaffolds the controller renders the low-level resources from), and an opinionated facade over the core proxy (the AI-Gateway controller generates and owns the backing Proxy — which carries its filter chains inline — with a mutually-exclusive proxyRef escape hatch). No proxy, listener, route, filter, APL document, Gateway API object, or GEP-713 targetRef is ever surfaced to the operator.

## 2. Design principles (the "sweet spot")

1. Domain nouns, not proxy primitives. Vocabulary is model / provider / endpoint / agent / tool, never route / listener / filter / cluster. A listener at the core layer appears here as the narrower endpoint; AI protocols induce filter chains the operator never sees.
    
2. Gateway concerns only — nothing business, nothing proxy. No subscriptions, billing, cost centres, org IDs, catalog curation (the product's job); no filter chains, APL, token reserve/reconcile internals, Gateway API objects (the proxy's job). See §11.
    
3. Compose via the favoured patterns only. Inheritance, extension references, generation templates, facade. No policy-attachment metaresources; no user-facing defaults/overrides merge across policy objects.
    
4. One unified consumer surface, one canonical internal format. Consumers may speak more than one inbound inference API (OpenAI Chat Completions, OpenAI Responses, Anthropic Messages), selected under the gateway's `inference` capability; `mcp` enables MCP on the shared endpoint, and A2A remains a future capability. The gateway normalises inference requests to a single canonical internal format — OpenAI Chat Completions — governs them there, and translates out to each provider's native format. This keeps translation N+M (inbound→canonical, then canonical→provider), never N×M, and means governance (policy/PPE) always inspects the canonical shape regardless of front door or backend. Provider selection, body-based routing and protocol translation are consequences of registering models and selecting inference APIs, never hand-written routes.
    
5. The Tenant Standard is load-bearing. One authoritative tenant identity, established at authentication, carried across auth, policy, rate limits, metering, credentials, routing, logs and traces — never derived from unauthenticated client headers.
    
6. No Gateway API dependency, Gateway API-friendly. The control plane stands on its own CRDs; Gateway API is an optional lower-level building block, never required.
    

## 3. Composition patterns, as used here

|Pattern|Favoured?|Where it is used in this API|
|---|---|---|
|Inheritance / Base configuration (baseRef → FooConfig, shared schema, instance overrides a subset)|✅|AIGateway→AIGatewayConfig, Model→ModelConfig, ModelProvider→ModelProviderConfig. Reusable safe defaults (baseline auth, guardrails, limits) live in a *Config; concrete instances inherit and set only what differs.|
|Extension references (extRef → a different-kind resource, reserved for non-standard cases that do not overlap standard fields)|✅|Connectors/extensions that are not standard knobs: a custom GuardrailProvider, a custom IdentitySource/PDP, a custom processing extension. Standard auth/guardrail/limit knobs stay inline; extRef carries only the exotic.|
|Escape hatch / Opinionated facade (Foo ⊂ Bar; controller generates Bar by default; mutually-exclusive barRef to a user-authored Bar)|✅|The whole AI-Gateway layer is a facade over the core Proxy (which carries its filter chains inline). AIGateway.spec.proxyRef hands control to a hand-authored Proxy; the controller then only watches it.|
|Generation templates (editable, code-decoupled scaffold; the controller renders each generated low-level kind by strategic-merge-patching the base *Config and then the instance onto a template)|✅|Each derived core kind (Proxy, …) is rendered from a template — the defaults behind the defaults — shipped in a ConfigMap at install and editable by operators. Generation = template ← AIGatewayConfig (baseRef) ← AIGateway instance, by strategic merge. Keeps complex generated shapes (long filter chains) out of controller code; §8.1.|
|Policy Attachment (GEP-713 targetRefs)|✗ dropped|Not used. Governance is inline + inherited, not attached via metaresources. (The Core API itself declares filter chains inline on the Proxy, listeners naming the chains that apply — so even the proxy layer is top-down, not attachment-based.)|
|Defaults & Overrides (merge strategies)|✗ dropped|Not used at the user-facing API: reuse is top-down inheritance (baseRef), one simple rule — a set field overrides the inherited value — not a multi-level default/override merge across policy objects. (The strategic merge that renders a generated core resource from a template is a controller-internal step, §8.1, not a user-facing metaresource merge.)|

## 4. Personas and ownership (RBAC per layer)

The AI-Gateway CRDs are split so these personas own disjoint resources:

|Persona|Owns (creates/edits)|Never touches|
|---|---|---|
|Provider Admin (platform ops; grid-admin)|Tenant (onboards it), AIGatewayConfig (fleet-wide safe defaults), the tenant's AIGateway instance(s), provider-credential operations|Core Proxy RBAC can differ/be withheld|
|Tenant Admin (customer platform team)|within their tenant: Model, ModelProvider (which credentials their tenant may use), MCPServer, Agent, tenant-scoped limits/guardrails, their Tenant's `authorizedCredentials` list|Other tenants' resources; cluster-wide config|
|Tenant User (developer/app/agent)|nothing in the control plane; authenticates to the gateway at the edge|Provider credentials (never handled)|
|Security engineer|org-level auth/guardrail base configs, revocation/kill-switch, audit config|—|

  

RBAC-per-layer is a first-class reason the facade exists: a persona can be granted the AI-Gateway kinds without any access to the core proxy kinds the controller generates.

## 5. The resource model

Group/version: aigateway.praxis.io/v1alpha1. Core proxy group it lowers to: praxis.io/v1alpha1.

`spec.tenantRef` is immutable after creation on resources that declare it. A
Model's `spec.publishedName` is also immutable. Updates attempting to change either
field must be rejected; changing Tenant ownership or the published model name
requires deleting and recreating the resource.

Tenant, AIGateway, ModelProvider, and Model use the shared `status.conditions`
`Ready` contract defined in the [v1alpha1 design](api-v1alpha1.md#status-and-readiness).

```mermaid
flowchart TB
    providerAdmin["Provider Admin"] --> tenant["Tenant<br/>Isolation boundary: identity, credentials, revocation<br/>Owns its gateway(s)"]
    tenant -->|"tenantRef<br/>Each AIGateway belongs to exactly one Tenant"| gateway["AIGateway<br/>(data plane)"]
    gateway -->|baseRef| gatewayConfig["AIGatewayConfig<br/>(fleet defaults)"]
    gateway -->|facade| proxy["Proxy<br/>(filter chains inline)"]

    subgraph tenantResources["Tenant Admin creates these within the tenant"]
        model["Model<br/>(+Config)"]
        modelProvider["ModelProvider<br/>(+Config)"]
        mcpServer["MCPServer"]
        agent["Agent"]
    end

    gateway --> model
    gateway --> modelProvider
    gateway --> mcpServer
    gateway --> agent
```

Endpoints carry inline governance: auth · guardrails · rateLimits · observability · credentials.

Compose via: `baseRef` (inheritance) · `extRef` (connectors) · templates (generation scaffold) · `proxyRef` (escape hatch).

### 5.1 Tenant — the isolation boundary and authoritative identity

The first-class isolation concept (UC3) and the carrier of the Tenant Standard: one authoritative tenant identity, the credentials the tenant is authorized to use, and its revocation surface. A Tenant owns one or more AIGateways; each AIGateway belongs to exactly one Tenant (no gateway is shared, per the 3.7 contract). The Tenant is not a base config and not an attachment target — it is an ownership boundary; governance reuse is still done through *Config + baseRef, not through a tenant→gateway cascade.

```yaml
apiVersion: aigateway.praxis.io/v1alpha1
kind: Tenant
metadata: { name: team-alpha, namespace: ai-gateway-system }
spec:
  authorizedIdentities:                                 # authentication types the tenant authorizes
    - type: OIDC
    - type: APIKey
  isolation: { namespace: team-alpha }                 # how isolation is realised (own ns / keyspace)
  authorizedCredentials:                               # which provider-credential sources this tenant may use
    - secretRef: { name: llmd-api-key }
  revocation: { store: { kind: Valkey }, failClosed: true }   # caller-credential kill switch (one-pager)
```

`authorizedIdentities` lists the identities allowed by the Tenant, each with a
`type` discriminator (`OIDC` or `APIKey`).
Identity provider configuration, such as OIDC `issuerURL` and `audiences`, lives
under `governance.authentication` on the gateway or its proposed base config.

#### Alternative structure: Tenant references tenant-owned AIGatewayConfig

Proposed alternative to the Tenant structure above; the existing proposal remains for comparison.

- **Tenant** defines `authorizedIdentities`, the namespaces authorized to consume the tenant's configuration, and the source Secrets in `authorizedCredentials` that may be shared. It references the tenant's AIGatewayConfig.
- **AIGatewayConfig** defines that tenant's gateway template and defaults: runtime, shared endpoint, inference/MCP capabilities, and governance, including authentication and revocation configuration. Defaults are owned by the tenant; this alternative does not require a fleet-wide defaults resource.
- Source Secret references resolve in the Tenant's namespace. The controller synchronizes the declared Secrets into authorized namespaces containing consuming gateways. An allowed namespace alone does not trigger distribution without a consuming gateway.
- The Tenant's namespace allowlist authorizes consumption. A gateway reference alone cannot grant access to another tenant's configuration or credentials.
- Gateways use the referenced AIGatewayConfig as their blueprint and may override permitted defaults. Overrides cannot change Tenant namespace authorization or Secret-sharing permissions; mandatory controls must be distinguished from defaults.
- Models explicitly attach to gateways, subject to the tenant boundary.
- Source Secret changes propagate to controller-managed copies. Revoking namespace authorization or removing the last authorized consumer stops use and triggers cleanup of copies that are no longer needed. Unrelated destination Secrets must not be overwritten.
- The tenant identity carried into policy, credentials, limits, storage, metering, logs and traces remains distinct from the authenticated user/agent identity.

```text
Tenant
├── authorizedIdentities
├── allowedNamespaces
├── authorizedCredentials (source Secrets in Tenant namespace)
└── gatewayConfigRef ──► AIGatewayConfig
                         └── tenant gateway defaults/template
```

Illustrative configuration (proposed fields):

```yaml
apiVersion: aigateway.praxis.io/v1alpha1
kind: Tenant
metadata:
  name: alpha
  namespace: tenant-alpha-system
spec:
  authorizedIdentities:
    - type: OIDC
    - type: APIKey
  allowedNamespaces:
    - alpha-public
    - alpha-internal
  authorizedCredentials:
    - secretRef: { name: provider-key }
  gatewayConfigRef:
    name: alpha-defaults
    namespace: tenant-alpha-system
---
apiVersion: aigateway.praxis.io/v1alpha1
kind: AIGatewayConfig
metadata:
  name: alpha-defaults
  namespace: tenant-alpha-system
spec:
  runtime: { replicas: 2 }
  endpoint: { port: 8443 }
  inference: { chatCompletions: {}, responses: {} }
  governance:
    authentication:
      - type: OIDC
        issuerURL: https://sso.example.com
        audiences: [alpha]
    # Shared revocation configuration lives alongside authentication here.
```

Changes from the existing structure:

- Tenant retains identity-type authorization, namespace membership, and shared-credential authorization. Authentication provider details and revocation configuration live together in the referenced AIGatewayConfig.
- Tenant selects its AIGatewayConfig through gatewayConfigRef; AIGatewayConfig supplies tenant-owned defaults and a gateway blueprint.
- allowedNamespaces replaces the single isolation.namespace mapping, allowing one tenant to authorize gateway namespaces explicitly.
- The `authorizedCredentials` list becomes an explicit distribution contract in this alternative: source Secrets remain authoritative, and the controller maintains managed replicas in consuming gateway namespaces.
- Explicit Model-to-Gateway attachments replace automatic exposure through tenant membership alone.
- The controller gains authorization checks, Secret synchronization, rotation propagation and cleanup responsibilities. The credential-management description must acknowledge persistent Kubernetes Secret replicas while retaining per-request credential use in the proxy.

Open design details: the exact gateway reference fields, and the list of overridable versus mandatory settings.


### 5.2 AIGatewayConfig + AIGateway — surface and baseline governance

AIGatewayConfig holds reusable safe defaults (runtime, exposure, baseline governance). AIGateway references it via baseRef and overrides only what differs — the inheritance pattern — and declares the one Tenant it belongs to via tenantRef (immutable).

```yaml
apiVersion: aigateway.praxis.io/v1alpha1
kind: AIGatewayConfig
metadata: { name: fleet-defaults, namespace: ai-gateway-system }
spec:
  runtime: { image: "registry.example.com/praxis-ai:VERSION", replicas: 2 }
  endpoint:
    port: 8443
    tls: { certificateRef: { name: fleet-tls } }
  inference:
    chatCompletions: {}
    responses: {}
  governance:                                          # fleet-wide safe defaults, inline
    authentication: [ { type: OIDC, issuerURL: "https://sso.example.com", audiences: [maas] } ]
    guardrails:
      input:  [ { pii: { action: Redact } } ]
    observability: { usageMetering: { sink: { kind: Prometheus } }, tracing: { sampling: 0.1 } }

---

apiVersion: aigateway.praxis.io/v1alpha1
kind: AIGateway
metadata: { name: team-alpha-gw, namespace: team-alpha }
spec:
  baseRef: { name: fleet-defaults }                    # inherit everything above…
  tenantRef: { name: team-alpha }                      # …belongs to exactly one Tenant (immutable)
  runtime: { replicas: 3 }                             # …override only what differs
  inference:                                          # shared endpoint inherited from the base
    chatCompletions: {}
    responses: {}
    messages: {}                                      # broader proposal: Anthropic Messages → canonical Chat Completions
  mcp: {}
  # proxyRef: { name: team-alpha-proxy }               # ESCAPE HATCH (facade): bring your own Proxy
```

Gateway governance applies to inference and MCP processing on the shared listener. Model, MCPServer and Agent governance adds resource-specific controls. Capability-specific governance overrides and multiple endpoints per gateway are deferred. Each AIGateway owns one Proxy.

`spec.endpoint` supplies the shared port and optional TLS configuration. After inheritance, the effective gateway configuration requires an endpoint with a port and at least one capability (`spec.inference` or `spec.mcp`). Omitting a capability disables it; `mcp: {}` enables MCP. Under `inference`, individual API stanzas such as `chatCompletions` and `responses` must be explicitly selected; an empty stanza selects that API, and an inference block with no selected API is invalid. Selected inference APIs and HTTP-based MCP share one listener, with generated request method/path dispatch. The broader proposal also illustrates `inference.messages`; its inclusion does not expand v1alpha1 scope. Non-canonical inference formats are normalised before governance (§2, principle #4).

#### 5.2.1 Inference API storage

An inference API stanza may declare storage dependencies needed by its gateway-managed implementation. For Responses, these include conversation/response state, file search, and file access. Each dependency is a Kubernetes Secret reference. The Secret carries the connection type, endpoint, credentials, and adapter-specific connection options; the API stanza carries the reference and feature behavior such as retention.

This uses the existing reference-based composition model without introducing a Storage CRD. Backend selection lives in the Secret, so the AIGateway does not repeat a backend type alongside the reference.

```yaml
apiVersion: aigateway.praxis.io/v1alpha1
kind: AIGateway
metadata: { name: team-alpha-gw, namespace: team-alpha }
spec:
  tenantRef: { name: team-alpha }
  endpoint:
    port: 8443
    tls: { certificateRef: { name: gateway-tls } }
  inference:
    responses:
      conversationState:
        secretRef: { name: responses-state }
        retention: 7d
      # Optional dependencies when the gateway implements these features:
      # fileSearch:
      #   secretRef: { name: responses-vector-search }
      # files:
      #   secretRef: { name: responses-files }
---
apiVersion: v1
kind: Secret
metadata: { name: responses-state, namespace: team-alpha }
type: Opaque
stringData:
  type: postgres/sql
  connectionString: "postgresql://gateway:REPLACE_ME@postgres.team-alpha.svc:5432/responses"
```

These are proposed control-plane fields. The connection discriminator is `stringData.type` (stored as `data.type`), distinct from the Kubernetes Secret's top-level `type: Opaque`. Each supported connection type defines its required keys and the storage operations its adapter provides. The Responses implementation validates that the selected adapter supports the role for which it is referenced.

The dependency roles remain distinct:

|Responses field (under `inference.responses`)|Purpose|
|---|---|
|conversationState|Persist responses and conversation items, retrieve stored responses, and restore context for subsequent turns.|
|fileSearch|Connect to the retrieval service used to execute hosted file search. The Secret may describe a vector-store HTTP API rather than a direct vector database connection.|
|files|Connect to the file service used to resolve uploaded file references and content.|

References and feature settings may be inherited through AIGatewayConfig and overridden by the AIGateway using baseRef's existing rules. Secret references resolve in the consuming AIGateway's namespace, including inherited references. Tenant authorization applies to dependency use; the runtime also enforces ownership of individual conversations, responses, and files.

Lowering resolves the Secret's connection type into the appropriate runtime configuration and supplies connection material to the generated implementation. The control plane declares dependencies and validates configuration; the runtime owns connections, persistence, rehydration, and retrieval calls. External database, retrieval, and file-service provisioning remains with their infrastructure owners. The generated configuration must support credential rotation consistently with the existing credential model (§5.3, §10).

A storage dependency is required when the gateway-managed feature needs it. Forwarding to a provider that owns the feature's state does not require a local storage reference. Removing or changing a reference does not implicitly delete or migrate existing data.

### 5.3 ModelProvider (+ ModelProviderConfig) — upstream and brokered credentials

A reusable upstream (self-hosted vLLM/llm-d, or external vendor), declaring its native API format so the gateway can translate to/from the unified consumer surface. Per the one-pager, credentials are brokered, not stored: referenced from an external source, resolved per request, injected before the backend call, discarded after — and rotatable with no downtime.

```yaml
kind: ModelProvider
metadata: { name: llmd-prod, namespace: team-alpha }
spec:
  tenantRef: { name: team-alpha }
  format: OpenAI                                       # OpenAI | Anthropic | Bedrock | Vertex | Azure | ...
  backend: { serviceRef: { name: llmd-gateway, namespace: model-serving, port: 8000 } }  # self-hosted vLLM/llm-d in-cluster
  # endpoint: https://api.openai.com                   # …or an external vendor URL
  credentials:                                         # BROKERED — referenced, never copied/stored
    source: { secretRef: { name: llmd-api-key, key: token } }  # selects the credential value in the Secret
    inject:
      header: Authorization
      valuePrefix: Bearer
    # rotation is picked up without restart; self-identity (mTLS/client-creds) configured here too
  # extRef: { kind: GuardrailProvider, name: ... }     # exotic per-provider connector (non-standard only)
```

`credentials.source.secretRef.key` selects the credential entry in the referenced
Secret. `credentials.inject.header` names the upstream HTTP header, and
`credentials.inject.valuePrefix` defaults to `Bearer` when omitted. The prefix
and credential value are separated by a space, for example
`Authorization: Bearer <credential>`. `backend.serviceRef.namespace` identifies
the Service's namespace and defaults to the ModelProvider's namespace if omitted.

### 5.4 Model (+ ModelConfig) — the consumable model

What a consumer requests by name; fronts one or more providers (weighted/failover). The published name is the body-based routing key; no route is ever written. Per-model governance (e.g. tighter token limits, extra guardrails) is inline, and a family of models can share defaults through baseRef → ModelConfig.

Model authorization uses `governance.authorization.cel`, a boolean CEL expression
over the identity established by gateway authentication. The `claim` map contains
verified identity claims. The example below checks group membership using the
same field shape as the v1alpha1 design.

A Model declares its owning tenant with tenantRef and lives in that tenant's namespace. That tenant membership — not any Model→AIGateway reference — is how a model binds to a gateway: there is no gatewayRef, because a Model is served by its tenant's AIGateway(s) (the same holds for MCPServer and Agent; a ModelProvider is a backend pulled in via a Model's providerRef and rides along with whichever gateway serves that model). With the 3.7 one-gateway-per-tenant common case this is unambiguous; selecting a specific gateway when a tenant runs several is open question §13.4.

Token limits are an ordered list of identity-keyed rules — a budget is a property of the (subject, model) pair, not of the model alone, so the same model can grant different budgets to different subjects (this is what lets many MaaS subscriptions map onto one model — §9.1). Each rule carries one or more windowed budgets (e.g. an hourly and a daily limit). Rules are evaluated first-match-wins (ordered evaluation within one list, not a cross-resource merge); a trailing catch-all can deny to yield the "authorised-but-no-quota → 429" behaviour. Each match resolves against the identity established by governance.authentication. The full tokenomics surface (estimation, token-type weights, org/team/user hierarchy) is §6.1; the shorthand below is the common case.

```yaml
kind: ModelConfig                                      # reusable per-model-family defaults (inheritance)
metadata: { name: chat-defaults, namespace: team-alpha }
spec:
  governance:
    rateLimits:
      tokens:
        - match: {}                                    # default budget for any authorised subject
          budgets: [ { window: 1h, limit: 100000 } ]
    guardrails: { output: [ { pii: { action: Redact } } ] }

---

kind: Model
metadata: { name: alpha-gpt4o, namespace: team-alpha }
spec:
  baseRef: { name: chat-defaults }
  tenantRef: { name: team-alpha }                      # owning tenant → served by the tenant's gateway(s)
  publishedName: "gpt-4o"                              # the "model" field consumers send
  providers:
    - { providerRef: { name: llmd-prod }, upstreamModel: "meta-llama/Llama-3.1-70B", weight: 90 }
    - { providerRef: { name: openai-fallback }, upstreamModel: "gpt-4o", weight: 10 }
  governance:
    authorization:
      cel: >-
        has(claim.groups) && "alpha-model-users" in claim.groups
    rateLimits:
      tokens:                                          # ordered; first match wins
        - match: { groups: ["team-alpha-premium"] }    # e.g. one MaaSSubscription onto this model
          budgets: [ { window: 1h, limit: 500000 } ]
          # priority: 10                                # subscription priority → ordering / inferencePriority
        - match: { groups: ["team-alpha-basic"] }      # another subscription onto the SAME model
          budgets: [ { window: 1h, limit: 100000 }, { window: 24h, limit: 1000000 } ]
        - match: {}                                     # catch-all: authorised but unsubscribed
          deny: true                                    # → 429 (the gateway-default-deny analog)
```

### 5.5 MCPServer and Agent — agentic endpoints (UC6)

Registered and governed identically to models; MCP gets tool discovery/federation brokering, A2A gets agent routing. Governance is inline, reuse via baseRef, exotic connectors via extRef.

```yaml
kind: MCPServer
metadata: { name: alpha-tools, namespace: team-alpha }
spec:
  tenantRef: { name: team-alpha }                      # owning tenant → served by the tenant's gateway(s)
  backend: { endpoint: "https://tools.internal/mcp", transport: StreamableHTTP }
  toolFilter: { allow: ["search_*", "read_*"] }        # which tools this tenant may discover/use
  governance: { authentication: [ { type: OIDC, issuerURL: "https://sso.example.com" } ] }

---

kind: Agent
metadata: { name: research-agent, namespace: team-alpha }
spec:
  tenantRef: { name: team-alpha }
  backend: { endpoint: "https://agents.internal/research", protocol: A2A }
```

## 6. Governance without attachment

Run 1's standalone, attached policy kinds are gone. Governance is expressed as inline governance blocks on the resources above, composed by the three favoured patterns:

- Reuse = put the common governance in a *Config and point baseRef at it. A set field on the instance overrides the inherited value (one rule, no merge grammar).
    
- Scope = where the block lives is its scope. A block on AIGatewayConfig/AIGateway is gateway-wide (and, since each gateway serves a single tenant, scoped to that tenant); a block on a Model/MCPServer is endpoint-specific; tenant-wide identity authorization and revocation live on the Tenant as `authorizedIdentities` and `revocation`. No targetRef, no inheritance-direction ambiguity.
    
- Exotic cases = extRef to a connector resource (GuardrailProvider, IdentitySource, custom PDP/extension) for things that do not fit the standard inline knobs, without widening the standard schema.
    
- Beyond the facade = proxyRef to a hand-authored Proxy (§8).
    

The standard inline knobs cover: authentication (OIDC/JWT, API key), authorization (claim/role rules expressed through `authorization.cel`), guardrails (input/output: PII, content-safety, prompt-guard), rateLimits (requests and token-denominated tokens, the latter an ordered list of identity-keyed rules — see §5.4), observability (usage metering sink, tracing), and credentials (on ModelProvider). Tenant-level identity authorization and caller-credential revocation/kill-switch (one-pager) live on the Tenant (`authorizedIdentities`, `revocation`: store ref, fail-closed, bounded staleness), since they are inherently per-tenant under the Tenant Standard.

Access vs quota are two independent concerns (this is what MaaS encodes as its "need both an auth policy and a subscription" rule — §9.1): authentication+authorization govern access (unauthorised ⇒ 403), while the rateLimits.tokens rules govern quota (authorised but matching a deny/absent rule ⇒ 429). The two are set independently and neither implies the other.

### 6.1 Token budgets and tokenomics (the MaaS MUST-DO)

MaaS's move onto AI Gateway brings the [Tokenomics spec](https://github.com/praxis-proxy/enhancements/blob/main/proposals/00121_token-rate-limiting.md). That spec is a data-plane specification — estimation mechanics, token-type capture mappings, reserve/reconcile, streaming, tier header injection, batch nesting. Its capability is not a standalone praxis-ai token_rate_limit filter: it lands in PPE, which now incorporates Kuadrant's Limitador, so token rate limiting and request-count limiting are one engine. The control plane must express its MUST-DO (M1–M7, plus the org/team/user hierarchy H1) at domain altitude — budgets, keys, cost intent — and not re-expose that low-level surface wholesale (which would reintroduce exactly the leaky, too-technical surface the whole proposal avoids). The split is deliberate:

|Tokenomics capability|In the control plane as…|Altitude|
|---|---|---|
|M1 condition-matched rules → budgets + default rule|rateLimits.tokens[]: each rule a match + one or more windowed budgets; a trailing match: {} catch-all|surfaced|
|M2 reservation/reconciliation|— (always-on enforcement mechanism; no knob)|hidden (data plane)|
|M3 configurable estimation|estimation.strategy enum (MaxTokens/InputPlusMaxTokens/Fixed/ModelScaled) + fallbackEstimate; default once per gateway, overridable per rule|surfaced (opinionated enum; mechanics hidden)|
|M4 token-type weights + capture|tokenAccounting.weights map (gateway default, per-rule override). Capture is a filter-wide preset (tokenAccounting.capture, default openai); per-provider / mixed-format handling and custom mappings are escape-only (§9, open)|surfaced (weights) / filter-wide preset (capture)|
|M5 flexible bucket keys|the rule match (headers, models, authenticated subject, compound). One key → one budget|surfaced|
|M6 hard deny 429 + standard headers|implied by a budget's limit (deny on exhaustion); Retry-After/X-RateLimit-* emitted automatically|hidden (data plane)|
|M7 observability + accounting|governance.observability (metrics/tracing) + tokenAccounting.metering.sink (exact per-rule/per-model records for chargeback)|surfaced|
|H1 org→team→user hierarchy|rateLimits.tokens.hierarchy: exactly three fixed levels (org, team, user), each with an identity source + window + limit|surfaced|
|soft/graduated tiers (S1), enforcement: Soft|escape-only in this first cut — the first control-plane surface is strictly MUST-DO; soft limits may return first-class pending §9|escape-only (for now)|
|estimation internals, streaming usage, reserve/reconcile, capture mappings & mixed-format, batch settlement, over-quota header specifics|escape hatch rateLimits.tokens.ref → raw PPE rate-limit config|escape-only|

  

Filter-wide defaults live at the gateway/tenant (they are "filter-wide" in the spec, and inheritable via baseRef):

```yaml
# on AIGatewayConfig / AIGateway (or Tenant) .spec.governance
tokenAccounting:
  estimation: { strategy: InputPlusMaxTokens, fallbackEstimate: 2000 }   # M3 default
  weights: { input: 1.0, output: 1.0, cachedInput: 0.1, reasoning: 0.9 } # M4 default (cached ~10× cheaper)
  capture: { preset: OpenAI }  # M4: filter-wide; mixed-format gateways / custom mappings → escape (§9, open)
  metering: { sink: { kind: BillingExport } }                           # M7/chargeback (exact actuals)
```

  

Per-rule budgets carry the richer shape where needed (the §5.4 single-window form is the shorthand):

```yaml
# on a Model (or AIGateway) .spec.governance.rateLimits.tokens
- match: { groups: ["team-alpha"], models: ["gpt-4o"] }   # M1/M5 — compound key
  estimation: { strategy: ModelScaled }                   # M3 per-rule override
  weights: { cachedInput: 0.05 }                          # M4 per-rule override
  budgets:
    - { window: 1h,  limit: 100000 }                       # hard deny at limit (M6)
    - { window: 24h, limit: 1000000 }
# soft tiers / enforcement: Soft are escape-only in this first cut (strictly MUST-DO) — §9
```

  

Hierarchical budgets (H1) are a separate, fixed three-level stack enforced on one request — org, then team, then user — each an independent budget; a parent exhausting its budget hard-denies a child still within its own:

```yaml
# on AIGateway / Tenant .spec.governance.rateLimits.tokens.hierarchy
hierarchy:
  - { level: Org,  key: { header: x-org-id },        window: 1h, limit: 5000000 }
  - { level: Team, key: { header: x-team-id },       window: 1h, limit: 1000000 }
  - { level: User, key: { subject: Authenticated },  window: 1h, limit: 100000  }
```

  

Lowering (all in PPE, backed by Limitador). All of rateLimits — both requests and tokens — lowers into the PPE document (the policy filter's config), where Kuadrant's Limitador runs as a PPE plugin (kind: ratelimit/limitador, invoked as a run(...) step after identity resolution — the spike in [policy#152](https://github.com/praxis-proxy/policy/issues/152)). Each budget maps to a limitador::Limit (max_value / seconds / conditions + variables to qualify the counter), and the keys are CEL/APL over the PPE identity attribute bag (subject.*, claim.*) populated by the identity/jwt plugin, so limits condition directly on claims/subject with no round-trip through a downstream filter. Request-count limiting is that plugin's verified shape; token budgets are the AI extension on top — token denomination (counting token deltas, not requests), the reserve → reconcile admission wrapper, token-type weighting, and estimation — with praxis-ai contributing only the existing token-usage extraction (the token_count/token_usage filter) that PPE reconciles against. There is no separate token_rate_limit filter. Metering records are emitted at reconciliation to the tokenAccounting.metering.sink. This unifies auth and all rate limiting into one engine config, and closes the old distributed request-count gap — Limitador-in-PPE means distributed counters for request-count limits too, not just token budgets.

What the control plane deliberately does not surface in this first cut (handled below it, or escape-only): the reserve/reconcile lifecycle and lost-request cleanup (M2); streaming-usage / include_usage handling; the provider-field capture mappings (only a filter-wide capture preset is surfaced; per-provider/mixed-format mappings via escape — §9); soft/graduated tiers (S1) and enforcement: Soft (kept strictly MUST-DO for now — may return first-class pending §9); the Retry-After computation and concurrent-reservation overshoot (open questions in the spec); and batch settlement (its own path — still open upstream). An operator who needs any of these verbatim uses rateLimits.tokens.ref to supply the raw PPE rate-limit config, and the controller stops generating that section (the same facade rule as everywhere else).

## 7. How a declared system becomes a processing pipeline

Unchanged in spirit from run 1: the pipeline is derived, never declared. A call on the team-alpha gateway — POST /v1/messages (Anthropic front door) or POST /v1/chat/completions (OpenAI), both with {"model":"gpt-4o"} — yields, implicitly:

```mermaid
flowchart TB
    parse["Parse + extract model"] --> inbound["Normalise inbound protocol<br/>→ canonical (OpenAI Chat Completions)"]

    subgraph canonicalGovernance["All on the canonical shape (principle #4)"]
        admission["Authenticate → authorize → input guardrails"]
        limits["Token reserve + request rate limit"]
        admission --> limits
    end

    inbound --> admission
    limits --> routing["Body-based route: publishedName &quot;gpt-4o&quot;<br/>→ Model alpha-gpt4o → pick ModelProvider (weighted)"]
    routing --> providerRequest["Translate canonical → provider format<br/>→ broker + inject provider credential"]
    providerRequest --> upstream["Forward to vLLM/llm-d"]
    upstream --> providerResponse["Response: translate provider → canonical"]
    providerResponse --> responseGovernance["Count tokens → token reconcile → output guardrails"]
    responseGovernance --> clientResponse["Translate canonical → inbound protocol"]
    clientResponse --> telemetry["Emit usage/trace"]
```

Governance always runs on the canonical shape: inbound translation precedes policy, provider translation follows it, so PPE sees one format whatever the client or backend speaks — the merged 3.7 pipeline (payload translation + BBR from the data-plane path, PPE policy replacing Kuadrant for auth/rate-limit) is this one chain. Each step is a praxis/praxis-ai filter or PPE hook, assembled into the Proxy's inline filter chains by the controller — surfaced to the operator only as the governance knobs of §6.

Note — deterministic ordering. The order above is one explicitly-ordered chain (parse → inbound-translate → policy → provider-translate → route). The Core API declares filter chains inline on the Proxy — spec.filterChains[] is a list of {name, filters}, each spec.listeners[].filterChains names the chains that apply, and filters run in the order declared — so the controller emits the AI filters and the policy entry in one deterministic order. This is invisible to our personas: the controller owns the whole Proxy.

## 8. Lowering to the core proxy (the facade in action)

The AI-Gateway controller generates and owns core praxis.io/v1alpha1 resources: AIGateway+AIGatewayConfig → a Proxy with a shared listener derived from `endpoint`. Its spec.filterChains[] contains the ordered processing chains, and the listener names the applicable chains. Generated request method/path dispatch selects the enabled inference or MCP processing. The operator never sees these resources.

|AI-Gateway concept|Lowers to|Provisioned by|
|---|---|---|
|AIGateway/AIGatewayConfig endpoint/runtime/service|Shared Proxy listener + Deployment/Service|core proxy / praxis-operator|
|`inference`: {chatCompletions, responses, messages}; `mcp`|API dispatch and ordered processing chains in Proxy.spec.filterChains[], referenced by the shared listener|praxis-ai|
|non-canonical inbound APIs (e.g. `inference.messages`)|inbound-translation filters (anthropic_messages_to_chat_completions(_stream), path rewrite, header strip) normalising to canonical before policy|praxis-ai|
|ModelProvider (backend)|load_balancer cluster + endpoints|praxis|
|Model|model_to_provider (publishedName → cluster + target model) + canonical→provider format translation (after policy, e.g. openai_chat_completions_to_anthropic(_stream))|praxis-ai|
|MCPServer / Agent|MCP classify+route + catalog brokering / A2A classify+route|praxis-ai|
|`governance.authentication` / `governance.authorization.cel` / `governance.guardrails`|PPE APL document (authentication:/authorization:/assertions:) + policy filter|PPE|
|governance.rateLimits.tokens (identity-keyed rules, tokenomics §6.1)|the PPE document backed by Limitador (distributed counters/windows/conditions; denomination, reserve/reconcile, weighting, estimation — no separate filter), reconciling against praxis-ai token-usage extraction; keys from PPE identity|PPE (Limitador) + praxis-ai (usage extraction)|
|governance.rateLimits.requests|PPE document backed by Limitador (distributed counters)|PPE (Limitador)|
|governance.observability|token-usage extraction + metrics/trace exporters|praxis-ai / praxis|
|ModelProvider.credentials / Tenant.revocation|brokered upstream credential injected by PPE assertions (AWS SigV4 via aws_sigv4_sign); revocation store check|PPE / praxis-ai|

  

Escape hatches (facade pattern): AIGateway.spec.proxyRef → user-authored Proxy (or a Gateway API Gateway, §12) (controller stops generating, only watches; since filter chains are inline on the proxy, the whole proxy is the unit of escape); extRef → connector extensions for non-standard governance. The rule: an escape replaces the generated artifact of the same kind; it never silently layers on top.

### 8.1 Generation templates (the defaults behind the defaults)

The shapes the controller generates are not hardcoded. For each core kind it owns (Proxy and the rest), the controller starts from a template — an initial, editable resource skeleton — and renders the final object in layers, by strategic merge patch:

```text
template  →  + AIGatewayConfig (baseRef defaults)  →  + AIGateway (instance values)  →  generated Proxy
```

  

The template carries what is always present and stable (e.g. the baseline filters every inference chain needs, in order); AIGatewayConfig layers the fleet's safe defaults; the AIGateway instance layers what differs. Fields inferred from a single high-level knob — an inference API stanza inducing a translation filter, a Model inducing a routing entry — are merged in by the controller the same way.

Templates are decoupled from controller code — shipped as a ConfigMap created when the AI Gateway controller is installed — so operators can adjust the generated low-level shape (add a standing filter, change an ordering, set a default) without a controller release. This is where the genuinely complex generated structures live (the long, mixed filter chains of a Proxy), kept out of code and out of the user-facing CRDs alike. It is also the mechanism behind inheritance (§5.2): baseRef is realised as the middle merge layer here. The strategic merge is a render step that produces one resource; it is not the disfavoured user-facing defaults/overrides merge across policy objects (§3).

## 9. Composing upward: the AI product layer (RHOAI/MaaS)

§8 showed the facade pointing down (AI Gateway → Core). The same pattern points up: the AI product's control plane is itself an opinionated facade over the AI Gateway layer. The result is a three-layer stack where each seam uses the identical escape-hatch mechanism, so the whole thing is open at every level without duplicating APIs. A full worked example across all three layers is in the [End-to-end example](https://docs.google.com/document/d/1Gq6JnyB84-5uk0dG16zQQx4igmbt7wvRX0XGRydThxw/edit?tab=t.kb946u2kkni6).

```text
RHOAI/MaaS — business: entitlements, API-key lifecycle, billing, priority, dashboard UI
   AITenant · MaaSModelRef · MaaSSubscription · MaaSAuthPolicy · ExternalProvider/ExternalModel
        │  aiGatewayRef   ◄── suggested escape hatch (optional, mutually exclusive, non-breaking)
        ▼
AI Gateway — tenant / model / provider / governance   (this proposal)
   Tenant · AIGateway(+Config) · Model · ModelProvider · MCPServer · Agent
        │  proxyRef   ◄── escape hatch (§8)
        ▼
Core Praxis — proxy (filter chains inline)
   Proxy
```

### 9.1 How MaaS CRDs lower onto the AI Gateway layer

Today MaaS compiles its top-level CRDs down to Kuadrant AuthPolicy/TokenRateLimitPolicy + Istio (EnvoyFilter/ServiceEntry/DestinationRule) + Gateway API. Routing them through the AI Gateway facade instead collapses three downstream technologies into one intermediate layer whose own lowering to Core is already defined (§8).

|MaaS top-level CRD (today)|Lowers onto AI Gateway|Notes / what the move retires|
|---|---|---|
|AITenant (+ MaasTenantConfig)|Tenant (+ AIGateway/AIGatewayConfig)|Dedicated namespace, per-tenant gateway, tenant-admin RBAC → Tenant→AIGateway. Allowed authentication types → Tenant.authorizedIdentities; OIDC configuration/telemetry → governance.authentication / governance.observability. API-key lifecycle (maas-api + Postgres) stays business.|
|MaaSModelRef|Model (+ ModelProvider)|"Publish as MaaS" creates a Model; retires the generated ServiceEntry/DestinationRule/EnvoyFilter.|
|ExternalProvider / ExternalModel|ModelProvider / Model|Near 1:1 (apiFormat→format, externalProviderRefs→providers[]) — the strongest overlap; a candidate for MaaS to delegate wholesale rather than re-model.|
|MaaSSubscription|one or more identity-keyed rules in the target Model's governance.rateLimits.tokens (full tokenomics surface: §6.1)|The entitlement/quota plan stays business (owner, priority, billing); only its enforcement crosses down. Each subscription emits rule(s) matched on its owner.groups/users, carrying its tokenRateLimits (window/limit) and priority; many subscriptions onto one model → many rules on that one Model, transposed by the MaaS controller at reconcile — the same aggregation it does today into one per-model Kuadrant TokenRateLimitPolicy. Org/team/user entitlement structure maps to the H1 hierarchy; FinOps chargeback to tokenAccounting.metering. Not a new AI Gateway kind, and not an attachment: the Model (MaaS-generated) owns its own rules.|
|MaaSAuthPolicy|governance.authentication + governance.authorization.cel (+ Tenant.authorizedIdentities)|Already slated to change significantly in the Envoy+Kuadrant→AI Gateway move: stops compiling to Kuadrant AuthPolicy, compiles to AI Gateway governance / PPE.|

  

Access vs quota — why MaaS needs both objects. MaaS's rule that a model needs both a MaaSAuthPolicy and a MaaSSubscription (auth without subscription ⇒ 429; subscription without auth ⇒ 403) falls straight out of the two independent governance concerns (§6): MaaSAuthPolicy lowers to authentication+authorization (access — 403 when absent), and MaaSSubscription lowers to rateLimits.tokens rules (quota — 429 when authorised but no matching rule). Same two-key semantics, now two inline governance blocks on resources MaaS already owns instead of two Kuadrant CRs.

What never crosses down (stays strictly MaaS): subscription/entitlement semantics, API-key CRUD, billing/priority/org attribution, the dashboard and its feature flags (maasAuthPolicies, externalModels, …). This is the top row of the §11 responsibility table, made concrete.

### 9.2 The suggested aiGatewayRef escape hatch

For custom product deployments that must deviate from MaaS's opinionated safe defaults (its default API-key authentication and Subscription-based quota), MaaS can expose an optional, mutually-exclusive aiGatewayRef on a top-level CRD (e.g. AITenant, or MaasTenantConfig). When unset, the MaaS controller generates and owns the backing AI Gateway Tenant/AIGateway as today; when set, it stops managing them and only watches the operator-authored ones. This is additive — no breaking change to the existing API.

Because the control-plane state now carries a "managed externally" case, dependent product components must adapt. The brief's own example: when custom authentication/authorization replaces MaaS's defaults, the MaaS UI should show a "custom auth" indicator and relax or hide the Subscription/API-key flows that no longer drive enforcement. More generally, anything that reads control-plane state for display, metering attribution, or policy decisions must treat an aiGatewayRef-escaped tenant/model as externally managed rather than assuming the MaaS-generated shape.

We can only suggest these MaaS-side changes (we control the AI Gateway and Core layers, not the product). aiGatewayRef and the MaaSAuthPolicy reshape are offered as low-cost, composition-improving steps that align with work already under way.

## 10. Tenancy, credentials, and multi-cluster posture (3.7)

- Tenant boundary. Tenant is a first-class kind (§5.1). The 3.7 contract is that each AIGateway belongs to exactly one Tenant (separate data planes; a tenant may run more than one gateway, but no gateway is shared). Running multiple tenants on one shared gateway (soft per-tenant controls) is gated on a per-request policy-isolation fix and is out of 3.7 scope.
    
- Credentials. Brokered, never stored (§5.3): resolved per request, injected into the HTTP header named by `credentials.inject.header` with `credentials.inject.valuePrefix` (default `Bearer`), discarded; no-downtime rotation; gateway self-identity for calls it makes as itself. AWS SigV4 and CloudIdentity remain additional mechanisms in the broader proposal. Caller-credential revocation is a fail-closed kill switch on the Tenant (§5.1, §6).
    
- Multi-cluster. No first-class cross-cluster capability in 3.7. Multiple single-cluster AIGateways pointed at shared external identity/policy/rate-limit (and revocation) services give a partial multi-cluster story with no new component. A hub/spoke ("AI Grid") control plane that distributes tenant-safe config across clusters is future work; the data-plane seams exist.
    

## 11. Responsibility boundary (the deliverable)

Three layers, three owners:

|Concern|AI product (RHOAI/MaaS, AI Grid, EnMaaS)|AI Gateway control plane (this proposal)|Core proxy (praxis.io / praxis-ai / PPE)|
|---|---|---|---|
|Tenant lifecycle, subscriptions, billing, org/cost-centre|✅ owns|—|—|
|Model catalog curation / ownership|✅ owns|consumes Model/ModelProvider|—|
|Tenant boundary & identity (Tenant Standard)|provides identity source|✅ Tenant (+ its AIGateways; one gateway per tenant)|enforces across dimensions|
|Model/provider registration & routing|—|✅ Model/ModelProvider|body-based route + translate|
|Auth / guardrails / rate-limit / observability intent|sets plan intent|✅ inline governance + baseRef/extRef|PPE/filters enforce|
|Credential brokering & caller revocation|owns the secret source|✅ ModelProvider.credentials / Tenant.revocation|broker + inject + revoke-check|
|MCP / A2A exposure & governance|—|✅ MCPServer / Agent|classify+route|
|Usage metering (raw signal)|consumes for billing|✅ governance.observability emits|token counting|
|Listeners / routes / filters / clusters|—|generates & owns via facade|✅ owns (Proxy, filter chains inline)|
|APL / PPE documents|—|generates|✅ executes|
|Data-plane deployment / rollout|—|drives|✅ owns|

  

Read the middle column top to bottom: "front my AI endpoints and enforce common processing" — and nothing else. Who-pays / what's-in-the-catalog is the product's; how-bytes-are-proxied is the core proxy's.

## 12. Stance on Gateway API

The control plane requires no Gateway API, and nothing in the AI-Gateway surface depends on it. When a Gateway API implementation is present, it is used at the core layer as another bring-your-own-proxy case of the facade escape hatch (§8): the AIGateway points its proxyRef at a Gateway API Gateway instead of a generated Proxy, and the controller only watches it. This does not remove the need for the praxis.io Core API group — Gateway API has no standard surface for most Praxis filter and policy capabilities. Policy is carried as a Policy resource, referenced from an HTTPRoute rule as an extensionRef route filter (the one referenceable filter kind today; others may follow), and the proxy's remaining inline filters are rendered by that same controller.

The hard constraint: the rendered Praxis config has exactly one owner. The Gateway API implementation used here must therefore be the same controller that implements the Praxis Core API — one controller both deploys the proxy (from the Gateway) and configures its filters (the extensionRef-ed Policy, plus the inline filters it renders directly). A separate Gateway-API-only proxy controller would contend with the Core controller over the same proxy config, so that is not a supported topology. Gateway API is an optional building block, never a requirement.

## 13. Open questions

1. Group naming — decided: aigateway.praxis.io/v1alpha1 (product-neutral, tied to the proxy project rather than any one AI product; core proxy stays praxis.io).
    
2. *Config granularity — one AIGatewayConfig carrying all baseline governance, or separate reusable *Config kinds per concern (e.g. a shared GuardrailConfig) referenced by baseRef/extRef? Trade-off: fewer kinds vs finer reuse/RBAC.
    
3. extRef vs inline boundary — exactly which governance is "standard inline" vs "exotic extRef"? (Mirrors Gateway API's standard-filter vs ExtensionRef line.)
    
4. Shared-gateway (multi-tenant) gateway — the Tenant kind exists now (one gateway per tenant). When the per-request isolation fix lands, how do multiple tenants share one AIGateway with soft controls, without reintroducing attachment/merge? Also: are tenant-scoped endpoints (Model/etc.) selected onto a specific gateway when a tenant runs several (e.g. an optional gatewayRefs), or served by all the tenant's gateways by default?
    
5. Request-count distributed limiting — resolved by PPE importing Limitador: distributed counters now back both request-count and token budgets (§6.1). Remaining: counter-store topology (shared Redis/Valkey) and fail-open vs fail-closed on it; the multi-environment aggregation case stays a separate design ([S0] in the tokenomics spec).
    
6. Reconciler topology — AI-Gateway controller emits core praxis.io resources for the Praxis control plane to consume – though they could be packed as a single controller/operator workload.
    
7. Token-RL ↔ Kuadrant alignment — keep this API above the ai#127/ai#796 decision so either lowering works.
    
8. MaaS composition (suggestions to the product) — which top-level CRD carries aiGatewayRef (AITenant vs MaasTenantConfig), and at what granularity (whole-tenant vs per-model)? And what is the concrete migration shape for MaaSAuthPolicy once it compiles to AI Gateway governance instead of Kuadrant AuthPolicy? How far should MaaS delegate ExternalProvider/ExternalModel to the near-identical AI Gateway ModelProvider/Model rather than re-modelling them?
    
9. Tokenomics altitude boundary (§6.1) — parked pending discussion. Two knobs are deliberately held back in the first cut, to be revisited together: (a) soft tiers (S1) + enforcement: Soft are escape-only for now — promote to first-class if the discussion favours it; (b) token-type capture is a single filter-wide preset for now — revisit for mixed-format gateways (per-provider capture derived from ModelProvider.format, or an explicit per-provider override) if one gateway must front providers of differing formats. Also still open: H1's identity resolution — org/team headers are set by a trusted upstream (the spec's non-goal); is that the Tenant/PPE identity layer's job, and how is that trust boundary expressed in the API?
    
10. Inbound-protocol translation fidelity (§2 principle #4, §7) — the canonical-internal-format invariant assumes every inbound protocol round-trips cleanly through OpenAI Chat Completions. Where does it leak? Candidate concerns: fields with no canonical equivalent (Anthropic system-prompt blocks, tool-use shapes), token-usage field divergence across formats (so token_count reconciles the right numbers — relates to §6.1 capture), and whether finer governance scoping is needed when inbound formats warrant different auth (e.g. Anthropic x-api-key vs OIDC bearer). The shared listener uses gateway governance; finer overrides remain deferred.
