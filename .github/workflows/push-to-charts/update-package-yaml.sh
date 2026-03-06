#!/usr/bin/env bash
# Update packages/rancher-backup/package.yaml in rancher/charts for a new BRO release.
#
# Inputs (env):
#   TAG         - BRO release tag (e.g. v9.0.2-rc.5) (required)
#   CHARTS_DIR  - path to rancher/charts clone (required)
#   PACKAGE     - package name (set by common.sh: "rancher-backup")
#
# Updates:
#   url                              → new chart tarball URL for TAG
#   additionalCharts[0].upstreamOptions.url → new CRD chart tarball URL for TAG
#   version                          → auto-incremented: major if BRO major changed, minor if BRO minor changed, else patch
#
# Output: git commit in CHARTS_DIR
set -euo pipefail
source "$(dirname "$0")/common.sh"

require_var TAG
require_charts_dir

BRO_REPO_URL="https://github.com/rancher/backup-restore-operator"
TAG_NO_V="${TAG#v}"  # e.g. 9.0.2-rc.5

PACKAGE_YAML="$CHARTS_DIR/packages/$PACKAGE/package.yaml"
if [ ! -f "$PACKAGE_YAML" ]; then
  echo "ERROR: package.yaml not found at $PACKAGE_YAML" >&2
  exit 1
fi

# --- Compute new URLs ---
NEW_URL="${BRO_REPO_URL}/releases/download/${TAG}/rancher-backup-${TAG_NO_V}.tgz"
NEW_CRD_URL="${BRO_REPO_URL}/releases/download/${TAG}/rancher-backup-crd-${TAG_NO_V}.tgz"

# --- Compute new charts version ---
# Read old BRO version from the current URL and strip any prerelease suffix
CURRENT_URL=$(yq e '.url' "$PACKAGE_YAML")
OLD_BRO_VERSION=$(echo "$CURRENT_URL" | sed 's|.*/rancher-backup-||' | sed 's|\.tgz$||')
OLD_BRO_BASE=$(echo "$OLD_BRO_VERSION" | sed 's/-.*//')

# New BRO base version (strip prerelease)
NEW_BRO_BASE=$(echo "$TAG_NO_V" | sed 's/-.*//')

CURRENT_CHARTS_VERSION=$(yq e '.version' "$PACKAGE_YAML")
CHARTS_MAJOR=$(echo "$CURRENT_CHARTS_VERSION" | cut -d. -f1)
CHARTS_MINOR=$(echo "$CURRENT_CHARTS_VERSION" | cut -d. -f2)
CHARTS_PATCH=$(echo "$CURRENT_CHARTS_VERSION" | cut -d. -f3)

OLD_BRO_MAJOR=$(echo "$OLD_BRO_BASE" | cut -d. -f1)
NEW_BRO_MAJOR=$(echo "$NEW_BRO_BASE" | cut -d. -f1)
OLD_BRO_MINOR=$(echo "$OLD_BRO_BASE" | cut -d. -f2)
NEW_BRO_MINOR=$(echo "$NEW_BRO_BASE" | cut -d. -f2)

if [ "$OLD_BRO_BASE" = "$NEW_BRO_BASE" ]; then
  # Same base BRO version (RC→RC or RC→stable): version was already bumped, keep it
  NEW_CHARTS_VERSION="$CURRENT_CHARTS_VERSION"
elif [ "$NEW_BRO_MAJOR" != "$OLD_BRO_MAJOR" ]; then
  # BRO major version changed (new Rancher version line seeded from previous branch)
  NEW_CHARTS_VERSION="$((CHARTS_MAJOR + 1)).0.0"
elif [ "$NEW_BRO_MINOR" != "$OLD_BRO_MINOR" ]; then
  # BRO minor version changed
  NEW_CHARTS_VERSION="${CHARTS_MAJOR}.$((CHARTS_MINOR + 1)).0"
else
  # BRO patch version changed
  NEW_CHARTS_VERSION="${CHARTS_MAJOR}.${CHARTS_MINOR}.$((CHARTS_PATCH + 1))"
fi

# --- Apply updates ---
yq e -i ".url = \"$NEW_URL\"" "$PACKAGE_YAML"
yq e -i ".additionalCharts[0].upstreamOptions.url = \"$NEW_CRD_URL\"" "$PACKAGE_YAML"
yq e -i ".version = \"$NEW_CHARTS_VERSION\"" "$PACKAGE_YAML"


summary "  - BRO: \`$OLD_BRO_VERSION\` → \`$TAG_NO_V\`"
summary "  - Charts version: \`$CURRENT_CHARTS_VERSION\` → \`$NEW_CHARTS_VERSION\`"

commit_if_changed "chore(charts): Update rancher-backup package.yaml for $TAG"
