# Architecture

This document explains the design decisions, constraints, and patterns that shape the automation-core system.

## Design Goals

1. **Single source of truth**: Update CI/CD logic once, apply to all branches
2. **Branch-specific configuration**: Each branch can specify its own K3S versions, settings
3. **Security compliance**: Respect GitHub Actions and SLSA attestation requirements
4. **Maintainability**: Clear patterns, minimal duplication, easy to understand
5. **Rollback capability**: Ability to freeze or pin versions when needed

## The Hybrid Approach

We use **three different patterns** depending on the security and functionality requirements:

### Pattern 1: Composite Actions

**What:** Reusable action definitions in `actions/` directory

**When to use:**
- Encapsulated, focused functionality (check semver, compute tags, run goreleaser)
- No OIDC context required
- Can be called from any branch without attestation issues

**How they work:**
```yaml
- uses: rancher/backup-restore-operator/actions/check-semver@automation-core
  with:
    tag: ${{ github.ref_name }}
```

**Examples:** build-deps, test-deps, check-semver, compute-branch-tags, run-goreleaser, undraft-release

**Security:** Safe to call from anywhere - no OIDC binding concerns

---

### Pattern 2: Reusable Workflows

**What:** Workflows defined in automation-core that can be called via `workflow_call`

**When to use:**
- Multi-step jobs that don't involve attestation
- Logic shared across all branches
- Non-security-sensitive operations

**How they work:**
```yaml
jobs:
  ci:
    uses: rancher/backup-restore-operator/.github/workflows/ci.yaml@automation-core
    with:
      k3s_versions: '["v1.34.5-k3s1"]'
```

**Examples:** ci.yaml (lint, build, test), head-builds.yaml (prerelease images)

**Security:** Safe for non-attestation workflows - OIDC context belongs to caller

---

### Pattern 3: Rendered Templates

**What:** Workflow templates rendered to release branches with branch-specific values

**When to use:**
- **REQUIRED**: Workflows that generate SLSA attestations (release.yaml)
- **OPTIONAL**: Workflows needing per-branch configuration (different K3S versions)

**How they work:**
1. Templates in `templates/` with `[[.Variables]]` placeholders
2. Config files in `configs/` specify values per branch
3. Go renderer generates final workflows
4. Deployment system creates PRs to release branches

**Examples:** release.yaml (SLSA required), ci.yaml (branch-specific K3S versions), fossa.yml

**Security:** SLSA attestation workflows MUST execute on the tagged branch for OIDC binding

---

## Why Not Just Use Reusable Workflows for Everything?

**The SLSA Attestation Problem:**

GitHub Actions SLSA attestation uses OIDC (OpenID Connect) tokens to prove provenance. These tokens include:
- Repository name
- **Workflow ref** (branch/tag where workflow file lives)
- Job details

When a workflow on `main` calls a reusable workflow on `automation-core`:
- OIDC token cites `automation-core` as the workflow source
- SLSA attestation records `automation-core` as the build source
- **Problem:** The tag `v10.0.0` was pushed to `release/v10.x`, not `automation-core`
- **Result:** Attestation integrity fails - source branch doesn't match tag location

**Solution:** For attestation workflows, the workflow file MUST exist on the tagged branch.

This is why `release.yaml` is a rendered template, not a reusable workflow.

---

## Security Boundaries

### What MUST Stay on Release Branches

**1. Vault Secret Reading**
```yaml
- name: "Read vault secrets"
  uses: rancher-eio/read-vault-secrets@v3
  with:
    secrets: |
      secret/data/github/repo/${{ github.repository }}/dockerhub/rancher/credentials ...
```

**Why:** Vault paths are scoped per-repo and per-branch context. The OIDC token must prove the request comes from the authorized branch.

**2. SLSA Attestation**
```yaml
- name: Attest build provenance
  uses: actions/attest@v4.1.0
  with:
    subject-path: dist/backup-restore-operator_*
```

**Why:** OIDC binding requirement - attestation must cite the tagged branch as source.

