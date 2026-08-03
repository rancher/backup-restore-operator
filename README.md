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

- `ci.yaml` — lint, build, and integration test. Takes `k3s_versions` (a JSON array as a string) as a required input, so each branch can set its own K3S versions to match their respective rancher minors.
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
    permissions:
      contents: read
    secrets: inherit
```

`head-builds.yaml` works the same way — same `uses:` pattern, different workflow file.

## Actions

### Internal Actions (used by workflows)

- `actions/build-deps` and `actions/test-deps` are composite actions used internally by `ci.yaml`. They're not meant to be called directly from a release branch.

### Release Action

**IMPORTANT**: The `actions/release` action has specific requirements due to GitHub Actions security boundaries.

#### What the Release Action Does

- Runs goreleaser to build binaries and Helm charts
- Publishes container images to Docker Hub and Prime registries
- Un-drafts the GitHub release

#### What Must Stay on Release Branch

Due to GitHub Actions security and OIDC binding requirements, the following **MUST** be done in the release branch's workflow file, not in this composite action:

1. **Vault Secret Reading**: Vault paths are per-repo and per-branch context. The release branch must read vault secrets and pass them as inputs.
2. **SLSA Attestation**: OIDC tokens are bound to the workflow file on the tagged branch. Attestation must be a step in the job definition on the release branch.

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
    permissions:
      contents: read

  goreleaser:
    needs: [ci]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - run: git fetch --force --tags

      - name: Install go
        uses: actions/setup-go@v6
        with:
          go-version: 1.26

      - uses: rancherlabs/dep-fetch/actions/sync-deps@v0.2.0

      - name: Run goreleaser
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          GORELEASER_CURRENT_TAG: ${{ github.ref_name }}
        run: goreleaser release --clean

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

      # Now call the release action with credentials passed as inputs
      - uses: rancher/backup-restore-operator/actions/release@automation-core
        with:
          github-token: ${{ secrets.GITHUB_TOKEN }}
          docker-username: ${{ env.DOCKER_USERNAME }}
          docker-password: ${{ env.DOCKER_PASSWORD }}
          prime-stg-registry: ${{ env.PRIME_STG_REGISTRY }}
          prime-stg-username: ${{ env.PRIME_STG_REGISTRY_USERNAME }}
          prime-stg-password: ${{ env.PRIME_STG_REGISTRY_PASSWORD }}
          # For stable releases, also provide prime prod credentials:
          # prime-registry: ${{ env.PRIME_REGISTRY }}
          # prime-username: ${{ env.PRIME_REGISTRY_USERNAME }}
          # prime-password: ${{ env.PRIME_REGISTRY_PASSWORD }}
```

#### Why These Restrictions Exist

- **Vault**: Composite actions cannot access the `vars` context (GitHub security restriction). Vault secret paths are per-repo and per-branch.
- **OIDC/SLSA**: OIDC tokens used for image signing and SLSA attestations are bound to the workflow file on the specific branch/tag being released. Moving attestation to a composite action breaks this binding.

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
