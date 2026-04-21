# Contributing ResourceSet Rules to BRO

This guide is for Rancher engineers who need their Kubernetes resources included in Rancher backups managed by the Backup Restore Operator (BRO). It covers the two most common actions — opting a secret in with a label, and adding selector rules via a PR — followed by a reference section that explains the underlying concepts in full.

> [!NOTE]
> If this is your first time working with BRO, please read [Background & Reference](#background--reference) first before attempting to write rules.

### Scope and expectations

Before diving in, a few facts that apply to every BRO backup and restore:

- **Local cluster only.** BRO only backs up Kubernetes resources on the local (Rancher management) cluster. Downstream clusters are not backed up — they appear in the backup only as resource objects that describe them in the local cluster. Their actual workloads and state are untouched.
- **Restores are all-or-nothing.** There is no partial restore. Restoring a backup reverses every change made since the backup was taken, including intentional ones.
- **Rancher versions must match.** When restoring, the Rancher version on the target cluster must match the version from which the backup was taken. BRO does not handle cross-version restores.
- **Restoring resources with external side effects does not replay those effects.** If a resource originally caused something to happen outside the cluster (e.g. provisioning a downstream cluster, creating cloud infrastructure), restoring that resource object does not re-trigger the action. The CR comes back; the external state does not.
- **Migration requires a clean cluster.** When migrating to a new cluster, Rancher must not already be running on the target. Install the same version of Rancher only _after_ the BRO restore completes. The Rancher domain must also remain the same — the old domain must be pointed at the new cluster.

---

## Quick Reference

### The `resources.cattle.io/backup` Opt-In Label

The fastest way to include a Secret in a full backup without touching BRO's chart is to label it:

```
resources.cattle.io/backup=true
```

Any Secret in any namespace carrying this label is automatically included in `rancher-resource-set-full` backups. Example:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-provider-credentials
  namespace: my-operator-namespace
  labels:
    resources.cattle.io/backup: "true"
data:
  ...
```

This works because the full ResourceSet already contains a catch-all selector in `files/default/sensitive-resourceset-contents/rancher.yaml`:

```yaml
- apiVersion: "v1"
  kindsRegexp: "^secrets$"
  namespaceRegexp: "^.*$"
  labelSelectors:
    matchExpressions:
      - key: "resources.cattle.io/backup"
        operator: "In"
        values: ["true"]
```

> **Scope:** this label applies only to Secrets, and only in `rancher-resource-set-full`. For non-secret resources, add a `ResourceSelector` rule in a chart file (see below).

---

### Adding Rules for a New Feature

The typical workflow for a Rancher feature team:

1. Identify the API group(s) your feature introduces (e.g. `turtles.rancher.io/v1`, `cluster.x-k8s.io/v1beta2`).
2. Decide which resources are non-secret (config, CRD definitions, CR instances) and which are secret (credentials, kubeconfigs, tokens).
3. Create or update the appropriate YAML file:
   - `charts/rancher-backup/files/default/basic-resourceset-contents/<feature>.yaml` — non-secrets, included in both ResourceSets
   - `charts/rancher-backup/files/default/sensitive-resourceset-contents/<feature>.yaml` — secrets, included in the full ResourceSet only
4. Open a PR against the BRO repository targeting the `fix-gha` branch.

#### Example: backing up all CAPI resources

`charts/rancher-backup/files/default/basic-resourceset-contents/turtles.yaml`

```yaml
# Back up all CRDs registered by CAPI / Turtles
- apiVersion: "apiextensions.k8s.io/v1"
  kindsRegexp: "."
  resourceNameRegexp: "cluster.x-k8s.io$|turtles.rancher.io$"

# Back up all CAPI cluster objects
- apiVersion: "cluster.x-k8s.io/v1beta1"
  kindsRegexp: "."

# Back up all Turtles-managed objects
- apiVersion: "turtles.rancher.io/v1"
  kindsRegexp: "."
```

#### Example: backing up CAPI secrets

`charts/rancher-backup/files/default/sensitive-resourceset-contents/turtles.yaml`

```yaml
# Machine credentials created by the CAPI infrastructure provider
- apiVersion: "v1"
  kindsRegexp: "^secrets$"
  namespaceRegexp: "^rancher-turtles-"
  labelSelectors:
    matchExpressions:
      - key: "cluster.x-k8s.io/cluster-name"
        operator: "Exists"
```

#### Example: excluding a noisy resource type

```yaml
- apiVersion: "turtles.rancher.io/v1"
  kindsRegexp: "."
  excludeKinds:
    - "capiproviders"     # runtime-derived, no need to back up
```

#### Example: backing up only specific named resources

```yaml
- apiVersion: "turtles.rancher.io/v1"
  kindsRegexp: "^capiprovidertemplates$"
  resourceNames:
    - "aws-provider"
    - "azure-provider"
```

---

### Testing Your Changes Locally

Before opening a PR, preview exactly which resources your new selectors would match using the `bro-tool resource-set:view` subcommand:

```sh
# from the repo root, build the chart first
make package-helm

# view selectors from the built chart
bro-tool resource-set:view --path build/charts/rancher-backup

# or target a specific published version
bro-tool resource-set:view --version 104.0.2+up4.0.2

# output as YAML or JSON instead of the default table
bro-tool resource-set:view --path build/charts/rancher-backup --output yaml
```

This prints the rendered `ResourceSelector` list and the source file each rule came from, which is useful for verifying that your new file was picked up and that the selectors look correct.

---

---

## Background & Reference

The sections below explain the full picture: what a ResourceSet is, how the collection algorithm works, and how the BRO chart is structured. Useful context before writing rules for the first time, or when debugging unexpected backup behavior.

---

### What is a ResourceSet?

A `ResourceSet` is a cluster-scoped custom resource (`resources.cattle.io/v1`) that tells BRO _which_ Kubernetes resources to include in a backup. A `Backup` object references a `ResourceSet` by name; BRO then collects everything that the ResourceSet describes and writes it to the backup archive.

```yaml
apiVersion: resources.cattle.io/v1
kind: ResourceSet
metadata:
  name: my-resource-set
resourceSelectors:
  - apiVersion: "management.cattle.io/v3"
    kindsRegexp: "."
controllerReferences: []
```

`resourceSelectors` is a list of `ResourceSelector` objects. Each one narrows down _what_ to collect from a single API group/version.

`controllerReferences` allows BRO to track a controller deployment's replica count so it can scale it down before restoring and back up after — this is used for Rancher and BRO, it is not something feature teams normally need to change.

---

### ResourceSelector — Field Reference

Each `ResourceSelector` targets **exactly one** `apiVersion` and then further filters by kind, name, namespace, labels, and fields. The Go type is:

```go
type ResourceSelector struct {
    APIVersion                string                // required — e.g. "management.cattle.io/v3"
    Kinds                     []string              // exact kind names (OR'd with KindsRegexp)
    KindsRegexp               string                // regex matched against kind names
    ResourceNames             []string              // exact resource names (OR'd with ResourceNameRegexp)
    ResourceNameRegexp        string                // regex matched against resource names
    Namespaces                []string              // exact namespaces (OR'd with NamespaceRegexp)
    NamespaceRegexp           string                // regex matched against namespace names
    LabelSelectors            *metav1.LabelSelector // standard k8s label selector
    FieldSelectors            fields.Set            // field-based filter (e.g. type=rke.cattle.io/machine-plan)
    ExcludeKinds              []string              // kinds to skip even if matched above
    ExcludeResourceNameRegexp string                // resource names to skip even if matched above
}
```

#### OR within a field pair, AND across fields

The two fields for each dimension (list + regexp) are **OR**'d together. Separate dimensions are **AND**'d. For example:

```yaml
- apiVersion: "v1"
  kindsRegexp: "^serviceaccounts$"
  resourceNameRegexp: "^cattle-|^rancher-"
  resourceNames:
    - "default"
  namespaceRegexp: "^cattle-system$"
```

Breaking this down:
- `kindsRegexp` selects only ServiceAccounts from the `v1` group.
- `resourceNameRegexp` and `resourceNames` are **OR**'d: the selector matches any ServiceAccount whose name starts with `cattle-` or `rancher-`, **plus** the one named exactly `default`.
- `namespaceRegexp` is **AND**'d with the name filter: only ServiceAccounts in the `cattle-system` namespace are considered. A ServiceAccount named `cattle-controller` in `kube-system` would be excluded.

#### Kinds

`Kinds` and `KindsRegexp` select resource _types_ (plural, lowercase) within the given API version. Both are OR'd:

```yaml
- apiVersion: "rbac.authorization.k8s.io/v1"
  kindsRegexp: "^roles$|^rolebindings$"
```

To match every kind in a group version, use `kindsRegexp: "."` (any non-empty string).

#### Label Selectors

Standard Kubernetes `matchLabels` / `matchExpressions` syntax applies. Labels are pushed down to the API server list call, so they are efficient even for large clusters.

```yaml
- apiVersion: "v1"
  kindsRegexp: "^configmaps$"
  namespaceRegexp: "^cattle-"
  labelSelectors:
    matchExpressions:
      - key: "cattle.io/kind"
        operator: "In"
        values: ["kubeconfig"]
```

#### Field Selectors

Field selectors filter on resource fields (typically `type` for Secrets). Unlike label selectors they are not indexed on most resources, but for Secrets the `type` field is supported:

```yaml
- apiVersion: "v1"
  kindsRegexp: "^secrets$"
  namespaces:
    - "fleet-default"
  fieldSelectors:
    "type": "rke.cattle.io/machine-plan"
```

#### Exclusions

`excludeKinds` and `excludeResourceNameRegexp` let you carve out exceptions within an otherwise broad selector:

```yaml
- apiVersion: "management.cattle.io/v3"
  kindsRegexp: "."          # everything in this API group...
  excludeKinds:
    - "tokens"              # ...except tokens (handled by a separate, narrower selector)
    - "rancherusernotifications"
```

---

### How the Collector Works (`GatherResources`)

When a backup runs, BRO calls `GatherResources` which iterates over every `ResourceSelector` in the referenced `ResourceSet`:

1. **Discover kinds** — uses the Kubernetes discovery API to list all resource types registered for the selector's `apiVersion`, then filters them by `Kinds`/`KindsRegexp` (and `ExcludeKinds`).
2. **Fetch objects** — for each matched resource type, calls the API server's list endpoint. `LabelSelectors` and `FieldSelectors` are pushed to this call so the server does the filtering.
3. **Filter by name** — the returned items are filtered client-side by `ResourceNames`/`ResourceNameRegexp` (OR'd) and then `ExcludeResourceNameRegexp`.
4. **Filter by namespace** — if the resource type is namespaced and the selector specifies `Namespaces`/`NamespaceRegexp`, only items in matching namespaces are kept.
5. **Accumulate** — results are merged by `GroupVersionResource` into the handler's object map. If two selectors in the same ResourceSet match the same resource, the object is included only once (deduplication happens at the GVR level via map keys).

Subresources (paths containing `/`, e.g. `pods/log`) are always skipped. Resources without `list` or `get` verbs are also skipped with a log message.

> **Implication for feature teams:** if your CRDs live under a new API group (e.g. `turtles.rancher.io/v1`) you need at least one `ResourceSelector` for that group. BRO will not discover your resources automatically.

---

### The Two Default ResourceSets

The BRO chart ships two `ResourceSet` objects. Which one is used for a backup is determined by the `Backup.spec.resourceSetName` field.

| ResourceSet name | Includes secrets? | Use when |
|---|---|---|
| `rancher-resource-set-basic` | No | Backup destination is unencrypted, or a secrets-free snapshot is sufficient |
| `rancher-resource-set-full` | Yes | Full Rancher backup including credentials and cluster state |

**`rancher-resource-set-full`** collects everything in `basic` plus the contents of `files/default/sensitive-resourceset-contents/`, which adds Secret selectors for machine-driver credentials, RKE cluster state, provisioning tokens, and so on.

#### Chart file layout

```
charts/rancher-backup/files/
├── default/
│   ├── basic-resourceset-contents/     # non-secret selectors for both ResourceSets
│   │   ├── rancher.yaml                # core Rancher management resources
│   │   ├── fleet.yaml                  # Fleet / GitOps resources
│   │   ├── provisioningv2.yaml         # CAPI / RKEv2 CRDs and objects
│   │   ├── eks.yaml                    # EKS cloud-provider resources
│   │   ├── aks.yaml                    # AKS cloud-provider resources
│   │   ├── ali.yaml                    # Alibaba Cloud provider resources
│   │   ├── gke.yaml                    # GKE cloud-provider resources
│   │   ├── rancher-operator.yaml       # rancher-operator deployment and RBAC
│   │   └── elemental.yaml
│   └── sensitive-resourceset-contents/ # secret selectors — full ResourceSet only
│       ├── rancher.yaml                # Rancher-owned secrets (+ opt-in label selector)
│       ├── fleet.yaml                  # Fleet secrets
│       ├── provisioningv2.yaml         # Machine-plan, cluster-state, machine-state secrets
│       └── elemental.yaml
└── optional/                           # feature-gated; enabled via values.optionalResources
    ├── basic-resourceset-contents/
    │   └── kubewarden.yaml
    └── sensitive-resourceset-contents/
        └── kubewarden.yaml
```

Each `.yaml` file under these directories is a plain YAML list of `ResourceSelector` objects (no document header, no `apiVersion`/`kind` — just the list items). Helm inlines them directly into the `resourceSelectors:` list of the rendered ResourceSet.

---

### Restore Scenarios

BRO restores fall along two independent axes. Understanding both is important context for the limitations described in the next section.

#### Infrastructure axis: in-place vs migration

**In-place restore** — the target Kubernetes cluster is already running Rancher. BRO scales Rancher down before restoring objects and scales it back up when done. The Rancher system charts (Fleet, Webhook, etc.) remain installed throughout; Rancher reconciles them back to a healthy state on the way up.

**Migration restore** — the target is either a fresh Kubernetes cluster or one where the nodes have been wiped. Rancher is not present during the BRO restore phase. System-chart Helm release objects are restored as raw Kubernetes objects, and Rancher must be installed afterwards to drive everything to a healthy state.

#### Version axis: same-version vs rollback

**Same-version restore** — the Rancher version on the target cluster matches the version from which the backup was taken. This is the recommended and best-supported path. Rancher's system chart reconciler targets the same versions that were in the backup, so any post-restore reconciliation converges cleanly.

**Cross-version restore** is the umbrella term for any restore where the Rancher version running on the target does not match the version from which the backup was taken. The distinction matters because of how Rancher behaves on startup:

- **Same-version restore (ideal):** Rancher wakes up and performs normal reconciliation — equivalent to the pod being restarted or scaled to zero for a period. It expects the world to look roughly as it left it, just potentially stale.
- **Cross-version restore:** Rancher wakes up and must also perform upgrade-related tasks on top of reconciliation — migrating data, updating CRD schemas, rewriting stored objects. These two concerns were designed to happen independently; a cross-version restore forces them to happen simultaneously and in an order Rancher was not designed to handle.

The further apart the versions, the larger the gap between what Rancher finds in the restored cluster and what it expects to see on startup, and the higher the risk of failure. This complexity is why cross-version restores are not officially supported — BRO can restore the objects, but it cannot control or compensate for what Rancher does when it wakes up against a world it wasn't expecting.

**Version rollback** — the backup was taken from an older Rancher version than the one currently installed on the target. This is not officially supported but is attempted in practice (typically to recover from a failed upgrade). It introduces additional failure modes — most significantly around CRD API version changes — that do not exist in same-version restores.

Version rollbacks require extra steps beyond triggering a BRO restore, and those steps differ by infrastructure axis:

- **Migration rollback** — after BRO restore completes, Rancher must be installed at the version matching the backup, not the latest available version. Installing the wrong version will cause Rancher to reconcile system charts to the wrong target state.
- **In-place rollback** — Rancher must be downgraded to the backup version (via `helm upgrade` to the target version) before triggering the BRO restore. If the rollback crosses a CRD API version boundary, a clean in-place downgrade is not possible; the recommended workaround is to wipe the cluster and treat it as a migration restore instead (see [CRD API version changes and rollback](#crd-api-version-changes-and-rollback)).

BRO does not validate that the installed Rancher version matches the backup — this check is the operator's responsibility.

#### The full matrix

| | Same-version | Rollback |
|---|---|---|
| **In-place** | Well supported | Risky — see [CRD API version changes](#crd-api-version-changes-and-rollback) |
| **Migration** | Well supported | Risky — same CRD issues; no Rancher present to assist recovery |

BRO does not enforce version matching and does not auto-detect which scenario applies. The correct `Restore` CR options (e.g. `prune`, `forceConcurrentRestore`) depend on the scenario, and selecting them is currently left to the operator. The limitations below call out which scenario each issue applies to.

---

### Known Limitations / Open Issues

#### In-place rollback pruning

The `Restore.spec.prune` option deletes objects found in the live cluster that are not present in the backup. Resources must be covered by the ResourceSet for them to be considered during pruning — resources outside the ResourceSet are invisible to the pruner and will not be removed even if they are stale after a rollback. If your feature introduces cluster-scoped resources that need to be cleaned up on rollback, ensure they are included in the ResourceSet.

A consequence of this is that **partial restores are not possible**. Restoring a backup to recover from one undesired change will also reverse every other change that occurred between the backup timestamp and now — including desired changes to clusters, projects, or any other backed-up resources. There is no mechanism to cherry-pick individual objects from a backup.

#### System-charts and the Rancher startup ordering problem

Rancher manages its system charts (Fleet, Webhook, etc.) via Helm, reconciling them when the `rancher-charts` ClusterRepo changes or on a periodic timer (default 6 hours). This creates a layered interaction with BRO restores that feature teams should understand:

- **In-place restore:** BRO scales down the Rancher deployment (driven by the `controllerReferences` field on the ResourceSet — see [What is a ResourceSet?](#what-is-a-resourceset)). BRO restores raw Kubernetes objects from the backup — it does not invoke Helm. BRO also intentionally skips restoring the `rancher` and `rancher-webhook` deployments in `cattle-system` themselves, since those are expected to be reconciled by Helm/Rancher on the way back up rather than overwritten by BRO. Once BRO finishes and scales Rancher back up, Rancher reconciles system charts to match the currently installed Rancher version.
- **Migration (restore to a new cluster):** Rancher is not present at all during the BRO restore phase. The system-chart Helm release objects are restored as raw Kubernetes objects, but Rancher must come up afterwards to drive the charts to a healthy state.

In both cases, the system charts are effectively reconciled by Rancher _after_ BRO completes, not _by_ BRO.

#### CRD API version changes and rollback

The most severe limitation arises when a Rancher version change also changes the API version of a CRD (e.g. `v1alpha1` → `v1beta1`). In this scenario:

- The live cluster has the new CRD version and new CRs.
- The backup contains the old CRD version and old CRs.
- BRO restores the old CRD, but the cluster may have existing CRs stored under the new API version in etcd.
- Rancher only starts _after_ BRO finishes, meaning it cannot orchestrate the downgrade of system-chart CRDs as part of the restore sequence.

There is no clean automated path through this today. **The current workaround for an in-place rollback that crosses a CRD API version boundary is a full cluster wipe followed by a restore from backup.**

When a wipe-and-restore is performed, the cluster cleanup step is handled by the [rancher/rancher-cleanup](https://github.com/rancher/rancher-cleanup) scripts. These scripts must explicitly enumerate the CRDs and resources to remove — if a CRD API version changes and the cleanup script is not updated, stale resources from the old version will survive the wipe and pollute the restored cluster, undermining the entire rollback.

> **Action required for CRD owners:** if your feature introduces or changes a CRD API version across Rancher releases, you must also update the rancher-cleanup scripts to cover both the old and new CRD versions and any associated resources. Failure to do so means the wipe-and-restore workaround will not fully work for users of your feature.

If your feature introduces CRDs that may change API versions across Rancher releases, flag this with the BRO maintainers early so the restore path can be considered.

An attempt was made to address this in [rancher/backup-restore-operator#911](https://github.com/rancher/backup-restore-operator/pull/911) by merging CRDs during restore — preserving the new API versions already in the cluster while also restoring the old ones, so that backed-up CRs could be applied against the original API. This is not a complete fix; in-place restores still have known issues in the CRD version change case.

A follow-on RFE tracking a proper solution is [rancher/backup-restore-operator#924](https://github.com/rancher/backup-restore-operator/issues/924). The core problem it addresses: the CRD merge approach adds the new API version to `spec.storedVersions` with `storage: false`, producing a CRD that differs from the original. If Rancher is upgraded again, controllers may not be prepared to handle that modified CRD state.

#### Rancher Webhook blocking restores

During an in-place restore BRO scales Rancher down, but the Rancher Webhook continues running. Resources that changed shape between Rancher versions (e.g. fields that became immutable in the newer version) can be blocked by the Webhook when BRO attempts to restore them to their older form, even though restoring that older form is the entire point of the operation. The Webhook has no context that a BRO restore is in progress.

The established `rancher-webhook-sudo` ServiceAccount bypass exists (`cattle-system/rancher-webhook-sudo`, granting `system:masters`) and Rancher uses it internally to bypass Webhook validation for certain privileged operations. However it is not usable by BRO today: the SA is created by the rancher-webhook Helm chart itself, so in a cluster migration where neither Rancher nor its system charts are present at restore time, the SA would not exist and BRO would fail trying to impersonate it.

The tracked RFE is [rancher/backup-restore-operator#836](https://github.com/rancher/backup-restore-operator/issues/836). The proposed direction is for BRO to ship its own dedicated "sudo" ServiceAccount and for the Webhook to extend its bypass logic to recognise it, covering both in-place restores and migrations cleanly.

If your feature adds Webhook validation that enforces immutability or strict version constraints on fields that BRO would need to overwrite during a rollback, coordinate with both the BRO and Webhook maintainers to ensure the restore path is considered.

#### Restore ordering and cross-group dependencies

BRO restores objects in three strict phases (verified in `pkg/controllers/restore/controller.go`):

1. **CRDs** — all CRDs are applied first, then BRO polls each one until it reaches `Established` before proceeding.
2. **Cluster-scoped resources** — applied in dependency order via an owner-reference graph.
3. **Namespaced resources** — applied in dependency order via a separate owner-reference graph.

Dependencies _within_ each phase are handled by the graph. Dependencies _across_ phases (e.g. a cluster-scoped CR that owns a namespaced resource, or a namespaced resource whose CRD depends on another CRD being fully established) can still cause ordering failures if the graph doesn't capture them. Prefer designs where cluster-scoped CRs reference namespaced objects rather than the reverse.

#### Resources with external side effects

Restoring a resource that originally caused something to happen outside the cluster does not re-trigger that action. The most common case is CAPI / provisioned downstream clusters: restoring the cluster CR does not cause Rancher to re-provision the machines or re-import the cluster. Fleet clusters require manual reimport to reissue service account tokens. If your feature creates resources that drive external state, assume that state will not be recovered by BRO alone and document the manual recovery steps for your users.