### What CAN Live in automation-core

- Composite actions (no OIDC context)
- Reusable workflows (non-attestation)
- Build/test logic
- Utility functions

---

## Configuration System Design

### Why Config Files?

**Problem:** Manual workflow_dispatch inputs are:
- Error-prone (typing JSON arrays, branch names)
- Not version-controlled
- Hard to audit ("what K3S versions does v9.x use?")
- Can't be bulk-updated

**Solution:** Declarative YAML config files
- Version-controlled in automation-core
- Single source of truth
- Easy to diff and audit
- Enables bulk operations

### Config File Structure

```yaml
branch: release/v10.x              # Target branch
k3s_versions:                      # Integration test versions
  - v1.33.8-k3s1
  - v1.35.1-k3s1
automation_core_ref: automation-core  # automation-core version
workflows:                         # Which workflows to deploy
  ci: true
  release: true
  head-builds: true
  fossa: true
description: "..."                 # Human-readable description
```

**Validation:** `.github/scripts/validate-config.sh` ensures:
- Required fields present
- Correct formats (branch patterns, K3S version format)
- Boolean values for workflow flags

---

## Template Rendering System

### Why Go Templates?

**Requirements:**
- Simple placeholder substitution
- YAML-aware (validate output)
- Familiar to team (Go engineers)
- No runtime dependencies (compiles to binary)

**Why not sed/awk:**
- Limited error messages
- No validation
- Hard to extend

**Why not Python/Jinja2:**
- Team is Go-focused
- Runtime dependency overhead
- Overkill for our needs

**Why not JavaScript/TypeScript:**
- No compelling advantage
- Would require Node.js runtime

**Decision:** Go's `text/template` with custom delimiters

### Template Delimiter Choice

**Problem:** GitHub Actions uses `${{ }}` for expressions, conflicts with Go template default `{{ }}`

**Solutions considered:**
1. Escape every `${{ }}` as `${{"{{"}} ... {{"}}"}}`  - tedious, error-prone
2. Custom delimiters `[[ ]]` - clean, no conflicts

**Decision:** Use `[[ ]]` delimiters

**Template syntax:**
```yaml
uses: repo/.github/workflows/ci.yaml@[[.AutomationCoreRef]]
with:
  k3s_versions: '[[jsonArray .K3SVersions]]'
```

**Renders to:**
```yaml
uses: repo/.github/workflows/ci.yaml@automation-core
with:
  k3s_versions: '["v1.33.8-k3s1", "v1.35.1-k3s1"]'
```

---

## Deployment Workflow Design

### Two Modes

**Config Mode:** Deploy one branch using its config file
- Simple, explicit
- Good for single-branch updates
- Easy to review changes

**Bulk Mode:** Deploy multiple branches at once
- Efficient for rolling out updates
- Matrix strategy (parallel PRs)
- Support "all" keyword

### Why No Legacy Mode?

