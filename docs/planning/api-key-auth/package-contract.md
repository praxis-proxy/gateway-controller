# Auth package contract

This is the implementation contract for package discovery and the subsequent
renderer. The [design](../../design/gateway-auth/gateway-authentication.md)
describes the user-facing API. Package discovery does not enable an auth type,
render policy or install admission resources.

## Package layout

Each configured source contains the same relative hierarchy:

```text
aigateway/governance/auth/<authType>/
  Chart.yaml
  values.schema.json
  README.md
  references.yaml          # optional: absent means no file dependencies
  templates/
    contribution.yaml
```

The directory name is the case-sensitive auth type ID. `Chart.yaml` supplies
the chart name and release version; its name need not equal the type ID. Auth
packages are Helm application charts with `apiVersion: v2`. Additional helpers,
default values and packaged files are retained for rendering.

Discovery merges named default/extension sources and rejects duplicate type IDs.
The caller marks whether a source's auth subtree is required. A missing optional
subtree contributes no packages; malformed packages are errors in every source.
Hidden projection bookkeeping entries are not packages. The caller must provide
a confined filesystem, such as `os.Root.FS()`, when reading mounted extensions.

The catalog copies package files into a startup snapshot. Replacing a mounted
file does not change a loaded package; a restart builds a new catalog. No Secret
contents or Kubernetes API calls are involved in discovery.

Discovery checks chart metadata, JSON schema syntax and reference declarations.
Full schema compilation and template validation belong to the renderer.
Admission resources are not required package artifacts.

## Secret declarations

`references.yaml` maps controller-facing file slot names to RFC 6901 JSON
Pointers into the flat authentication entry:

```yaml
secretFiles:
  keys: /keySource/secretRef
```

Each selected value must be a same-namespace Secret reference with `name` and
`key`. Pointers describe configuration fields, not filesystem paths. Escaped
tokens use `~0` for `~` and `~1` for `/`; malformed pointers and unknown declaration
fields are rejected. The catalog validates declarations; S05 resolves the actual
entry and authorizes its references.

A slot is a name for a template input, not a filesystem layout. The controller
derives its path as `/var/run/praxis/auth/secrets/<secret-name>/<data-key>`.
Slots selecting the same Secret/key receive the same path, regardless of auth
type or instance name. Content changes and same-name replacement retain paths.

The metadata-only plan keeps slot paths separate from projection requirements.
For each namespace/Secret, it records one read-only directory mount and the
sorted union of referenced keys, preserving each key as its filename. Unreferenced
keys are excluded. The controller chooses valid volume names and rejects reserved
volume/path conflicts; packages cannot request alternate filenames or layouts.
Actual mounts are deferred to core workload integration and must use directories
without `subPath`. The convention assumes one source namespace per workload,
matching the gateway's namespace.

A package needing no files omits this file or declares an empty `secretFiles`
map. HTTP-backed authentication therefore has no artificial verifier Secret
requirement. ConfigMap references are outside the first delivery.

## Render input

The renderer supplies two separate values objects:

```yaml
entry:
  name: consumers
  authType: apiKey
  header: X-API-Key
  keySource:
    secretRef:
      name: consumer-keys
      key: keys.yaml
context:
  gateway:
    name: team-alpha-gateway
    namespace: team-alpha
  pluginName: auth-consumers
  files:
    keys:
      path: /var/run/praxis/auth/secrets/consumer-keys/keys.yaml
```

`entry` is the flat public authentication object; these wrappers exist only in
Helm values. The controller constructs `context` independently. User fields or
chart defaults cannot replace its namespace, allocated plugin name or resolved
file paths. Templates consume the supplied path rather than reconstructing it
from the entry.

The package's `values.schema.json` validates this complete values object,
including its own entry fields and the context it consumes. Apply entry defaults
through `values.yaml` or template logic; a JSON Schema `default` annotation alone
does not populate values. Secret names/keys remain references in `entry`; Secret
bytes never enter either values object.

