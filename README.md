# automation-core - Backup and Restore Operator

Centralized GitHub Actions workflows, actions, and templates for backup-restore-operator. Update once here, all branches pick it up.

## Quick Start

**Deploy workflows:**
- GitHub UI → Actions → Deploy Workflows
- Mode: `config` (single branch) or `bulk` (multiple)
- Config File: `configs/main.yaml` or Branches: `all`

## Structure

```
automation-core/
├── actions/           # Composite actions
├── configs/           # Branch configs (K3S versions, settings)
├── templates/         # Workflow templates
└── .github/workflows/ # Reusable workflows + deploy system
```

## Three Patterns

**1. Composite Actions** - Call from any branch
```yaml
uses: rancher/backup-restore-operator/actions/check-semver@automation-core
```

**2. Reusable Workflows** - For CI, head-builds
```yaml
uses: rancher/backup-restore-operator/.github/workflows/ci.yaml@automation-core
with:
  k3s_versions: '["v1.34.5-k3s1"]'
```

**3. Rendered Templates** - For release workflows (SLSA attestation required on branch)

## Common Tasks

**Add new branch:**
1. Copy config: `cp configs/release-v10.x.yaml configs/release-v11.x.yaml`
2. Edit branch name and K3S versions
3. Deploy via GitHub UI

**Update K3S versions:**
1. Edit `configs/` files
2. Commit to automation-core
3. Deploy: mode=`bulk`, branches=`all`

## Documentation

- [ARCHITECTURE.md](docs/ARCHITECTURE.md) - Design decisions
- [TEMPLATES.md](docs/TEMPLATES.md) - Template syntax

## Available Actions

- `check-semver` - Parse semver from tag
- `compute-branch-tags` - Calculate branch tags
- `run-goreleaser` - Execute goreleaser
- `undraft-release` - Undraft release
- `build-deps` / `test-deps` - CI dependencies

## Available Workflows

- `ci.yaml` - Lint, build, test
- `head-builds.yaml` - Prerelease images
- `deploy-workflows.yml` - Template deployment
