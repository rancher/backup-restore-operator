#!/usr/bin/env bash
# Shared setup for push-to-charts scripts. Source this file: source "$(dirname "$0")/common.sh"

# Determine BRO_DIR (backup-restore-operator root) from this script's location
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BRO_DIR="${BRO_DIR:-$(cd "$SCRIPT_DIR/../../.." && pwd)}"

# Required: path to a local rancher/charts clone
CHARTS_DIR="${CHARTS_DIR:-}"

# Remote name for rancher/charts in CHARTS_DIR (may differ locally if using a fork)
CHARTS_REMOTE="${CHARTS_REMOTE:-origin}"

# Skip git commits, push, and PR creation when true
DRY_RUN="${DRY_RUN:-false}"

# Static package name for backup-restore-operator in rancher/charts
PACKAGE="rancher-backup"

# Map of BRO major version → rancher/charts target branch.
# Add a new entry here when a new BRO/Rancher version pair is created.
declare -A CHARTS_BRANCH_MAP=(
  ["10"]="dev-v2.14"
  ["9"]="dev-v2.13"
  ["8"]="dev-v2.12"
  ["7"]="dev-v2.11"
  ["6"]="dev-v2.10"
  ["5"]="dev-v2.9"
)

# Resolve the rancher/charts target branch for a given BRO tag (e.g. v9.0.2-rc.5).
# Prints the branch name and exits 1 if the major version is not in the map.
get_charts_branch() {
  local tag="${1#v}"  # strip leading v
  local major
  major=$(echo "$tag" | cut -d. -f1)
  local branch="${CHARTS_BRANCH_MAP[$major]:-}"
  if [ -z "$branch" ]; then
    echo "ERROR: No rancher/charts branch configured for BRO major version '$major' (tag: $1)" >&2
    echo "ERROR: Add an entry to CHARTS_BRANCH_MAP in common.sh" >&2
    exit 1
  fi
  echo "$branch"
}

# Write to GitHub step summary if available, and always print to stdout
summary() {
  if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    echo "$@" >> "$GITHUB_STEP_SUMMARY"
  fi
  echo "$@"
}

require_var() {
  local var="$1"
  if [ -z "${!var:-}" ]; then
    echo "ERROR: $var is required" >&2
    exit 1
  fi
}

require_charts_dir() {
  require_var CHARTS_DIR
  if [ ! -d "$CHARTS_DIR" ]; then
    echo "ERROR: CHARTS_DIR '$CHARTS_DIR' does not exist" >&2
    exit 1
  fi
}

# Returns 0 if the given TARGET_BRANCH is frozen, 1 otherwise.
is_branch_frozen() {
  local target_branch="$1"
  local freeze_manifest="${2:-/tmp/bro-push/code-freeze.yaml}"
  if [ ! -f "$freeze_manifest" ]; then
    return 1
  fi
  local manifest_branch_name
  manifest_branch_name=$(echo "$target_branch" | sed 's|dev-v|release/v|')
  local manifest_ref="refs/heads/$manifest_branch_name"
  local result
  result=$(yq e ".spec.forProvider.conditions[].refName[].include[] | select(. == \"$manifest_ref\")" "$freeze_manifest")
  [ -n "$result" ]
}

# Commit all changes in CHARTS_DIR if any exist. Does nothing if tree is clean.
commit_if_changed() {
  local message="$1"
  if git -C "$CHARTS_DIR" diff --quiet --exit-code && [ -z "$(git -C "$CHARTS_DIR" status --porcelain)" ]; then
    return 0
  fi
  git -C "$CHARTS_DIR" add .
  git -C "$CHARTS_DIR" commit -m "$message"
}
