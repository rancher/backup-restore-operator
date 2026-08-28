# Template Guide

This guide covers the workflow template system in automation-core, including syntax, available variables, and how to create and test templates.

## Overview

Templates allow us to generate branch-specific workflows from a single source. Each template is rendered with values from a branch's config file and deployed to that branch as a complete workflow.

**Why templates instead of reusable workflows for everything?**
- **SLSA attestation requirement**: release.yaml must execute on the tagged branch (OIDC binding)
- **Branch-specific configuration**: Different branches use different K3S versions
- **Consistency**: Single source of truth for workflow structure

## Template Syntax

Templates use Go's `text/template` package with **custom delimiters** to avoid conflicts with GitHub Actions expressions.

### Delimiter Choice

**GitHub Actions uses:** `${{ }}` for expressions  
**Our templates use:** `[[` `]]` for template variables

This prevents conflicts and eliminates the need to escape GitHub Actions syntax.

### Basic Template Syntax

**Template variable:**
```yaml
uses: rancher/backup-restore-operator/.github/workflows/ci.yaml@[[.AutomationCoreRef]]
```

**Renders to:**
```yaml
uses: rancher/backup-restore-operator/.github/workflows/ci.yaml@automation-core
```

**GitHub Actions expressions remain unchanged:**
```yaml
tag: ${{ github.ref_name }}  # No escaping needed
token: ${{ secrets.GITHUB_TOKEN }}  # Works as-is
```

## Available Template Variables

### .AutomationCoreRef
- **Type:** String
- **Usage:** Reference to automation-core (branch, tag, or SHA)
- **Example:** `@[[.AutomationCoreRef]]` → `@automation-core`

### .K3SVersions
- **Type:** Array of strings
- **Usage:** K3S versions for integration tests
- **Example:** `[[jsonArray .K3SVersions]]` → `["v1.33.8-k3s1", "v1.35.1-k3s1"]`

### .Branch
- **Type:** String
- **Usage:** Target branch name

### .Description
- **Type:** String
- **Usage:** Human-readable branch description

## Template Functions

### jsonArray
Converts array to JSON array string for GitHub Actions inputs.

```yaml
k3s_versions: '[[jsonArray .K3SVersions]]'
# Renders to: '["v1.33.8-k3s1", "v1.35.1-k3s1"]'
```

## Testing Templates Locally

```bash
# Build renderer
go build -o render-templates ./render-templates.go

# Render templates
./render-templates \
  -config configs/main.yaml \
  -template-dir templates \
  -output-dir /tmp/rendered

# Validate output
yq eval '.' /tmp/rendered/ci.yaml
```

## Template Best Practices

1. **Keep templates simple** - Complex logic belongs in reusable workflows
2. **Use jsonArray for arrays** - Required for GitHub Actions inputs
3. **Preserve GitHub Actions syntax** - Don't template `${{ }}` expressions
4. **Test before deploying** - Use dry-run mode
5. **Add comments** - Explain non-obvious sections

For more details, see [ARCHITECTURE.md](ARCHITECTURE.md).
