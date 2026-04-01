#!/usr/bin/env bash
# Push the working branch to rancher/charts and open a PR.
#
# Inputs (env):
#   CHARTS_DIR     - path to rancher/charts clone (required)
#   TARGET_BRANCH  - base branch for the PR (required)
#   BRANCH_NAME    - working branch to push (required)
#   TAG            - BRO release tag, used in PR title (required)
#   SOURCE_REPO    - source repo for PR body (default: rancher/backup-restore-operator)
#   DRY_RUN        - skip push and PR creation if "true"
set -euo pipefail
source "$(dirname "$0")/common.sh"

require_charts_dir
require_var TARGET_BRANCH
require_var BRANCH_NAME
require_var TAG

SOURCE_REPO="${SOURCE_REPO:-rancher/backup-restore-operator}"

# Build the combined version string: {charts_version}+up{bro_version_without_v}
TAG_NO_V="${TAG#v}"
CHARTS_VERSION=$(yq e '.version' "$CHARTS_DIR/packages/$PACKAGE/package.yaml")
COMBINED_VERSION="${CHARTS_VERSION}+up${TAG_NO_V}"

# Determine action: prerelease tags are a "bump", stable tags are an "UnRC"
if [[ "$TAG_NO_V" == *"-"* ]]; then
  ACTION="bump"
else
  ACTION="UnRC"
fi

PR_TITLE="[${TARGET_BRANCH}] rancher-backup ${COMBINED_VERSION} ${ACTION}"
PR_BODY="Automated PR to update \`rancher-backup\` and \`rancher-backup-crd\` charts from [${SOURCE_REPO}](https://github.com/${SOURCE_REPO}/releases/tag/${TAG}) release \`${TAG}\`."

if [ "$DRY_RUN" = "true" ]; then
  echo "[DRY RUN] Skipping push and PR creation."
  echo "  Branch: $BRANCH_NAME"
  echo "  Title:  $PR_TITLE"
  echo "  Base:   $TARGET_BRANCH"
  echo "  Version: $COMBINED_VERSION"
  echo "All commits are in your local $CHARTS_DIR checkout."
  exit 0
fi

git -C "$CHARTS_DIR" push "$CHARTS_REMOTE" "$BRANCH_NAME"
PR_URL=$(gh pr create \
  --title "$PR_TITLE" \
  --body "$PR_BODY" \
  --base "$TARGET_BRANCH" \
  --head "$BRANCH_NAME" \
  --repo rancher/charts)

summary "  - Created PR: $PR_URL"
echo "$PR_URL"
