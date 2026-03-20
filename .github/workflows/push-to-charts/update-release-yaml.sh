#!/usr/bin/env bash
# Prepend the new combined version to rancher-backup (and rancher-backup-crd) in release.yaml.
#
# Prerequisites: generate-assets.sh
#
# Inputs (env):
#   TAG        - BRO release tag (e.g. v9.0.2-rc.5) (required)
#   CHARTS_DIR - path to rancher/charts clone (required)
#   PACKAGE    - package name (set by common.sh: "rancher-backup")
set -euo pipefail
source "$(dirname "$0")/common.sh"

require_var TAG
require_charts_dir

TAG_NO_V="${TAG#v}"
CHARTS_VERSION=$(yq e '.version' "$CHARTS_DIR/packages/$PACKAGE/package.yaml")
COMBINED_VERSION="${CHARTS_VERSION}+up${TAG_NO_V}"

RELEASE_YAML="$CHARTS_DIR/release.yaml"

yq e -i ".${PACKAGE} |= [\"${COMBINED_VERSION}\"] + ." "$RELEASE_YAML"
summary "  - Added \`${PACKAGE}\`: \`${COMBINED_VERSION}\`"

CRD_PACKAGE="${PACKAGE}-crd"
if yq e ".${CRD_PACKAGE}" "$RELEASE_YAML" &>/dev/null; then
  yq e -i ".${CRD_PACKAGE} |= [\"${COMBINED_VERSION}\"] + ." "$RELEASE_YAML"
  summary "  - Added \`${CRD_PACKAGE}\`: \`${COMBINED_VERSION}\`"
fi

if ! git -C "$CHARTS_DIR" diff --quiet --exit-code -- release.yaml; then
  git -C "$CHARTS_DIR" add release.yaml
  git -C "$CHARTS_DIR" commit -m "chore(charts): Update release.yaml for rancher-backup ${COMBINED_VERSION}"
fi
