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

expected_sha256_var="K3D_SHA256_${ARCH}"
expected_sha256="${!expected_sha256_var}"

if [[ -z "${expected_sha256}" ]]; then
  echo "No hardcoded SHA256 for arch ${ARCH} — run update-checksums.sh after bumping K3D_VERSION"
  exit 1
fi

echo "Downloading k3d@${K3D_VERSION}"
curl -sL --fail "https://github.com/k3d-io/k3d/releases/download/${K3D_VERSION}/k3d-linux-${ARCH}" > k3d

echo "${expected_sha256}  k3d" | sha256sum -c

chmod +x k3d
cp k3d /usr/local/bin/k3d

k3d version
