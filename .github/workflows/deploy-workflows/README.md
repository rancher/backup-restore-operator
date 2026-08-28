# Deploy Workflows - Local & GHA

Dual-execution workflow deployment system. Can run locally or in GitHub Actions.

## Local Usage

### Prerequisites

```bash
# Install dependencies
brew install yq jq gh

# Authenticate gh CLI
gh auth login
```

### Deploy Single Branch

```bash
cd ~/GitProjects/SUSE/backup-restore-operator/.worktree/automation-core

./.github/workflows/deploy-workflows/run-local.sh \
  --config configs/main.yaml \
  --target-dir ~/GitProjects/SUSE/backup-restore-operator
```

### Deploy Multiple Branches

```bash
./.github/workflows/deploy-workflows/run-local.sh \
  --branches "main,release/v10.x" \
  --target-dir ~/GitProjects/SUSE/backup-restore-operator
```

### Deploy All Configured Branches

```bash
./.github/workflows/deploy-workflows/run-local.sh \
  --branches "all" \
  --target-dir ~/GitProjects/SUSE/backup-restore-operator
```

### Dry-Run (Preview Only)

```bash
./.github/workflows/deploy-workflows/run-local.sh \
  --config configs/main.yaml \
  --target-dir ~/GitProjects/SUSE/backup-restore-operator \
  --dry-run
```

## Options

- `--config FILE` - Config file to use (for single branch deployment)
- `--branches LIST` - Comma-separated branch list or 'all' (for bulk deployment)
- `--target-dir DIR` - Path to backup-restore-operator repository clone (**required**)
- `--remote NAME` - Git remote name in target repo (default: `origin`)
- `--dry-run` - Show diffs only, don't create branch or PR
- `--help` - Show usage

## How It Works

1. **Build Matrix** - Uses `prepare-deployment.sh` to build deployment matrix
2. **Render Templates** - Go renderer generates workflows from templates + configs
3. **Create Branch** - Creates deployment branch on target repo
4. **Show Diffs** - Shows what changed (or exits if dry-run)
5. **Commit & Push** - Commits rendered workflows and pushes
6. **Create PR** - Uses `gh` CLI to create PR (if available)

## GitHub Actions Usage

The same scripts power the GitHub Actions workflow. See `deploy-workflows.yml`.

## Files

- `run-local.sh` - Local entry point
- `run-gha.sh` - GitHub Actions entry point
- `common.sh` - Shared functions
- `build-matrix.sh` - Matrix builder
- `deploy-entries.sh` - Loop over matrix entries
- `deploy-single.sh` - Deploy to one branch

## Troubleshooting

**"gh CLI not found"**
- Install: `brew install gh`
- Branch is still pushed, just create PR manually

**"Permission denied"**
- Authenticate: `gh auth login`
- Check you have write access to target repo

**"No such file or directory"**
- Ensure `--target-dir` points to valid backup-restore-operator clone
- Ensure you're running from automation-core directory