For example, the API-key template's provider path uses:

```gotmpl
path: {{ .Values.context.files.keys.path | quote }}
```

## Render output and assembly

The rendered `templates/contribution.yaml` contains three fields:

| Field | Contribution |
| --- | --- |
| `plugins` | PPE plugin declarations, including the allocated plugin name. |
| `authentication` | Names of the identity plugins to activate. |
| `stripHeaders` | Caller credential headers to remove before forwarding. |

S06 validates this structure and rejects other top-level fields, including
workload volumes/mounts or a replacement global policy. S11 owns document
assembly, the global authenticated-caller requirement and subsequent route/PDP
composition. The first milestone accepts one configured API-key instance;
this list is not an implementation of general OR selection.

## Admission resources

S07 supplies a shared AIGateway ValidatingAdmissionPolicy and enforcing binding
for the default auth types, initially `apiKey`. Platform administrators install
and maintain these cluster-scoped resources separately from the controller.
Extension owners provide validation rules for administrators to add to this VAP.

The controller neither manages admission resources nor checks their readiness.
Helm validation and Tenant grants are sufficient controller gates for now.
Default and extension owners maintain shared admission/render cases so the two
validation layers agree.

## Renderer selection and source checks

Select `helm.sh/helm/v3` at `v3.22.1`. Its module requires Go 1.26.0 and
uses Kubernetes 0.37 dependencies, matching the implementation target.
S04 introduces it for chart tests; S06 adds the production renderer.
The verified rendering path is:

1. `loader.LoadFiles` loads the catalog's buffered chart snapshot.
2. `chartutil.ToRenderValuesWithSchemaValidation` prepares values with
   `skipSchemaValidation` set to `false`.
3. An `engine.Engine` with strict mode renders without a Kubernetes client or
   DNS lookup capability.

Schemas must be self-contained for offline validation. S06 must reject external
schema references before calling Helm: the SDK's schema compiler otherwise
includes file/HTTP loaders and an unresolved-URN fallback. Rendering must neither
fetch dependencies nor gain access to cluster Secrets through Helm's `lookup`.

The default package explicitly configures a 5-second refresh and 30-second
staleness ceiling, with staleness at least refresh. These are package defaults:
PPE's file provider does not supply them when omitted. Its SHA-256 records,
`expires_at`, `record_map`, credential prefix and interval fields were checked
against PPE source revision `842f8250b4266e74f8970537ac884beaf4261778`.
This source check is not a runtime enforcement test.

The default chart accepts whole-second intervals from 1 to 9,007,199,254,740,991
(`2^53 - 1`). This package limit keeps values exact through Helm's numeric values
handling and within signed integer comparisons; PPE itself uses unsigned 64-bit
intervals. Rendering normalizes accepted numbers before comparing and emitting
the effective intervals.

Named `authType` entries and Tenant grants use exact, case-sensitive type IDs.
The default API-key package uses `apiKey`.

Sources:

- [Helm in-memory chart loading](https://github.com/helm/helm/blob/v3.22.1/pkg/chart/loader/load.go)
- [Helm values/schema preparation](https://github.com/helm/helm/blob/v3.22.1/pkg/chartutil/values.go)
- [Helm template engine](https://github.com/helm/helm/blob/v3.22.1/pkg/engine/engine.go)
- [Helm schema loaders](https://github.com/helm/helm/blob/v3.22.1/pkg/chartutil/jsonschema.go)
- [PPE file-directory contract](https://github.com/praxis-proxy/policy/blob/842f8250b4266e74f8970537ac884beaf4261778/crates/builtins/src/plugins/identity_api_key/file_directory.rs)
- [PPE API-key configuration](https://github.com/praxis-proxy/policy/blob/842f8250b4266e74f8970537ac884beaf4261778/crates/builtins/src/plugins/identity_api_key/config.rs)
