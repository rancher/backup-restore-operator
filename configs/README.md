# Branch Configuration Files

This directory contains configuration files for each release branch. Each config defines branch-specific values used when rendering workflow templates.

## Config File Format

```yaml
# Required fields
branch: release/v9.x            # Target branch name
go: 1.25                        # Go version (simple format)
k3s_versions:                   # K3S versions for integration tests
  - v1.32.13-k3s1
  - v1.34.6-k3s1
automation_core_ref: automation-core  # automation-core ref (branch/tag/SHA)

# Workflow enablement
workflows:
  ci: true
  release: true
  head-builds: true
  fossa: true

# Metadata
description: "Release branch for BRO v9.x"
```

## Go Version Field

### Simple Format (Recommended)

```yaml
go: 1.25
```

- Automatically expands to version `1.25.0`
- Derives CI image tag as `go1.25`
- Use this format unless you have a specific need for the explicit format

### Explicit Format (Edge Cases)

```yaml
go:
  version: 1.25.0
  ci_image: go1.25
```

Use this when:
- You need a specific patch version (e.g., `1.25.3`)
- The CI image tag doesn't follow the standard `goX.Y` pattern

### How to Determine Go Version

Check the target branch's `go.mod` file:

```bash
git show release/v9.x:go.mod | grep "^go "
# Output: go 1.25.0
```

Then use the major.minor version in the config:
```yaml
go: 1.25
```

## K3S Versions

List K3S versions used for integration testing on this branch.

Format: `vX.Y.Z-k3sN`

```yaml
k3s_versions:
  - v1.32.13-k3s1
  - v1.34.6-k3s1
```

## Automation Core Reference

The `automation_core_ref` field determines which version of automation-core workflows and actions to use.

**Options:**
- `automation-core` - Latest (rolling release, recommended for active branches)
- `automation/v1.0.0` - Pinned version (for stability)
- `abc123f` - Specific commit SHA (for debugging)

## Workflow Enablement

Control which workflows are deployed to the branch:

```yaml
workflows:
  ci: true              # CI workflow (lint, build, test)
  release: true         # Release workflow (on tag push)
  head-builds: true     # Head builds workflow (prerelease images)
  fossa: true           # FOSSA license scanning
```

## Validation

Validate a config file before committing:

```bash
.github/scripts/validate-config.sh configs/release-v9.x.yaml
```

## Creating a New Config

1. Copy an existing config:
   ```bash
   cp configs/release-v10.x.yaml configs/release-v11.x.yaml
   ```

2. Edit the new config:
   - Update `branch` to the new branch name
   - Set `go` to match the branch's `go.mod` version
   - Update `k3s_versions` for the new branch
   - Update `description`

3. Validate:
   ```bash
   .github/scripts/validate-config.sh configs/release-v11.x.yaml
   ```

4. Test render locally:
   ```bash
   ./render-templates \
     -config configs/release-v11.x.yaml \
     -template-dir templates \
     -output-dir /tmp/test-render
   ```

5. Deploy (dry-run first):
   ```bash
   ./.github/workflows/deploy-workflows/run-local.sh \
     --config configs/release-v11.x.yaml \
     --target-dir ~/path/to/backup-restore-operator \
     --dry-run
   ```

## Updating an Existing Config

### Update Go Version

When updating Go on a release branch:

1. Update `go.mod` on the release branch
2. Update corresponding config file in automation-core:
   ```yaml
   go: 1.26  # Updated from 1.25
   ```
3. Redeploy workflows to that branch

### Update K3S Versions

1. Edit the config file:
   ```yaml
   k3s_versions:
     - v1.33.0-k3s1  # Updated
     - v1.35.0-k3s1  # Updated
   ```
2. Commit and push to automation-core
3. Redeploy to the branch

## See Also

- [TEMPLATES.md](../docs/TEMPLATES.md) - Template syntax and variables
- [DEPLOYMENT.md](../docs/DEPLOYMENT.md) - Deployment guide
- [ARCHITECTURE.md](../docs/ARCHITECTURE.md) - System architecture
