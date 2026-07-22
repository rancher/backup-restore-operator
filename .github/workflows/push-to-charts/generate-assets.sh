#!/usr/bin/env bash
# Generate chart assets from the updated package.yaml and patches.
#
# Prerequisites: update-patches.sh
#
# Inputs (env):
#   CHARTS_DIR - path to rancher/charts clone (required)
#   PACKAGE    - package name (set by common.sh: "rancher-backup")
set -euo pipefail
source "$(dirname "$0")/common.sh"

require_charts_dir

make -C "$CHARTS_DIR" charts PACKAGE="$PACKAGE" USE_CACHE=true

commit_if_changed "chore(charts): Generate rancher-backup chart assets"
summary "  - Chart assets generated"
