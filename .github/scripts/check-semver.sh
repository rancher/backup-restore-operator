#!/usr/bin/env bash

# Usage:
#   ./check_tag.sh [tag]
# If no tag is provided, it uses GITHUB_REF_NAME environment variable.

set -euo pipefail

# Input: tag
tag="${1:-${GITHUB_REF_NAME:-}}"

if [[ -z "$tag" ]]; then
  echo "Error: No tag provided and GITHUB_REF_NAME is not set." >&2
  exit 1
fi

# Strip leading 'v' if present
if [[ "$tag" == v* ]]; then
  tag="${tag:1}"
fi

# Function to detect prerelease (e.g. 1.2.3-beta)
has_prerelease() {
  [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+- ]]
}

# Function to detect build metadata (e.g. 1.2.3+build.1)
has_build_meta() {
  [[ "$1" =~ \+ ]]
}

# Output results to stdout
if has_prerelease "$tag"; then
  echo "HAS_PRERELEASE=true"
else
  echo "HAS_PRERELEASE=false"
fi

if has_build_meta "$tag"; then
  echo "HAS_BUILD_META=true"
else
  echo "HAS_BUILD_META=false"
fi
