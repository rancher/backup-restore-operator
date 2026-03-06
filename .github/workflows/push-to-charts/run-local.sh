#!/usr/bin/env bash
# Local entry point for running the chart update workflow against a local charts clone.
#
# Usage:
#   ./run-local.sh --charts-dir /path/to/rancher/charts --tag v9.0.2-rc.5 [OPTIONS]
#
# Options:
#   --charts-dir DIR    Path to local rancher/charts clone (required)
#   --tag TAG           BRO release tag to process (e.g. v9.0.2-rc.5) (required)
#   --remote NAME       Remote name for rancher/charts in CHARTS_DIR (default: origin)
#   --source-repo REPO  Source repo for PR body (default: rancher/backup-restore-operator)
#   --dry-run           Skip push and PR creation (all local git work still runs)
#   --help              Show this message
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

usage() {
  sed -n '/^# Usage/,/^[^#]/{ s/^# \{0,1\}//; /^[^#]/d; p }' "$0"
  exit 0
}

TAG=""
SOURCE_REPO="rancher/backup-restore-operator"

while [[ $# -gt 0 ]]; do
  case $1 in
    --charts-dir)  CHARTS_DIR="$2";    shift 2 ;;
    --tag)         TAG="$2";           shift 2 ;;
    --remote)      CHARTS_REMOTE="$2"; shift 2 ;;
    --source-repo) SOURCE_REPO="$2";   shift 2 ;;
    --dry-run)     DRY_RUN="true";     shift ;;
    --help|-h)     usage ;;
    *) echo "Unknown option: $1" >&2; usage ;;
  esac
done

require_var TAG
require_charts_dir

TARGET_BRANCH=$(get_charts_branch "$TAG")
export TAG TARGET_BRANCH CHARTS_DIR CHARTS_REMOTE DRY_RUN BRO_DIR SOURCE_REPO

echo "Tag:           $TAG"
echo "Target branch: $TARGET_BRANCH"
echo "Charts dir:    $CHARTS_DIR"
echo "Dry run:       $DRY_RUN"
echo ""

git -C "$CHARTS_DIR" checkout -B "$TARGET_BRANCH" "$CHARTS_REMOTE/$TARGET_BRANCH"

BRANCH_NAME="bot/bro-${TAG}-$(date +%s)"
git -C "$CHARTS_DIR" checkout -b "$BRANCH_NAME"
export BRANCH_NAME

echo "--- Removing previous RC (if applicable) ---"
bash "$SCRIPT_DIR/remove-previous-rc.sh"

echo "--- Updating package.yaml ---"
bash "$SCRIPT_DIR/update-package-yaml.sh"

echo "--- Updating chart patches ---"
bash "$SCRIPT_DIR/update-patches.sh"

echo "--- Generating chart assets ---"
bash "$SCRIPT_DIR/generate-assets.sh"

echo "--- Updating release.yaml ---"
bash "$SCRIPT_DIR/update-release-yaml.sh"

echo "--- Creating PR ---"
bash "$SCRIPT_DIR/create-pr.sh"

echo ""
echo "Workflow complete."
