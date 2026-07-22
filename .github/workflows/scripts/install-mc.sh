#!/bin/bash

set -e
set -x

# renovate: datasource=github-releases depName=minio/mc versioning=loose
MC_VERSION="RELEASE.2025-08-13T08-35-41Z"
# SHA256 for mc.{MC_VERSION} per arch — run update-checksums.sh after changing MC_VERSION
MC_SHA256_amd64="01f866e9c5f9b87c2b09116fa5d7c06695b106242d829a8bb32990c00312e891"
MC_SHA256_arm64="14c8c9616cfce4636add161304353244e8de383b2e2752c0e9dad01d4c27c12c"

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
