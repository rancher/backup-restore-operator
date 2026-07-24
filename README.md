# Backup and Restore Operator — automation-core

This branch hosts the reusable GitHub Actions workflows and composite actions for `backup-restore-operator`. Instead of every release branch keeping its own copy of CI, they call back into the workflows defined here. Update something once, here, and every branch calling in picks it up on its next run.

## Workflows

- `ci.yaml` — lint, build, and integration test. Takes `k3s_versions` (a JSON array as a string) as a required input, so each branch can set its own K3S versions to match their respective rancher minors.
- `head-builds.yaml` — builds and pushes the prerelease image.
- `release.yaml` — the tag-triggered release pipeline: runs `ci.yaml`, goreleaser, image publishing, and un-drafts the GitHub release. Also takes `k3s_versions`, which it forwards to `ci.yaml`.

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

`head-builds.yaml` and `release.yaml` work the same way — same `uses:` pattern, different workflow file.

## Actions

`.github/actions/build-deps` and `.github/actions/test-deps` are composite actions used internally by `ci.yaml`. They're not meant to be called directly from a release branch.
