#!/usr/bin/env bash
# GHA entry point for deploying workflows
# Called from deploy-workflows.yml after token generation
#
# Required env vars:
#   MODE              - 'config' or 'bulk'
#   CONFIG_FILE       - config file (if mode=config)
#   BRANCHES          - branches list (if mode=bulk)
#   TARGET_DIR        - path to target repo checkout
#   GH_TOKEN          - GitHub token
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

require_var MODE
require_var TARGET_DIR

export REMOTE="origin"
export DRY_RUN="${DRY_RUN:-false}"

summary "## Deploy Workflows"
summary "- Mode: $MODE"
if [[ "$MODE" == "config" ]]; then
  summary "- Config: $CONFIG_FILE"
else
  summary "- Branches: $BRANCHES"
fi
summary "- Dry run: $DRY_RUN"
summary ""

# Configure git in target repo
git -C "$TARGET_DIR" config user.name "${GIT_USER_NAME:-github-actions[bot]}"
git -C "$TARGET_DIR" config user.email "${GIT_USER_EMAIL:-github-actions[bot]@users.noreply.github.com}"

# Build matrix
bash "$SCRIPT_DIR/build-matrix.sh"

# Deploy entries
bash "$SCRIPT_DIR/deploy-entries.sh"

summary ""
summary "## Deployment Complete"
