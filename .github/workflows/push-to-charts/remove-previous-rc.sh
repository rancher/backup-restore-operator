#!/usr/bin/env bash
# Remove the previous RC charts version when publishing a new RC or stable for the same base version.
# Skipped automatically if the old BRO base version differs from the new one (no RC to clean up).
#
# Prerequisites: none (runs before update-package-yaml.sh)
#
# Inputs (env):
#   TAG        - new BRO release tag (e.g. v9.0.2-rc.5 or v9.0.2) (required)
#   CHARTS_DIR - path to rancher/charts clone (required)
#   PACKAGE    - package name (set by common.sh: "rancher-backup")
#
# Output: git commit in CHARTS_DIR (only if removal was performed)
set -euo pipefail
source "$(dirname "$0")/common.sh"

require_var TAG
require_charts_dir

PACKAGE_YAML="$CHARTS_DIR/packages/$PACKAGE/package.yaml"

# Extract old BRO version from current URL and strip any prerelease suffix
CURRENT_URL=$(yq e '.url' "$PACKAGE_YAML")
OLD_BRO_VERSION=$(echo "$CURRENT_URL" | sed 's|.*/rancher-backup-||' | sed 's|\.tgz$||')
OLD_BRO_BASE=$(echo "$OLD_BRO_VERSION" | sed 's/-.*//')

# New BRO base version (strip prerelease)
TAG_NO_V="${TAG#v}"
NEW_BRO_BASE=$(echo "$TAG_NO_V" | sed 's/-.*//')

# Only remove if the base version is unchanged AND the old version was a prerelease
if [ "$OLD_BRO_BASE" != "$NEW_BRO_BASE" ]; then
  summary "  - Base version changed ($OLD_BRO_BASE → $NEW_BRO_BASE), no RC to remove"
  exit 0
fi

if [[ "$OLD_BRO_VERSION" != *"-"* ]]; then
  summary "  - Old version $OLD_BRO_VERSION is not a prerelease, nothing to remove"
  exit 0
fi

PREV_CHARTS_VERSION=$(yq e '.version' "$PACKAGE_YAML")
summary "  - Removing previous RC: $PACKAGE $PREV_CHARTS_VERSION"

make -C "$CHARTS_DIR" remove CHART="$PACKAGE" VERSION="$PREV_CHARTS_VERSION"
make -C "$CHARTS_DIR" remove CHART="${PACKAGE}-crd" VERSION="$PREV_CHARTS_VERSION"

commit_if_changed "chore(charts): Remove previous RC rancher-backup $PREV_CHARTS_VERSION"
