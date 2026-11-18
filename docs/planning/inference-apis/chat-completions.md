# OpenAI Chat Completions filter mapping

Status: Proposed controller mapping. Sources reviewed on 2026-11-11 against
Praxis AI [`a6c78b2`][ai-revision]. Tests were inspected, not run. The policy
reference and complete generated chain remain to be qualified.

## When configuration is created

Clients call `POST /v1/chat/completions` with a Model's published name. The
controller combines these inputs for each gateway:

| Configuration | What the controller should configure |
| --- | --- |
| `AIGateway.spec.protocols.llm.chatCompletions: {}` | Enable the Chat Completions endpoint and model extraction (`model_to_header`). Apply protocol limits. No provider or storage connection is required in this stanza. |
| An accepted `Model` with resolved `ModelProvider` references | Add the published-name/upstream-model mapping to the routing overlay, provider clusters, model rewriting or translation, and provider credential bindings. |
| `AIGateway.spec.governance` | Generate the gateway's authentication and authorization policy, referenced after model extraction and before provider routing. |
| `Model.spec.governance.authorization` | Add the Model's access rules to that policy stage, evaluated against the public model identity. Model rules add to gateway rules. |

These are reconciliation inputs, not a required resource-creation order. Models
populate a shared protocol chain; they do not each require a separate chain.
Protocol enablement alone must not create a fallback route to an arbitrary
provider. Forwarding requires an accepted Model and resolved provider.

## Example filter chain

This example follows the [selected-provider fixture][chat-selected-config],
with a proposed policy placeholder. Model and provider configuration determine
which processing is needed:

| Providers exposed through the gateway | Chain requirements |
| --- | --- |
| OpenAI-compatible only | Model extraction, policy when configured, provider selection, upstream model rewriting, credentials where required, and forwarding. The JSON adapter below performs model-only rewriting for an `openai-chat` profile; the Anthropic SSE adapter can be omitted in a proposed native-only variant. |
| Includes Anthropic Messages | Also include the SSE adapter and enable request/JSON-response translation in the JSON adapter. An Anthropic provider does not require a Messages front door. |

