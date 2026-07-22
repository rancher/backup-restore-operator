#!/usr/bin/env bash
# GHA entry point: orchestrates the full chart update workflow.
# Called from push-to-charts.yaml after token generation.
#
# Required env vars (set by push-to-charts.yaml):
#   TAG         - BRO release tag (e.g. v9.0.2-rc.5)
#   GH_TOKEN    - GitHub app token with access to rancher/charts
#   SOURCE_REPO - source repo (github.repository)
#   BRO_DIR     - path to backup-restore-operator workspace ($GITHUB_WORKSPACE)
#   CHARTS_DIR  - path to clone rancher/charts into
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

require_var TAG
require_var GH_TOKEN

export BRO_DIR CHARTS_DIR DRY_RUN

TARGET_BRANCH=$(get_charts_branch "$TAG")
export TARGET_BRANCH

summary "## Push to rancher/charts"
summary "- Tag: \`$TAG\`"
summary "- Target branch: \`$TARGET_BRANCH\`"

# --- Check code freeze ---
FREEZE_MANIFEST="/tmp/bro-push/code-freeze.yaml"
mkdir -p "$(dirname "$FREEZE_MANIFEST")"
curl -sf -H "Authorization: token $GH_TOKEN" \
  "https://raw.githubusercontent.com/rancher/org/refs/heads/main/manifests/resources/RepositoryRuleset/rancher/code-freeze.yaml" \
  -o "$FREEZE_MANIFEST" || echo "WARNING: Could not fetch code-freeze manifest, freeze check skipped" >&2

if is_branch_frozen "$TARGET_BRANCH" "$FREEZE_MANIFEST"; then
  MANIFEST_BRANCH=$(echo "$TARGET_BRANCH" | sed 's|dev-v|release/v|')
  summary "- Branch \`$MANIFEST_BRANCH\` is currently frozen. Skipping."
  exit 0
fi

# --- Clone downstream repo ---
git clone "https://oauth2:${GH_TOKEN}@github.com/rancher/charts.git" "$CHARTS_DIR"
git -C "$CHARTS_DIR" config user.name "github-actions[bot]"
git -C "$CHARTS_DIR" config user.email "github-actions[bot]@users.noreply.github.com"

git -C "$CHARTS_DIR" checkout -B "$TARGET_BRANCH" "$CHARTS_REMOTE/$TARGET_BRANCH"

BRANCH_NAME="bot/bro-${TAG}-$(date +%s)"
git -C "$CHARTS_DIR" checkout -b "$BRANCH_NAME"
export BRANCH_NAME

summary ""
summary "## Steps"

summary "- Removing previous RC (if applicable)..."
bash "$SCRIPT_DIR/remove-previous-rc.sh"

summary "- Updating package.yaml..."
bash "$SCRIPT_DIR/update-package-yaml.sh"

summary "- Updating chart patches..."
bash "$SCRIPT_DIR/update-patches.sh"

summary "- Generating chart assets..."
bash "$SCRIPT_DIR/generate-assets.sh"

summary "- Updating release.yaml..."
bash "$SCRIPT_DIR/update-release-yaml.sh"

summary "- Pushing changes and creating PR..."
export SOURCE_REPO="${SOURCE_REPO:-rancher/backup-restore-operator}"
bash "$SCRIPT_DIR/create-pr.sh"

summary ""
summary "## Workflow Complete"