Original plan included a "legacy" manual input mode for backward compatibility. **Removed because:**
- Added complexity for no benefit
- Manual inputs are error-prone (the problem we're solving)
- Team can adopt new system cleanly
- Simpler is better

### Dry-Run Capability

**Purpose:** Preview changes before creating PRs

**How it works:**
1. Render templates
2. Show diffs in GitHub job summary
3. Stop before creating branch/PR

**Use cases:**
- Verify template changes before rollout
- Review what will change on each branch
- Catch errors early

---

## Version Management Strategy

### Rolling Release (Current)

**Approach:** All branches use `automation_core_ref: automation-core`

**Pros:**
- Simple - no version management
- Always up-to-date
- No tag maintenance overhead

**Cons:**
- Breaking changes affect all branches immediately
- Can't freeze for stability
- No gradual rollout capability

**Decision:** Start with rolling release, add versioning only if needed

### Hierarchical Tags (Escape Hatch)

**If we need versioning later:**

Use `automation/vX.Y.Z` tags to avoid conflicts with product release tags (`v10.0.0`, `v9.0.0`):

```bash
git tag -a automation/v1.0.0 -m "..."
git tag -f automation/v1.0
git tag -f automation/v1
```

**Config pinning:**
```yaml
automation_core_ref: automation-core      # Rolling (default)
automation_core_ref: automation/v1        # Major version pin
automation_core_ref: automation/v1.0.0    # Exact pin
automation_core_ref: abc123f              # SHA pin (debugging)
```

**When to use:**
- EOL branches needing stability
- Testing breaking changes
- Gradual rollout of major updates

---

## Validation Strategy

### Multi-Layer Validation

**Layer 1: Config Schema Validation**
- Bash script using `yq`
- Checks: required fields, formats, types
- Runs: locally + CI on PR

**Layer 2: Template Rendering Validation**
- Go renderer validates template syntax
- Checks for missing variables
- YAML parser validates rendered output

**Layer 3: CI Testing**
- `validate-configs.yml` - validates all configs
- `test-templates.yml` - renders all templates
- Runs on every PR to automation-core

**Layer 4: Dry-Run Testing**
- Preview changes before deployment
- Catch issues in GitHub Actions environment
- Human review of diffs

### Why Blocking Validation?

**Philosophy:** Fail fast, fail loud

**Benefits:**
- Catch errors at commit time, not deploy time
- Prevent invalid configs from merging
- Clear error messages guide fixes
- Confidence in automation

---

## Trade-offs and Alternatives Considered

### Why Not a Monorepo?

**Alternative:** Keep all workflows in each release branch

**Rejected because:**
- Duplication across 6+ branches
- Hard to keep in sync
- High maintenance burden
- Changes require PRs to every branch

**Our approach:** Centralized with branch-specific configs

### Why Not GitHub's Reusable Workflow Inputs?

**Alternative:** Pass all config via workflow inputs

**Rejected because:**
- Manual input every time (error-prone)
- Not version-controlled
- Can't bulk-update
- Hard to audit

**Our approach:** Config files + deployment system

### Why Not Renovate/Updatecli for Workflow Updates?

**Alternative:** Auto-update workflows via dependency bots

**Rejected because:**
- Can't handle branch-specific logic
- No dry-run preview
- Harder to test before merge
- Less control over rollout

**Our approach:** Explicit deployment workflow with dry-run

---

## Future Considerations

### Potential Enhancements

**1. Auto-deployment on Config Changes**
- Trigger deployments when configs change
- Create draft PRs automatically
- Current: Manual trigger

**2. Canary Deployments**
- Deploy to test branch first
- Automated validation
- Progressive rollout

**3. Config Inheritance**
- Base config with branch-specific overrides
- Reduce duplication
- Current: Explicit per-branch configs

**4. Workflow Metrics**
- Track deployment success rate
- Monitor template rendering time
- Audit config drift

### Breaking Change Policy

**When automation-core needs breaking changes:**

1. Create hierarchical tag `automation/v2.0.0`
2. Update main to use new version
3. Test thoroughly
4. Update release branches incrementally
5. Keep `automation/v1` stable for old branches

**Migration path exists** via hierarchical tags when needed.

---

## Decision Log

**Why orphan branch?**
- Clean separation from application code
- No merge conflicts with feature branches
- Independent history

**Why Go for rendering?**
- Team expertise
- Standard library sufficient
- Single binary, no runtime

**Why config files over database?**
- Version-controlled
- Git-based review process
- Simple, no infrastructure

**Why bash scripts for validation?**
- Leverage existing tools (yq, jq)
- No additional dependencies
- Easy to understand and modify

**Why remove legacy mode?**
- Simpler system
- Fewer code paths to maintain
- Team can adopt new system cleanly

---

## Summary

This architecture balances:
- **Security** (SLSA attestation integrity)
- **Maintainability** (single source of truth)
- **Flexibility** (branch-specific configuration)
- **Simplicity** (rolling release, minimal versioning)
- **Safety** (multi-layer validation, dry-run)

The hybrid approach (actions + reusable workflows + templates) respects GitHub Actions security boundaries while minimizing duplication across release branches.
