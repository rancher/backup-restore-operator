# Workflow Templates

This directory contains workflow templates that are deployed to release branches via the `deploy-workflows.yml` workflow.

## Templates

### `ci.yaml.tmpl`
CI workflow for PR and push validation.

**Placeholders:**
- `{{K3S_VERSIONS}}` - JSON array of K3S versions for integration tests
- `{{AUTOMATION_CORE_REF}}` - Automation-core ref (e.g., `@automation-core` or `@v1`)

### `release.yaml.tmpl`
Release workflow triggered on tag push.

**Placeholders:**
- `{{K3S_VERSIONS}}` - JSON array of K3S versions for integration tests
- `{{AUTOMATION_CORE_REF}}` - Automation-core ref (e.g., `@automation-core` or `@v1`)

### `head-builds.yaml.tmpl`
Head builds workflow for branch prerelease images.

**Placeholders:**
- `{{AUTOMATION_CORE_REF}}` - Automation-core ref (e.g., `@automation-core` or `@v1`)

## Updating Templates

When updating templates:

1. Make changes to the `.tmpl` files in this directory
2. Test by deploying to a test branch first
3. Once validated, deploy to production branches (main, release/v2.x, etc.)
4. Document significant changes in the automation-core CHANGELOG

## Template Syntax

Templates use simple `sed` replacement:
- Use `{{PLACEHOLDER}}` for values that will be replaced
- Placeholders are case-sensitive
- No complex logic - keep templates simple

## Adding New Templates

To add a new workflow template:

1. Create `new-workflow.yaml.tmpl` in this directory
2. Add placeholders as needed
3. Update `deploy-workflows.yml` to process the new template
4. Update the main README.md documentation
