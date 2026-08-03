# Backup and Restore Operator — automation-core

This branch hosts the reusable GitHub Actions workflows and composite actions for `backup-restore-operator`. Instead of every release branch keeping its own copy of CI, they call back into the workflows defined here. Update something once, here, and every branch calling in picks it up on its next run.

## Directory Structure

```
automation-core/
├── actions/                    # Composite actions (at root, not .github/actions)
│   ├── build-deps/
│   ├── test-deps/
│   └── release/
├── .github/
│   ├── workflows/              # Reusable workflows
│   │   ├── ci.yaml
│   │   └── head-builds.yaml
│   └── scripts/                # Shared scripts
│       ├── check-semver.sh
│       └── branch-tags.sh
└── README.md
```

**Note**: Actions are at the root `actions/` directory for cleaner paths. Reference them as `rancher/backup-restore-operator/actions/{name}@automation-core`.

## Workflows

- `ci.yaml` — lint, build, and integration test. Takes required inputs:
  - `k3s_versions` (JSON array as a string) - K3S versions for integration tests
  - `go-version` (string) - Go version to use (e.g., "1.26", "1.27")
- `head-builds.yaml` — builds and pushes the prerelease image.

## Calling a workflow from a release branch

Keep the real trigger on the release branch, and delegate the jobs here:

```yaml
name: Backup Restore CI

on:
  push:
    branches: [main, release/v10.x]
  pull_request:

jobs:
  ci:
    uses: rancher/backup-restore-operator/.github/workflows/ci.yaml@automation-core
    with:
      k3s_versions: '["v1.34.5-k3s1", "v1.36.1-k3s1"]'
      go-version: "1.26"
    permissions:
      contents: read
    secrets: inherit
```

`head-builds.yaml` works the same way — same `uses:` pattern, different workflow file.

## Actions

### Atomic Actions (for release branches)

These focused actions can be composed together in release branch workflows:

#### `check-semver`
Parses semantic version characteristics from a tag.

**Inputs:**
- `tag` (optional, default: `${{ github.ref_name }}`)

**Outputs:**
- `HAS_PRERELEASE` - Whether the tag has a prerelease identifier
- `HAS_BUILD_META` - Whether the tag has build metadata

**Example:**
```yaml
- uses: rancher/backup-restore-operator/actions/check-semver@automation-core
  id: semver_check
  with:
    tag: ${{ github.ref_name }}
```

#### `compute-branch-tags`
Calculates branch tags for head builds.

**Outputs:**
- `branch_tag` - The branch tag for this build
- `branch_static_tag` - The current static branch tag
- `prev_static_tag` - The previous static branch tag

**Example:**
```yaml
- uses: rancher/backup-restore-operator/actions/compute-branch-tags@automation-core
  id: branch_tags
```

#### `undraft-release`
Undrafts a GitHub release with retry logic (waits up to 2 minutes for release to become visible).

**Inputs:**
- `github-token` (required)
- `tag` (optional, default: `${{ github.ref_name }}`)

**Example:**
```yaml
- uses: rancher/backup-restore-operator/actions/undraft-release@automation-core
  with:
    github-token: ${{ secrets.GITHUB_TOKEN }}
```

#### `run-goreleaser`
Executes goreleaser and validates outputs.

**Inputs:**
- `github-token` (required)
- `tag` (optional, default: `${{ github.ref_name }}`)
- `go-version` (optional, default: "1.26")

**Outputs:**
- `metadata` - Contents of dist/metadata.json
- `artifacts` - Contents of dist/artifacts.json

**Example:**
```yaml
- uses: rancher/backup-restore-operator/actions/run-goreleaser@automation-core
  id: goreleaser
  with:
    github-token: ${{ secrets.GITHUB_TOKEN }}
    go-version: "1.26"
```

**Note:** SLSA attestation must be done in the job definition on the release branch (after this action), not within this action, due to OIDC binding requirements.

### Internal Actions (used by workflows)

- `actions/build-deps` and `actions/test-deps` are composite actions used internally by `ci.yaml`. They're not meant to be called directly from a release branch.

### Security Boundaries

Due to GitHub Actions security and OIDC binding requirements, the following **MUST** be done in the release branch's workflow file:

1. **Vault Secret Reading**: Vault paths are per-repo and per-branch context. Release branches must read vault secrets and pass them as inputs to actions.
2. **SLSA Attestation**: OIDC tokens are bound to the workflow file on the tagged branch. Attestation must be a step in the job definition on the release branch (after `run-goreleaser`).

#### Example Release Workflow (on release branch)

