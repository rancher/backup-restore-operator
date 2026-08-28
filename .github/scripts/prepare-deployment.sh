#!/bin/bash
set -euo pipefail

# Prepare deployment matrix for GitHub Actions workflow
# Usage: ./prepare-deployment.sh <mode> <configFile> <branches>

MODE="${1:-}"
CONFIG_FILE="${2:-}"
BRANCHES="${3:-}"

if [ -z "$MODE" ]; then
    echo "ERROR: Mode required (config or bulk)" >&2
    exit 1
fi

# Check required tools
if ! command -v yq &> /dev/null; then
    echo "ERROR: yq is required but not installed" >&2
    exit 1
fi

if ! command -v jq &> /dev/null; then
    echo "ERROR: jq is required but not installed" >&2
    exit 1
fi

# Get automation-core directory (script is in .github/scripts/)
AUTOMATION_CORE_DIR="$(cd "$(dirname "$0")/../.." && pwd)"

build_matrix_entry() {
    local config_file="$1"
    local config_path="${AUTOMATION_CORE_DIR}/${config_file}"

    if [ ! -f "$config_path" ]; then
        echo "ERROR: Config file not found: $config_path" >&2
        return 1
    fi

    # Validate config first
    if ! bash "${AUTOMATION_CORE_DIR}/.github/scripts/validate-config.sh" "$config_path" > /dev/null 2>&1; then
        echo "ERROR: Config validation failed for $config_file" >&2
        return 1
    fi

    # Extract values from config
    local branch=$(yq eval '.branch' "$config_path")
    local k3s_versions=$(yq eval '.k3s_versions | @json' "$config_path")
    local automation_core_ref=$(yq eval '.automation_core_ref' "$config_path")

    # Build matrix entry as JSON
    jq -n \
        --arg branch "$branch" \
        --arg config_file "$config_file" \
        --argjson k3s_versions "$k3s_versions" \
        --arg automation_core_ref "$automation_core_ref" \
        '{
            branch: $branch,
            config_file: $config_file,
            k3s_versions: $k3s_versions,
            automation_core_ref: $automation_core_ref
        }'
}

case "$MODE" in
    config)
        if [ -z "$CONFIG_FILE" ]; then
            echo "ERROR: Config file required for config mode" >&2
            exit 1
        fi

        # Single config deployment
        MATRIX_ENTRY=$(build_matrix_entry "$CONFIG_FILE")
        MATRIX=$(jq -n --argjson entry "$MATRIX_ENTRY" '{include: [$entry]}')
        ;;

    bulk)
        if [ -z "$BRANCHES" ]; then
            echo "ERROR: Branches required for bulk mode" >&2
            exit 1
        fi

        MATRIX_ENTRIES="[]"

        # Handle 'all' keyword
        if [ "$BRANCHES" == "all" ]; then
            # Find all config files
            for config_file in "${AUTOMATION_CORE_DIR}"/configs/*.yaml; do
                if [ -f "$config_file" ]; then
                    # Get relative path from automation-core root
                    rel_path="configs/$(basename "$config_file")"
                    ENTRY=$(build_matrix_entry "$rel_path")
                    MATRIX_ENTRIES=$(echo "$MATRIX_ENTRIES" | jq --argjson entry "$ENTRY" '. += [$entry]')
                fi
            done
        else
            # Parse comma-separated branch names
            IFS=',' read -ra BRANCH_ARRAY <<< "$BRANCHES"
            for branch in "${BRANCH_ARRAY[@]}"; do
                # Trim whitespace
                branch=$(echo "$branch" | xargs)

                # Convert branch name to config filename
                # main -> main.yaml
                # release/v10.x -> release-v10.x.yaml
                config_name=$(echo "$branch" | sed 's|/|-|g')
                config_file="configs/${config_name}.yaml"

                ENTRY=$(build_matrix_entry "$config_file")
                MATRIX_ENTRIES=$(echo "$MATRIX_ENTRIES" | jq --argjson entry "$ENTRY" '. += [$entry]')
            done
        fi

        MATRIX=$(jq -n --argjson entries "$MATRIX_ENTRIES" '{include: $entries}')
        ;;

    *)
        echo "ERROR: Invalid mode: $MODE (must be 'config' or 'bulk')" >&2
        exit 1
        ;;
esac

# Output matrix JSON for GitHub Actions
echo "$MATRIX" | jq -c '.'
