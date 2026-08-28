#!/usr/bin/env bash
# Build deployment matrix from config/branches input
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

AUTOMATION_CORE_DIR=$(get_automation_core_dir)

# Build matrix using prepare-deployment.sh
MATRIX_JSON=$("$AUTOMATION_CORE_DIR/.github/scripts/prepare-deployment.sh" \
  "$MODE" \
  "${CONFIG_FILE:-}" \
  "${BRANCHES:-}")

# Export as file for deploy-entries.sh to read
echo "$MATRIX_JSON" > /tmp/deploy-matrix.json

# Show matrix
summary "## Deployment Matrix"
summary '```json'
summary "$(echo "$MATRIX_JSON" | jq '.')"
summary '```'
summary ""

# Count entries
ENTRY_COUNT=$(echo "$MATRIX_JSON" | jq '.include | length')
echo "Will deploy to $ENTRY_COUNT branch(es)"
