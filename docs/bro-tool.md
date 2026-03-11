# bro-tool

> **No Warranty.** `bro-tool` is an unofficial companion utility provided **as-is, without warranty of any kind**. It is not part of the Rancher Backup and Restore Operator (BRO) product and is not covered by SUSE/Rancher support agreements. Issues and bugs may be filed in the project repository but carry no SLA. See [Disclaimer](#disclaimer) for full terms.

`bro-tool` is a command-line utility for Rancher/SUSE support engineers working with the [Backup and Restore Operator (BRO)](../README.md). It lets you inspect BRO's ResourceSet configuration and answer common support questions **without needing a live cluster**.

## Table of Contents

- [When to Use bro-tool](#when-to-use-bro-tool)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Commands](#commands)
  - [resource-set:view](#resource-setview)
  - [resource-set:check](#resource-setcheck)
- [Common Support Workflows](#common-support-workflows)
- [Understanding the Output](#understanding-the-output)
- [Disclaimer](#disclaimer)
- [Contributing / Development](#contributing--development)

---

## When to Use bro-tool

Use `bro-tool` when a customer or colleague asks questions like:

- *"Is my `Deployment` in namespace `cattle-fleet-system` covered by BRO backups?"*
- *"Which rule in the ResourceSet captures my custom resource?"*
- *"Did BRO v2.1.0 back up `ManagedChart` resources?"*
- *"What changed between BRO v2.0.0 and v2.1.0 in terms of ResourceSet coverage?"*

The tool answers these questions **offline** — it fetches or reads the BRO helm chart and analyzes its bundled ResourceSets, without connecting to a cluster.

---

## Installation

`bro-tool` is a Go binary. Build it from source using the Go toolchain (Go 1.22+):

```bash
git clone https://github.com/rancher/backup-restore-operator.git
cd backup-restore-operator/cmd/tool
go build -o bro-tool .
```

Then move the binary somewhere on your `$PATH`:

```bash
mv bro-tool /usr/local/bin/
```

Verify the installation:

```bash
bro-tool --version
```

### Versioning

Each release of `bro-tool` is built and versioned alongside BRO. Keep the tool version aligned with the BRO version you are investigating:

| Scenario | Supported? |
|---|---|
| Same `bro-tool` version as BRO | Fully supported |
| Newer `bro-tool`, older BRO | Generally works; logical compatibility maintained |
| Older `bro-tool`, newer BRO | Supported within the same major version only |

When in doubt, build the tool from the same tag as the BRO version you are troubleshooting.

---

## Quick Start

View all ResourceSet rules for BRO v9.0.0 (fetches chart automatically from GitHub):

```bash
bro-tool resource-set:view --version v9.0.0
```

Check whether a specific resource is covered by the backup:

```bash
bro-tool resource-set:check --version v9.0.0 --resource ManagedChart/my-chart --namespace fleet-default
```

Check against a locally-built chart:

```bash
bro-tool resource-set:view --path ./build/charts/rancher-backup --output yaml
```

---

## Commands

### resource-set:view

**Purpose:** Display every ResourceSet rule shipped with a BRO chart version. Useful for understanding exactly what a backup will capture.

```
bro-tool resource-set:view [flags]
```

**Flags:**

| Flag | Description |
|---|---|
| `--version <v>` | BRO chart version to fetch from GitHub (e.g. `v9.0.0`). Cached locally after first download. |
| `--path <dir>` | Path to a local `rancher-backup` helm chart directory or `.tgz` file. |
| `--output <fmt>` | Output format: `table` (default), `yaml`, or `json`. |

`--version` and `--path` are mutually exclusive; exactly one is required.

**Examples:**

```bash
# View all rules for v9.0.0 as a table (default)
bro-tool resource-set:view --version v9.0.0

# View as YAML (useful for saving/diffing)
bro-tool resource-set:view --version v9.0.0 --output yaml

# View a locally built chart
bro-tool resource-set:view --path ./build/charts/rancher-backup --output json
```

**Table output columns:**

| Column | Meaning |
|---|---|
| `ResourceSet` | Name of the ResourceSet (e.g. `rancher-resource-set-full`) |
| `Source` | The chart source file that defines this rule (e.g. `aks.yaml`, `fleet.yaml`) |
| `APIVersion` | API group/version matched by this rule |
| `Kinds` | Kubernetes kinds matched (comma-separated; `*` means all kinds in this API version) |
| `Names` | Resource names matched (`*` = any; `~pattern` = regex) |
| `Namespaces` | Namespaces matched (`*` = any or cluster-scoped; `~pattern` = regex) |

---

### resource-set:check

**Purpose:** Check whether a specific Kubernetes resource would be included in a BRO backup. Reports all matching ResourceSet rules and any caveats about conditions that could not be verified offline.

```
bro-tool resource-set:check [flags]
```

**Flags:**

| Flag | Description |
|---|---|
| `--version <v>` | BRO chart version to fetch from GitHub (e.g. `v9.0.0`). |
| `--path <dir>` | Path to a local `rancher-backup` helm chart directory or `.tgz` file. |
| `--resource <kind/name>` | Resource to check, in `kind/name` format (e.g. `Deployment/my-app`). |
| `--resource-path <file>` | Path to a YAML/JSON resource manifest. Kind, name, namespace, and labels are read from the file. |
| `--namespace <ns>` | Namespace of the resource. Required for namespace-scoped resources when using `--resource`. Overrides the value in `--resource-path` if both are provided. |
| `--api-version <gv>` | API group/version (e.g. `apps/v1`). Inferred automatically for well-known Kubernetes kinds. Overrides the value in `--resource-path`. |
| `--output <fmt>` | Output format: `table` (default) or `json`. |

`--version`/`--path` are mutually exclusive (exactly one required).
`--resource`/`--resource-path` are mutually exclusive (exactly one required).

**Exit codes:**

| Code | Meaning |
|---|---|
| `0` | At least one matching rule was found |
| `1` | No matching rule found (resource is NOT covered) |

This makes `resource-set:check` safe to use in scripts.

**Examples:**

```bash
# Check a namespaced resource by kind/name
bro-tool resource-set:check --version v9.0.0 \
  --resource Deployment/my-app \
  --namespace cattle-fleet-system

# Check a cluster-scoped resource (no namespace needed)
bro-tool resource-set:check --version v9.0.0 \
  --resource ClusterRole/my-cluster-role

# Check using a manifest file (namespace and labels read from file)
bro-tool resource-set:check --version v9.0.0 \
  --resource-path ./my-deployment.yaml

# Check with an explicit API version (for CRDs or ambiguous kinds)
bro-tool resource-set:check --version v9.0.0 \
  --resource ManagedChart/my-chart \
  --namespace fleet-default \
  --api-version management.cattle.io/v3

# Output as JSON for scripting
bro-tool resource-set:check --version v9.0.0 \
  --resource ConfigMap/rancher-config \
  --namespace cattle-system \
  --output json

# Use in a shell script
if bro-tool resource-set:check --version v9.0.0 --resource Secret/my-secret --namespace default; then
  echo "Covered by backup"
else
  echo "NOT covered — customer data at risk"
fi
```

**Table output columns:**

| Column | Meaning |
|---|---|
| `ResourceSet` | Name of the ResourceSet containing the matching rule |
| `#` | Rule index within the ResourceSet (0-based) |
| `Source` | Chart source file that defines the rule |
| `APIVersion` | API group/version in the rule |
| `Kinds` | Kinds matched by the rule |
| `Names` | Resource name filter in the rule |
| `Namespaces` | Namespace filter in the rule |
| `Caveats` | Conditions that could NOT be verified offline (see below) |

#### Understanding Caveats

Some matching conditions require live cluster data that `bro-tool` cannot access:

| Caveat | Meaning |
|---|---|
| `namespace filter not checked` | The rule filters by namespace but no `--namespace` was provided; match may be partial |
| `label selector not checked` | The rule uses a label selector but no labels were provided; actual match may differ |

A result with caveats means the rule *might* match at runtime but could not be fully verified. When caveats are present, use `--resource-path` with a full manifest (including labels) or `--namespace` to reduce ambiguity.

---

## Common Support Workflows

### "Is this resource covered by the backup?"

```bash
bro-tool resource-set:check --version <bro-version> \
  --resource <Kind/name> \
  --namespace <namespace>
```

If the exit code is 1 (no match), the resource is **not** in the backup. Check the `Caveats` column — if there are unresolved caveats, the answer may be "it depends on labels or namespace content at runtime."

### "What does BRO back up from AKS / EKS / GKE clusters?"

```bash
bro-tool resource-set:view --version <bro-version> | grep -i aks
bro-tool resource-set:view --version <bro-version> --output yaml | grep -A20 "source: aks"
```

The `Source` column maps each rule back to a named file within the chart (e.g. `aks.yaml`, `eks.yaml`, `fleet.yaml`). This tells you exactly which optional component a rule belongs to.

### "What changed in ResourceSet coverage between two BRO versions?"

```bash
bro-tool resource-set:view --version v8.0.0 --output yaml > v8.yaml
bro-tool resource-set:view --version v9.0.0 --output yaml > v9.yaml
diff v8.yaml v9.yaml
```

### "Customer says their resource wasn't restored — is it covered?"

1. Get the resource kind, name, namespace, and BRO version from the customer.
2. Run `resource-set:check` to confirm whether the resource should have been included.
3. If covered (exit 0): investigate the backup/restore logs and object storage — the issue is likely elsewhere.
4. If not covered (exit 1): the resource was never in scope — explain to the customer and escalate if a new ResourceSet rule is needed.

### "Check a resource using a manifest the customer sent"

```bash
# Save the customer's manifest to /tmp/resource.yaml, then:
bro-tool resource-set:check --version <bro-version> --resource-path /tmp/resource.yaml
```

Labels in the manifest are used for label selector matching.

---

## Understanding the Output

### Chart caching

When `--version` is used, the helm chart `.tgz` is downloaded from GitHub Releases and cached in `~/.cache/bro-tool/charts/`. Subsequent runs with the same version use the cached copy. To force a re-download, delete the cached file.

### API version inference

For well-known Kubernetes kinds (e.g. `Deployment`, `ConfigMap`, `Secret`, `ClusterRole`), the tool automatically infers the canonical API version (e.g. `apps/v1`, `v1`, `rbac.authorization.k8s.io/v1`). For CRDs or non-standard resources, provide `--api-version` explicitly.

### Regex filters

ResourceSet rules that use regular expressions are shown with a `~` prefix in table output (e.g. `~rancher-.*`). These match via Go's `regexp` package.

### `*` wildcard

An asterisk (`*`) in the `Kinds`, `Names`, or `Namespaces` column means the rule has no filter for that field — it matches all values.

---

## Disclaimer

`bro-tool` is provided **as-is, without warranty of any kind, express or implied**, including but not limited to warranties of merchantability, fitness for a particular purpose, or non-infringement.

- `bro-tool` is **not** a supported SUSE/Rancher product.
- It is **not** covered by any support agreement or SLA.
- It may produce incorrect results, especially for CRDs or resources with complex label selectors.
- Matching logic is a best-effort offline approximation of the operator's runtime behavior — it is not guaranteed to be identical.
- Use the results of this tool to guide investigation, not as definitive proof that a resource was or was not backed up.

Bug reports and contributions are welcome via the project's GitHub repository.

---

## Contributing / Development

This section is for developers working on `bro-tool` itself.

### Module layout

`bro-tool` lives in `cmd/tool/` as a **separate Go module** (`github.com/rancher/backup-restore-operator/cmd/tool`). This isolation is intentional:

- Tool-only dependencies (Helm SDK, etc.) go in `cmd/tool/go.mod` **only** — never in the root module.
- The tool imports the parent BRO module via a `replace` directive in its `go.mod`.
- `bro-tool` code is a consumer of BRO's types, not a public API.

### Key packages

| Package | Purpose |
|---|---|
| `internal/chart/fetch.go` | Downloads and caches chart `.tgz` from GitHub Releases |
| `internal/chart/render.go` | Loads chart (dir or `.tgz`) and renders ResourceSets via Helm Go SDK |
| `internal/chart/match.go` | Offline resource matching logic; returns all matches with caveats |
| `internal/cmd/resourcesetview/` | `resource-set:view` subcommand |
| `internal/cmd/resourcesetcheck/` | `resource-set:check` subcommand |

### Building

```bash
# Build everything (operator + bro-tool) from the repo root:
make build
# Output: bin/backup-restore-operator and bin/bro-tool

# Or build just bro-tool from the repo root via workspace:
go build -C cmd/tool -o bin/bro-tool .
```

### Adding a new subcommand

All subcommands share the same core workflow:

1. Parse flags; validate inputs.
2. Resolve chart path: call `chart.FetchChartByVersion(version)` or use `--path` directly.
3. Load and render: call `chart.LoadAndRenderResourceSets(chartPath)` → `[]*chart.AnnotatedResourceSet`.
4. Do subcommand-specific work on the returned ResourceSets/selectors.
5. Dispatch from `main.go`.

See `internal/cmd/resourcesetview/cmd.go` as the simplest reference implementation.

### Chart notes for local development

- Use `build/charts/rancher-backup` (the **built** chart), not `charts/rancher-backup` (source). The source chart contains an unrendered `%TAG%` placeholder that is invalid YAML.
- Build the chart with: `make package-helm` (runs `scripts/package-helm`).
- `loader.Load()` from `helm.sh/helm/v3` accepts both directory paths and `.tgz` archives.

### Source attribution

`AnnotatedResourceSet` (in `render.go`) wraps a `*v1.ResourceSet` with `SelectorSources []string` — one source filename per selector. Sources are derived by parsing the chart's `files/**/*.yaml` static files and fingerprinting each `ResourceSelector` via `json.Marshal`. This gives per-rule attribution in table output without polluting YAML/JSON output.

### Dev rules

- No bro-tool dependency may be added to the root module or `tests/` module.
- `bro-tool` code is not considered a public API; it is not reusable outside this tool.
- The tool is intended for development, testing, and debugging — not production automation.
