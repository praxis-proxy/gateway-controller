# MaaS external API-key authentication example

Status: Worked proposal.

Design: [Extensible gateway authentication](gateway-authentication.md).

This example applies the proposed authentication API to a MaaS validation service.
The service owns the API-key records. PPE sends the caller's key to that service
and uses a successful response to establish the authenticated identity.

The AIGateway fields are proposed inputs for an auth package, not fields already
implemented by the controller. The PPE configuration uses its existing HTTP
API-key directory capability. The example covers the `Authorization` API-key
branch of the reference Kuadrant AuthPolicy; full policy parity remains separate.

## Tenant and AIGateway configuration

An auth package named `maasApiKey` lets the Tenant permit external MaaS validation
independently of the Secret-backed `apiKey` type. Its proposed template location is:

```text
<extension-root>/aigateway/governance/auth/maasApiKey/
```

The package supplies its schema, template and documentation. Its owner supplies
admission rules for the platform administrator to add to the shared AIGateway
VAP. The controller validates through Helm, and the Tenant must grant permission
to use the type:

```yaml
# Tenant.spec
authorizedIdentities:
  - type: maasApiKey
```

The gateway configures the package with a flat authentication entry:

```yaml
# AIGateway.spec excerpt
governance:
  authentication:
    - name: maas-consumers
      authType: maasApiKey
      header: Authorization
      prefix: "Bearer sk-oai-"
      validationURL: https://maas-api.redhat-ai-gateway-infra.svc.cluster.local:8443/internal/v1/api-keys/validate
      timeoutSeconds: 5
      cache:
        ttlSeconds: 60
        negativeTtlSeconds: 10
```

These settings belong to the package rather than an auth-specific controller
branch. The package understands the MaaS response contract and maps `username`
and `groups` into the identity consumed by subsequent authorization.

This package requires no verifier Secret or file mount. Reference resolution and
mount allocation apply only to packages that declare file dependencies. The
controller image and CRD do not change when this package is added through the
proposed extension mechanism.

## Generated PPE configuration

The authentication portion of the assembled PPE document would be:

```yaml
engine_settings:
  dispatch: policy

plugins:
  - name: auth-maas-consumers
    kind: identity/api-key
    hooks: [identity.resolve]
    capabilities: [perform_http]
    on_error: fail
    config:
      credential:
        kind: header
        name: Authorization
      prefix: "Bearer sk-oai-"

      provider:
        kind: http
        url: https://maas-api.redhat-ai-gateway-infra.svc.cluster.local:8443/internal/v1/api-keys/validate
        key_field: key
        valid_field: valid
        timeout_secs: 5

      record_map:
        subject:
          id: username
          roles: groups

      cache:
        ttl_secs: 60
        negative_ttl_secs: 10

global:
  authentication:
    - auth-maas-consumers
  authorization:
    pre_invocation:
      - "require(authenticated)"
  assertions:
    request:
      strip:
        - Authorization
```

The shared assembler adds the gateway's model routes and any later model
authorization checks to this same document. The package contributes the identity
plugin and credential-stripping requirement; it does not create a separate
gateway policy.

The service endpoint also requires outbound access and TLS trust in the Praxis
runtime. Declaring `perform_http` grants the plugin its HTTP capability; runtime
transport, trust and egress configuration must permit the internal service.

## Validation and identity flow

For a request carrying:

```http
Authorization: Bearer sk-oai-example
```

PPE removes the Bearer scheme, retains the `sk-oai-` key prefix and sends an HTTP
POST with a JSON body:

```json
{"key":"sk-oai-example"}
```

An illustrative successful response is:

```json
{
  "valid": true,
  "username": "alice",
  "groups": ["alpha-model-users"]
}
```

PPE requires a boolean `valid` field. HTTP 200 alone is insufficient: `valid:
false` rejects the key, and a missing or incorrectly typed verdict is a malformed
directory response. Transport failures and unexpected status codes fail closed.

On success, `username` becomes `subject.id` and `groups` becomes `subject.roles`.
The PPE MaaS recipe uses `username` as the principal; it documents that `userId`
and `keyId` refer to the key's database row rather than the subject. Subsequent
model authorization evaluates the verified identity without revalidating the key.
The caller's `Authorization` header is stripped before forwarding upstream.

Successful lookups are cached for 60 seconds in this example; unknown keys are
cached for 10 seconds. A revoked key can continue authenticating until its positive
cache entry expires. These are explicit example settings, not a claim that all
reference AuthPolicy cache behaviour is reproduced.


## References

- [PPE MaaS HTTP API-key directory recipe](https://github.com/praxis-proxy/policy/blob/main/docs/content/guides/identity-delegation.md#recipe-7-an-opaque-api-key-resolved-against-a-directory)
- [HTTP directory request and response handling](https://github.com/praxis-proxy/policy/blob/main/crates/builtins/src/plugins/identity_api_key/http_directory.rs)
- [API-key resolver configuration](https://github.com/praxis-proxy/policy/blob/main/crates/builtins/src/plugins/identity_api_key/config.rs)
- [Credential lookup and missing-header handling](https://github.com/praxis-proxy/policy/blob/main/crates/builtins/src/token_lookup/resolver.rs)
- [PPE fetched metadata](https://github.com/praxis-proxy/policy/blob/main/docs/content/apl/metadata.md)