Anthropic translation is limited to the [request adapter's supported subset][chat-request-adapter].

The fixture includes both provider formats and assumes provider credentials:

```yaml
filter_chains:
  - name: main
    filters:
      # Protocol enabled: extract the client's published model name.
      - filter: model_to_header
        header: X-Model

      # Governance configured: proposed PPE policy reference placeholder.
      # Apply gateway authentication/authorization and Model authorization.

      # Models published: select their resolved provider profiles.
      - filter: intelligent_route
        overlay_file: /etc/praxis/routing/routing-overlay.json
        model_header: X-Model
        clusters: [anthropic-backend, openai-backend]
        reload: {enabled: true, debounce_ms: 100}

      # Anthropic providers: translate streaming responses.
      - filter: openai_chat_completions_to_anthropic_stream
        max_partial_event_bytes: 1048576
        max_tool_blocks: 256

      # Rewrite native OpenAI model names or translate Anthropic requests/JSON.
      - filter: openai_chat_completions_to_anthropic
        api_translation_enabled: true
        max_body_bytes: 1048576

      # Provider credentials: resolve the selected Secret's projected file.
      - filter: credential_inject
        credentials: []
        projected_credential_mount_base: /run/secrets/provider-credentials

      # Provider targets: forward to the selected cluster.
      - filter: load_balancer
        clusters:
          - name: anthropic-backend
            endpoints: ["127.0.0.1:8001"]
          - name: openai-backend
            endpoints: ["127.0.0.1:8002"]
```

Assume this controller creates and updates a routing overlay file for each
gateway from its accepted Models and resolved ModelProviders. Each candidate
identifies the published model, provider cluster, upstream format/path, target
model and credential reference. The controller makes the file available at
`overlay_file` and keeps it consistent with provider clusters and credential
mounts. Cluster names, routing headers, file paths and reload settings are
controller-derived. Listener configuration is omitted from the example.

The policy is a comment placeholder, absent from the source fixture. Verify that
PPE sees the body-derived model before evaluation and rejects denied requests
before credential injection or upstream contact. YAML position alone does not
prove callback ordering. The JSON adapter rewrites requests after upstream
selection. Both response adapters use the bound provider profile.

## Inputs and options to surface

Existing resource fields supply the serving configuration:

| Resource | Inputs |
| --- | --- |
| `Model` | `spec.publishedName`, `spec.providers[].providerRef`, `spec.providers[].upstreamModel`. |
| `ModelProvider` | `spec.format`; exactly one of `spec.backend.serviceRef` or `spec.endpoint`; `spec.credentials.source.secretRef` including its key; `spec.credentials.inject.header` and `.valuePrefix`. |
| Governance | Gateway authentication/authorization and `Model.spec.governance.authorization.cel`. These remain separate from protocol settings. |
| `Tenant` | Namespace boundary and `spec.authorizedCredentials` permitting the provider Secrets. Secrets resolve in the provider/Tenant namespace. |

For this mapping, omitting `chatCompletions` disables the API and `{}` enables
it. No Chat Completions storage connection is needed. Decide whether the
following limits need protocol fields or controller-managed settings:

| Candidate option | Praxis adapter setting | Runtime default |
| --- | --- | --- |
| Request-body limit | `max_request_body_bytes` | 2 MiB |
| Anthropic JSON response limit | `max_response_body_bytes` | 1 MiB |
| Incomplete SSE-event limit | `max_partial_event_bytes` | 1 MiB |
| Streaming tool-block limit | `max_tool_blocks` | 256 |

Defaults come from the [JSON][chat-adapter-config] and [SSE][chat-stream-config]
adapter configs. The example's `max_body_bytes: 1048576` overrides both JSON
adapter limits to 1 MiB; direction-specific settings take precedence. Request
bounds must also agree with the model-extraction filter's buffering limit.

Open mapping decisions:

- Whether selecting an Anthropic provider implicitly permits translation or
  requires a separate administrator setting. The runtime adapter defaults
  `api_translation_enabled` to `false`.
- How credential header/prefix settings map to the [credential filter][chat-credentials].
  It supports fixed `Authorization: Bearer <token>` or a raw API key. Dynamic
  projected API keys use `x-api-key`; custom headers require an explicit entry.
  Arbitrary prefixes are unsupported by this chain. The Secret key is chosen by
  the reference, not fixed by Chat Completions.

- Define how `ModelProvider.spec.format` values map to the routing profile and
  select the required adapters.
- Define provider selection when a Model has multiple provider bindings, so the
  controller can configure the overlay's selection policy.
- Define caller-credential removal for credential-free upstreams. The current
  `credential_inject` filter skips header removal when no credential is selected.

[ai-revision]: https://github.com/praxis-proxy/ai/tree/a6c78b2313ddc0a66c6b777a069b5f66d22e5beb
[chat-selected-config]: https://github.com/praxis-proxy/ai/blob/a6c78b2313ddc0a66c6b777a069b5f66d22e5beb/examples/configs/openai-selected-provider-legacy-translation.yaml
[chat-request-adapter]: https://github.com/praxis-proxy/ai/blob/a6c78b2313ddc0a66c6b777a069b5f66d22e5beb/apis/src/anthropic/openai_chat_completions_to_anthropic/request.rs
[chat-adapter-config]: https://github.com/praxis-proxy/ai/blob/a6c78b2313ddc0a66c6b777a069b5f66d22e5beb/apis/src/anthropic/openai_chat_completions_to_anthropic/config.rs
[chat-stream-config]: https://github.com/praxis-proxy/ai/blob/a6c78b2313ddc0a66c6b777a069b5f66d22e5beb/apis/src/anthropic/openai_chat_completions_to_anthropic_stream/config.rs
[chat-credentials]: https://github.com/praxis-proxy/ai/blob/a6c78b2313ddc0a66c6b777a069b5f66d22e5beb/filters/src/routing/credential_inject.rs
