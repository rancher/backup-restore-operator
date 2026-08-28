#!/usr/bin/env bash
# Common functions for workflow deployment (local and GHA)

require_var() {
  local var_name="${1}"
  local display_name="${2:-$var_name}"
  if [[ -z "${!var_name:-}" ]]; then
    echo "ERROR: $display_name is required but not set" >&2
    exit 1
  fi
}

summary() {
  # In GHA, write to GITHUB_STEP_SUMMARY. Locally, just echo.
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    echo "$@" >> "$GITHUB_STEP_SUMMARY"
  fi
  echo "$@"
}

# Get automation-core directory (3 levels up from this script)
get_automation_core_dir() {
  cd "$(dirname "$0")/../../.." && pwd
}
