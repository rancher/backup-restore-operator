#!/bin/bash

set -e
set -x

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=tool-versions.sh
source "${SCRIPT_DIR}/tool-versions.sh"

# initArch discovers the architecture for this system.
initArch() {
  ARCH=$(uname -m)
  case $ARCH in
    armv7*) ARCH="arm";;
    aarch64) ARCH="arm64";;
    x86_64) ARCH="amd64";;
  esac
}

initArch

expected_sha256_var="MC_SHA256_${ARCH}"
expected_sha256="${!expected_sha256_var}"

if [[ -z "${expected_sha256}" ]]; then
  echo "No hardcoded SHA256 for arch ${ARCH} — run update-checksums.sh after bumping MC_VERSION"
  exit 1
fi

curl -sL --fail "https://dl.min.io/client/mc/release/linux-${ARCH}/archive/mc.${MC_VERSION}" > mc

echo "${expected_sha256}  mc" | sha256sum -c

chmod +x mc
cp mc /usr/local/bin/mc

mc --version
