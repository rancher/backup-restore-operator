#!/usr/bin/env bash
# Deploy workflows for each matrix entry
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

AUTOMATION_CORE_DIR=$(get_automation_core_dir)
MATRIX_JSON=$(cat /tmp/deploy-matrix.json)
ENTRY_COUNT=$(echo "$MATRIX_JSON" | jq '.include | length')

for i in $(seq 0 $((ENTRY_COUNT - 1))); do
  ENTRY=$(echo "$MATRIX_JSON" | jq ".include[$i]")
  
  export BRANCH=$(echo "$ENTRY" | jq -r '.branch')
  export CONFIG_FILE=$(echo "$ENTRY" | jq -r '.config_file')
  export K3S_VERSIONS=$(echo "$ENTRY" | jq -r '.k3s_versions | @json')
  export AUTOMATION_CORE_REF=$(echo "$ENTRY" | jq -r '.automation_core_ref')
  
  summary "## Deploying to $BRANCH"
  summary "- Config: $CONFIG_FILE"
  summary "- K3S versions: $(echo "$K3S_VERSIONS" | jq -r '. | join(", ")')"
  summary "- Automation-core ref: @$AUTOMATION_CORE_REF"
  summary ""
  
  # Deploy this entry
  bash "$SCRIPT_DIR/deploy-single.sh"
done
