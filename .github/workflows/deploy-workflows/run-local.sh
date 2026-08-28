#!/usr/bin/env bash
# Local entry point for deploying workflows to release branches
#
# Usage:
#   ./run-local.sh --config configs/main.yaml --target-dir /path/to/backup-restore-operator [OPTIONS]
#
# Options:
#   --config FILE       Config file to use (e.g., configs/main.yaml) (required for single mode)
#   --branches LIST     Comma-separated branches or 'all' (required for bulk mode)
#   --target-dir DIR    Path to backup-restore-operator clone (required)
#   --remote NAME       Remote name in target repo (default: origin)
#   --dry-run           Show diffs only, don't create branch or PR
#   --help              Show this message
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

usage() {
  sed -n '/^# Usage/,/^set/{
    s/^# \{0,1\}//
    /^set/d
    p
  }' "$0"
  exit 0
}

CONFIG_FILE=""
BRANCHES=""
TARGET_DIR=""
REMOTE="origin"
DRY_RUN="false"

while [[ $# -gt 0 ]]; do
  case $1 in
    --config)      CONFIG_FILE="$2"; shift 2 ;;
    --branches)    BRANCHES="$2";    shift 2 ;;
    --target-dir)  TARGET_DIR="$2";  shift 2 ;;
    --remote)      REMOTE="$2";      shift 2 ;;
    --dry-run)     DRY_RUN="true";   shift ;;
    --help|-h)     usage ;;
    *) echo "Unknown option: $1" >&2; usage ;;
  esac
done

require_var TARGET_DIR "TARGET_DIR (--target-dir)"

# Determine mode
if [[ -n "$CONFIG_FILE" ]]; then
  MODE="config"
  require_var CONFIG_FILE
elif [[ -n "$BRANCHES" ]]; then
  MODE="bulk"
else
  echo "ERROR: Must specify either --config or --branches" >&2
  usage
fi

export MODE CONFIG_FILE BRANCHES TARGET_DIR REMOTE DRY_RUN

echo "Mode:          $MODE"
echo "Target dir:    $TARGET_DIR"
echo "Dry run:       $DRY_RUN"
echo ""

# Build matrix
bash "$SCRIPT_DIR/build-matrix.sh"

# Deploy each entry
bash "$SCRIPT_DIR/deploy-entries.sh"

echo ""
echo "Deployment complete."
