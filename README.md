# Backup and Restore Operator — automation-core

This branch hosts the reusable GitHub Actions workflows and composite actions for `backup-restore-operator`. Instead of every release branch keeping its own copy of CI, they call back into the workflows defined here. Update something once, here, and every branch calling in picks it up on its next run.

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

- `.github/actions/build-deps` and `.github/actions/test-deps` are composite actions used internally by `ci.yaml`. They're not meant to be called directly from a release branch.
- `.github/actions/release` runs goreleaser, publishes the container images, and un-drafts the GitHub release. Unlike the workflows above, this one has to be called as a *step* rather than a reusable workflow — the image signing it does needs the job to be defined in the release branch's own file (at the tag being released), not in a file called in from a different ref. A release branch's `release.yaml` looks like:

```yaml
name: Publish Images & artifacts (via goreleaser)

on:
  push:
    tags:
      - "*"

jobs:
  ci:
    uses: rancher/backup-restore-operator/.github/workflows/ci.yaml@automation-core
    with:
      k3s_versions: '["v1.34.5-k3s1", "v1.36.1-k3s1"]'
    permissions:
      contents: read
    secrets: inherit

  release:
    needs: [ ci ]
    runs-on: runs-on,image=ubuntu22-full-x64,runner=4cpu-linux-x64,run-id=${{ github.run_id }}
    permissions:
      contents: write
      id-token: write
      attestations: write
    steps:
      - uses: rancher/backup-restore-operator/.github/actions/release@automation-core
        with:
          github-token: ${{ secrets.GITHUB_TOKEN }}
          docker-password: ${{ secrets.DOCKER_PASSWORD }}
```