```yaml
name: Publish Images & artifacts (via goreleaser)

on:
  push:
    tags: ["*"]

env:
  PUBLIC_REGISTRY: docker.io
  PUBLIC_REPO: rancher

permissions:
  contents: write
  id-token: write
  attestations: write

jobs:
  ci:
    uses: rancher/backup-restore-operator/.github/workflows/ci.yaml@automation-core
    with:
      k3s_versions: '["v1.34.5-k3s1", "v1.36.1-k3s1"]'
      go-version: "1.26"
    permissions:
      contents: read

  goreleaser:
    needs: [ci]
    runs-on: ubuntu-latest
    steps:
      - uses: rancher/backup-restore-operator/actions/run-goreleaser@automation-core
        id: goreleaser
        with:
          github-token: ${{ secrets.GITHUB_TOKEN }}
          go-version: "1.26"

      # SLSA attestation MUST stay on release branch (OIDC binding)
      - name: Attest build provenance
        uses: actions/attest@v4.1.0
        with:
          subject-path: dist/backup-restore-operator_*, build/artifacts/*.tgz

  push:
    needs: [ci]
    runs-on: ubuntu-latest
    permissions:
      contents: read
      id-token: write
    steps:
      - uses: actions/checkout@v7

      # Vault secrets MUST be read on release branch
      - name: "Read vault secrets"
        uses: rancher-eio/read-vault-secrets@v3
        with:
          secrets: |
            secret/data/github/repo/${{ github.repository }}/dockerhub/rancher/credentials username | DOCKER_USERNAME ;
            secret/data/github/repo/${{ github.repository }}/dockerhub/rancher/credentials password | DOCKER_PASSWORD ;
            secret/data/github/repo/${{ github.repository }}/rancher-prime-stg-registry/credentials registry | PRIME_STG_REGISTRY ;
            secret/data/github/repo/${{ github.repository }}/rancher-prime-stg-registry/credentials username | PRIME_STG_REGISTRY_USERNAME ;
            secret/data/github/repo/${{ github.repository }}/rancher-prime-stg-registry/credentials password | PRIME_STG_REGISTRY_PASSWORD ;

      # Call publish-image directly (from ecm-distro-tools)
      - uses: rancher/ecm-distro-tools/actions/publish-image@v0.74.2
        with:
          image: "backup-restore-operator"
          tag: ${{ github.ref_name }}
          public-registry: ${{ env.PUBLIC_REGISTRY }}
          public-repo: ${{ env.PUBLIC_REPO }}
          public-username: ${{ env.DOCKER_USERNAME }}
          public-password: ${{ env.DOCKER_PASSWORD }}
          push-to-prime: true
          prime-registry: ${{ env.PRIME_STG_REGISTRY }}
          prime-repo: rancher
          prime-username: ${{ env.PRIME_STG_REGISTRY_USERNAME }}
          prime-password: ${{ env.PRIME_STG_REGISTRY_PASSWORD }}

      - uses: rancher/backup-restore-operator/actions/check-semver@automation-core
        id: semver_check
        with:
          tag: ${{ github.ref_name }}

      # For stable releases (no prerelease), also push to prime production
      - name: "Read vault secrets (prime prod)"
        if: ${{ steps.semver_check.outputs.HAS_PRERELEASE == 'false' }}
        uses: rancher-eio/read-vault-secrets@v3
        with:
          secrets: |
            secret/data/github/repo/${{ github.repository }}/rancher-prime-registry/credentials registry | PRIME_REGISTRY ;
            secret/data/github/repo/${{ github.repository }}/rancher-prime-registry/credentials username | PRIME_REGISTRY_USERNAME ;
            secret/data/github/repo/${{ github.repository }}/rancher-prime-registry/credentials password | PRIME_REGISTRY_PASSWORD ;

      - uses: rancher/ecm-distro-tools/actions/publish-image@v0.74.2
        if: ${{ steps.semver_check.outputs.HAS_PRERELEASE == 'false' }}
        with:
          image: "backup-restore-operator"
          tag: ${{ github.ref_name }}
          push-to-public: false
          push-to-prime: true
          prime-registry: ${{ env.PRIME_REGISTRY }}
          prime-repo: rancher
          prime-username: ${{ env.PRIME_REGISTRY_USERNAME }}
          prime-password: ${{ env.PRIME_REGISTRY_PASSWORD }}

  release:
    needs: [ci, goreleaser, push]
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: rancher/backup-restore-operator/actions/undraft-release@automation-core
        with:
          github-token: ${{ secrets.GITHUB_TOKEN }}
```

## Shared Scripts

Scripts in `.github/scripts/` are used by actions and workflows:

- `check-semver.sh` - Parses semantic version to determine if it's a prerelease
- `branch-tags.sh` - Calculates branch-specific tags for head builds

These scripts should NOT be called directly from release branch workflows. In Phase 2, they will be wrapped by atomic actions.

## Development

When updating automation-core:
1. Make changes in the automation-core branch
2. Test by updating a single release branch to reference the new changes
3. Once validated, other release branches will pick up changes automatically on their next run

## Future Improvements (Phases 2-4)

- **Phase 2**: Create focused atomic actions (check-semver, compute-branch-tags, undraft-release, run-goreleaser)
- **Phase 3**: Workflow template deployment mechanism for easier release branch setup
- **Phase 4**: CI-image optimization for consistency and performance
