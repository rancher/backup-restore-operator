#!/usr/bin/env bash
# Regenerate chart patches after updating package.yaml.
#
# Prerequisites: update-package-yaml.sh
#
# Inputs (env):
#   CHARTS_DIR - path to rancher/charts clone (required)
#   PACKAGE    - package name (set by common.sh: "rancher-backup")
set -euo pipefail
source "$(dirname "$0")/common.sh"

require_var TAG
require_charts_dir

# Update appVersion context line in Chart.yaml.patch to match the new tag so
# make prepare applies cleanly (mismatched context causes .orig file creation).
CHART_PATCH="$CHARTS_DIR/packages/$PACKAGE/generated-changes/patch/Chart.yaml.patch"
if [ -f "$CHART_PATCH" ]; then
  awk -v tag="$TAG" '{sub(/appVersion: v[^ ]*/, "appVersion: " tag)}1' "$CHART_PATCH" > "$CHART_PATCH.tmp" && mv "$CHART_PATCH.tmp" "$CHART_PATCH"
  summary "  - Updated appVersion in Chart.yaml.patch to $TAG"
fi

make -C "$CHARTS_DIR" prepare PACKAGE="$PACKAGE" USE_CACHE=true
make -C "$CHARTS_DIR" patch PACKAGE="$PACKAGE" USE_CACHE=true
make -C "$CHARTS_DIR" clean

commit_if_changed "chore(charts): Refresh rancher-backup chart patches"
summary "  - Patches refreshed"
