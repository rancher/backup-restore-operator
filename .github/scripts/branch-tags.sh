#!/bin/bash

# Exit immediately if a command exits with a non-zero status
set -e

# Function to get the previous tag
getPreviousTag() {
  local tagPrefix="$1"
  # List all tags and filter ones that start with tagPrefix, sort by creation date
  git tag --sort=-creatordate | grep "^${tagPrefix}" | head -n 1
}

# Determine if we're in a GitHub Actions environment
if [ -n "$GITHUB_REF" ] && [ -n "$GITHUB_SHA" ]; then
  # Use GHA environment variables
  ref="$GITHUB_REF"
  commitSha="${GITHUB_SHA:0:7}"
else
  # Fallback to local Git repo
  if [ ! -d ".git" ]; then
    echo "This script must be run from the root of a Git repository or GitHub Actions."
    exit 1
  fi
  ref=$(git symbolic-ref HEAD)
  commitSha=$(git rev-parse --short HEAD)
fi

branchTag=""
branchStaticTag=""
prevTag=""

if [ "$ref" == "refs/heads/main" ]; then
  branchTag="head"
  branchStaticTag="main-${commitSha}"
  prevTag=$(getPreviousTag "main-")
elif [[ "$ref" == refs/heads/release/* ]]; then
  version="${ref#refs/heads/release/}"  # Extract "vX.0"
  branchTag="${version}-head"
  branchStaticTag="${version}-head-${commitSha}"
  prevTag=$(getPreviousTag "${version}-head-")
else
  echo "Unsupported branch pattern. Expected 'main' or 'release/*'."
  exit 1
fi

# Output the results
echo "branch_tag=${branchTag}"
echo "branch_static_tag=${branchStaticTag}"
echo "prev_static_tag=${prevTag}"