#!/bin/bash
set -euo pipefail

# Validate configuration YAML files for automation-core deployment system
# Usage: ./validate-config.sh <config-file.yaml>

CONFIG_FILE="${1:-}"

if [ -z "$CONFIG_FILE" ]; then
    echo "Usage: $0 <config-file.yaml>" >&2
    exit 1
fi

if [ ! -f "$CONFIG_FILE" ]; then
    echo "ERROR: Config file not found: $CONFIG_FILE" >&2
    exit 1
fi

echo "Validating config: $CONFIG_FILE"

# Check yq is installed
if ! command -v yq &> /dev/null; then
    echo "ERROR: yq is required but not installed" >&2
    echo "Install with: brew install yq" >&2
    exit 1
fi

# Validate YAML syntax
if ! yq eval '.' "$CONFIG_FILE" > /dev/null 2>&1; then
    echo "ERROR: Invalid YAML syntax in $CONFIG_FILE" >&2
    exit 1
fi

# Extract values
BRANCH=$(yq eval '.branch' "$CONFIG_FILE")
K3S_VERSIONS=$(yq eval '.k3s_versions' "$CONFIG_FILE")
AUTOMATION_CORE_REF=$(yq eval '.automation_core_ref' "$CONFIG_FILE")

# Validate required fields exist
if [ "$BRANCH" == "null" ] || [ -z "$BRANCH" ]; then
    echo "ERROR: Missing required field: branch" >&2
    exit 1
fi

if [ "$K3S_VERSIONS" == "null" ] || [ -z "$K3S_VERSIONS" ]; then
    echo "ERROR: Missing required field: k3s_versions" >&2
    exit 1
fi

if [ "$AUTOMATION_CORE_REF" == "null" ] || [ -z "$AUTOMATION_CORE_REF" ]; then
    echo "ERROR: Missing required field: automation_core_ref" >&2
    exit 1
fi

# Validate branch format: main or release/v{N}.{x|0|N}
if ! echo "$BRANCH" | grep -qE '^(main|release/v[0-9]+\.(x|0|[0-9]+))$'; then
    echo "ERROR: Invalid branch format: $BRANCH" >&2
    echo "Expected: 'main' or 'release/vN.x' or 'release/vN.0'" >&2
    exit 1
fi

# Validate k3s_versions is an array
K3S_COUNT=$(yq eval '.k3s_versions | length' "$CONFIG_FILE")
if [ "$K3S_COUNT" -eq 0 ]; then
    echo "ERROR: k3s_versions must contain at least one version" >&2
    exit 1
fi

# Validate each K3S version format: vN.N.N-k3sN
for i in $(seq 0 $((K3S_COUNT - 1))); do
    K3S_VERSION=$(yq eval ".k3s_versions[$i]" "$CONFIG_FILE")
    if ! echo "$K3S_VERSION" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+-k3s[0-9]+$'; then
        echo "ERROR: Invalid k3s_version format: $K3S_VERSION" >&2
        echo "Expected format: vN.N.N-k3sN (e.g., v1.34.5-k3s1)" >&2
        exit 1
    fi
done

# Validate automation_core_ref format: automation-core, vN, vN.N, vN.N.N, or SHA
if ! echo "$AUTOMATION_CORE_REF" | grep -qE '^(automation-core|v[0-9]+(\.[0-9]+)*|[0-9a-f]{7,40})$'; then
    echo "ERROR: Invalid automation_core_ref format: $AUTOMATION_CORE_REF" >&2
    echo "Expected: 'automation-core', 'vN', 'vN.N', 'vN.N.N', or SHA" >&2
    exit 1
fi

# Validate workflows object exists (optional fields)
WORKFLOWS_EXISTS=$(yq eval '.workflows' "$CONFIG_FILE")
if [ "$WORKFLOWS_EXISTS" != "null" ]; then
    # Validate workflow fields are booleans if present
    for workflow in ci release head-builds fossa; do
        WORKFLOW_VALUE=$(yq eval ".workflows.$workflow" "$CONFIG_FILE")
        if [ "$WORKFLOW_VALUE" != "null" ] && [ "$WORKFLOW_VALUE" != "true" ] && [ "$WORKFLOW_VALUE" != "false" ]; then
            echo "ERROR: workflows.$workflow must be boolean (true/false), got: $WORKFLOW_VALUE" >&2
            exit 1
        fi
    done
fi

echo "✓ Config validation passed: $CONFIG_FILE"
echo "  Branch: $BRANCH"
echo "  K3S versions: $(yq eval '.k3s_versions | join(", ")' "$CONFIG_FILE")"
echo "  Automation-core ref: $AUTOMATION_CORE_REF"
