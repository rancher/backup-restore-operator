# Deployment Guide

Practical guide for deploying and managing workflows across release branches.

## Deploying Workflows

### Config Mode (Single Branch)

Deploy to one branch using its configuration file.

**Steps:**
1. Go to **Actions** → **Deploy Workflows** in GitHub
2. Click **Run workflow**
3. Fill in inputs:
   - **mode**: `config`
   - **configFile**: `configs/main.yaml` (or any config file)
   - **dryRun**: `true` (to preview) or `false` (to create PR)
4. Click **Run workflow**

**What happens:**
- Templates are rendered with values from the config file
- Workflows are validated as valid YAML
- A new branch is created on the target repository
- PR is opened with the rendered workflows

**Dry-run mode:**
Set `dryRun: true` to see diffs in the job summary without creating PRs.

### Bulk Mode (Multiple Branches)

Deploy to multiple branches in parallel.

**Steps:**
1. **mode**: `bulk`
2. **branches**: 
   - Specific branches: `main,release/v10.x,release/v9.x`
   - All configured branches: `all`
3. **dryRun**: `true` or `false`

**What happens:**
- Matrix strategy deploys to each branch in parallel
- One PR created per branch
- Independent failure handling (one branch failing doesn't stop others)

### When to Use Each Mode

**Config mode:**
- Deploying to a single branch
- Testing changes on one branch first
- Making branch-specific adjustments

**Bulk mode:**
- Rolling out K3S version updates to all branches
- Deploying template changes across the board
- Initial setup for multiple branches

**Dry-run:**
- Always preview first for bulk deployments
- Verify template changes before creating PRs
- Check diffs for unexpected changes

## Using Reusable Workflows

### From Release Branches

Release branches can call automation-core workflows via `uses:`.

**CI Workflow:**
```yaml
name: Backup Restore CI

on:
  push:
    branches: [main, release/**]
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

**Head Builds Workflow:**
```yaml
name: Branch head Prerelease Images

on:
  push:
    branches: [main, release/**]

jobs:
  head-builds:
    uses: rancher/backup-restore-operator/.github/workflows/head-builds.yaml@automation-core
    permissions:
      contents: write
      id-token: write
    secrets: inherit
```

## Using Composite Actions

### check-semver

Parse semantic version characteristics from a tag.

**Inputs:**
- `tag` (optional, default: `${{ github.ref_name }}`)

**Outputs:**
- `HAS_PRERELEASE` - Whether tag has prerelease identifier
- `HAS_BUILD_META` - Whether tag has build metadata

**Example:**
```yaml
- uses: rancher/backup-restore-operator/actions/check-semver@automation-core
  id: semver_check
  with:
    tag: ${{ github.ref_name }}

- name: Push to production
  if: ${{ steps.semver_check.outputs.HAS_PRERELEASE == 'false' }}
  run: echo "Stable release - push to prod"
```

### run-goreleaser

Execute goreleaser with validation.

**Inputs:**
- `github-token` (required)
- `tag` (optional, default: `${{ github.ref_name }}`)

**Outputs:**
- `metadata` - Contents of dist/metadata.json
- `artifacts` - Contents of dist/artifacts.json

**Example:**
```yaml
- uses: rancher/backup-restore-operator/actions/run-goreleaser@automation-core
  id: goreleaser
  with:
    github-token: ${{ secrets.GITHUB_TOKEN }}

# SLSA attestation MUST be on release branch (after goreleaser)
- name: Attest build provenance
  uses: actions/attest@v4.1.0
  with:
    subject-path: dist/backup-restore-operator_*, build/artifacts/*.tgz
```

### undraft-release

Undraft a GitHub release (with retry logic).

**Inputs:**
- `github-token` (required)
- `tag` (optional, default: `${{ github.ref_name }}`)

**Example:**
```yaml
- uses: rancher/backup-restore-operator/actions/undraft-release@automation-core
  with:
    github-token: ${{ secrets.GITHUB_TOKEN }}
```

### compute-branch-tags

Calculate branch tags for head builds.

**Outputs:**
- `branch_tag` - The branch tag for this build
- `branch_static_tag` - Current static branch tag
- `prev_static_tag` - Previous static branch tag

**Example:**
```yaml
- uses: rancher/backup-restore-operator/actions/compute-branch-tags@automation-core
  id: branch_tags

- name: Tag image
  run: |
    docker tag myimage:latest myimage:${{ steps.branch_tags.outputs.branch_tag }}
```

## Security Boundaries

### What Must Stay on Release Branches

Due to GitHub Actions security and OIDC binding requirements:

**1. Vault Secret Reading**

Vault paths are scoped per-repo and per-branch. Secrets MUST be read on the release branch.

```yaml
# On release branch (NOT automation-core)
- name: "Read vault secrets"
  uses: rancher-eio/read-vault-secrets@v3
  with:
    secrets: |
      secret/data/github/repo/${{ github.repository }}/dockerhub/rancher/credentials username | DOCKER_USERNAME ;
      secret/data/github/repo/${{ github.repository }}/dockerhub/rancher/credentials password | DOCKER_PASSWORD
```

**2. SLSA Attestation**

OIDC tokens are bound to the workflow file on the tagged branch. Attestation MUST execute on the release branch.

```yaml
# On release branch (NOT automation-core)
- name: Attest build provenance
  uses: actions/attest@v4.1.0
  with:
    subject-path: dist/backup-restore-operator_*
```

**Why?** SLSA attestation records the workflow file's branch as the build source. If called from automation-core, it would cite automation-core as the source, breaking attestation integrity.

## Managing Branch Configurations

### Adding a New Release Branch

**Step 1: Create config file**
```bash
cd /path/to/automation-core
cp configs/release-v10.x.yaml configs/release-v11.x.yaml
```

**Step 2: Edit config**
```yaml
branch: release/v11.x
go: 1.25                        # Match branch's go.mod version
k3s_versions:
  - v1.35.0-k3s1
  - v1.37.0-k3s1
automation_core_ref: automation-core
workflows:
  ci: true
  release: true
  head-builds: true
  fossa: true
description: "Release branch for BRO v11.x"
```

**Important:** Set `go` to match the version in the target branch's `go.mod` file.

**Step 3: Validate**
```bash
.github/scripts/validate-config.sh configs/release-v11.x.yaml
```

**Step 4: Deploy**
- GitHub UI → Actions → Deploy Workflows
- mode: `config`
- configFile: `configs/release-v11.x.yaml`
- dryRun: `false`

**Step 5: Review and merge PR** on the release/v11.x branch

### Updating K3S Versions

**For one branch:**
1. Edit `configs/release-v10.x.yaml`
2. Update `k3s_versions` array
3. Commit to automation-core
4. Deploy: mode=`config`, configFile=`configs/release-v10.x.yaml`

**For all branches:**
1. Edit all config files in `configs/`
2. Commit to automation-core
3. Deploy: mode=`bulk`, branches=`all`
4. Review PRs on each branch

### Disabling a Workflow

Edit the config and set workflow flag to false:

```yaml
workflows:
  ci: true
  release: true
  head-builds: true
  fossa: false  # Disable FOSSA
```

Then redeploy to the branch.

## Troubleshooting

### "Config validation failed"

**Symptom:** Validation script reports errors

**Common causes:**
- Invalid YAML syntax
- Missing required fields (`branch`, `go`, `k3s_versions`, `automation_core_ref`)
- Wrong K3S version format (must be `vN.N.N-k3sN`)
- Wrong Go version format (must be `N.N` or `{version: N.N.N, ci_image: goN.N}`)
- Invalid branch name pattern

**Fix:**
```bash
# Check YAML syntax
yq eval '.' configs/your-file.yaml

# Validate config
.github/scripts/validate-config.sh configs/your-file.yaml
```

### "Template rendering failed"

**Symptom:** Deploy workflow fails during template rendering

**Common causes:**
- Template syntax error
- Missing template variable
- Renderer binary not built

**Fix:**
```bash
# Rebuild renderer
go build -o render-templates ./render-templates.go

# Test locally
./render-templates \
  -config configs/main.yaml \
  -template-dir templates \
  -output-dir /tmp/test
```

### "Workflows not updating on release branch"

**Symptom:** Changes to automation-core don't appear on release branch

**Cause 1:** Using reusable workflows (they update automatically on next run)
- No action needed - next workflow run picks up changes

**Cause 2:** Using rendered templates (need redeployment)
- Run Deploy Workflows to update the branch
- Templates don't auto-update, they're deployed as static files

**Check which pattern:**
Look at the workflow file on the release branch:
- `uses: .github/workflows/ci.yaml@automation-core` = reusable (auto-updates)
- Full workflow definition = template (needs redeployment)

### "SLSA attestation failing"

**Symptom:** Attestation step fails on release workflow

**Causes:**
- Attestation in automation-core (wrong - must be on release branch)
- Workflow called from automation-core (wrong - must be rendered template)
- OIDC binding issue

**Fix:**
Ensure release.yaml is a rendered template on the release branch, not calling automation-core:

```yaml
# WRONG (reusable workflow)
jobs:
  release:
    uses: rancher/backup-restore-operator/.github/workflows/release.yaml@automation-core

# CORRECT (rendered template)
jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: rancher/backup-restore-operator/actions/run-goreleaser@automation-core
      
      # Attestation step HERE on release branch
      - name: Attest build provenance
        uses: actions/attest@v4.1.0
```

### "Matrix builder errors"

**Symptom:** Deploy workflow fails in prepare job

**Common causes:**
- Invalid config file path
- Config file doesn't exist
- Missing yq or jq tools

**Fix:**
```bash
# Test locally
.github/scripts/prepare-deployment.sh config configs/main.yaml
.github/scripts/prepare-deployment.sh bulk "" "all"
```

## Common Operations

### Update all branches at once

```bash
# Edit all configs
vim configs/*.yaml

# Commit changes
git add configs/
git commit -m "Update K3S versions across all branches"
git push

# Deploy
# GitHub UI: mode=bulk, branches=all, dryRun=false
```

### Test changes before rolling out

```bash
# 1. Create test branch config
cp configs/main.yaml configs/test-branch.yaml
# Edit to point to test branch

# 2. Dry-run first
# GitHub UI: mode=config, configFile=configs/test-branch.yaml, dryRun=true

# 3. Deploy to test branch
# GitHub UI: dryRun=false

# 4. Verify on test branch

# 5. Roll out to production branches
# GitHub UI: mode=bulk, branches=all
```

### Roll back a deployment

If a deployed workflow causes issues:

**Option 1: Revert the PR**
- Go to the PR on the release branch
- Revert it via GitHub UI

**Option 2: Redeploy previous version**
- Restore old config from git history
- Redeploy to the branch

**Option 3: Emergency fix**
- Edit workflow directly on release branch
- Commit fix
- Update config and redeploy when ready

## Best Practices

1. **Always dry-run first** for bulk deployments
2. **Test on one branch** before deploying to all
3. **Review PRs carefully** before merging
4. **Keep configs in sync** with actual branch state
5. **Document why** in config descriptions
6. **Validate locally** before committing config changes
