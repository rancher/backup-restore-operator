#!/usr/bin/env bash
# update-checksums.sh — refresh all SHA256 values in tool-versions.sh to match current versions.
# Run this after Renovate bumps a version variable, then commit the result.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSIONS_FILE="${SCRIPT_DIR}/tool-versions.sh"

# shellcheck source=tool-versions.sh
source "${VERSIONS_FILE}"

# update_var rewrites a VAR="value" line in tool-versions.sh in-place.
update_var() {
  local var="$1"
  local value="$2"
  perl -i -pe "s|^${var}=.*|${var}=\"${value}\"|" "${VERSIONS_FILE}"
}

echo "==> k3d ${K3D_VERSION}"
k3d_sums=$(curl -fsSL "https://github.com/k3d-io/k3d/releases/download/${K3D_VERSION}/checksums.txt")
for arch in amd64 arm64 arm; do
  hash=$(echo "${k3d_sums}" | awk "/k3d-linux-${arch}\$/ {print \$1}")
  if [[ -z "${hash}" ]]; then
    echo "  WARNING: no checksum found for k3d-linux-${arch} — skipping"
    continue
  fi
  echo "  ${arch}: ${hash}"
  update_var "K3D_SHA256_${arch}" "${hash}"
done

echo "==> mc ${MC_VERSION}"
for arch in amd64 arm64; do
  hash=$(curl -fsSL "https://dl.min.io/client/mc/release/linux-${arch}/archive/mc.${MC_VERSION}.sha256sum" | awk '{print $1}')
  if [[ -z "${hash}" ]]; then
    echo "  WARNING: no checksum found for mc linux-${arch} — skipping"
    continue
  fi
  echo "  ${arch}: ${hash}"
  update_var "MC_SHA256_${arch}" "${hash}"
done

echo "==> oras ${ORAS_VERSION} (note: Renovate auto-updates this — manual run only needed as a fallback)"
oras_bare="${ORAS_VERSION#v}"
oras_sums=$(curl -fsSL "https://github.com/oras-project/oras/releases/download/${ORAS_VERSION}/oras_${oras_bare}_checksums.txt")
hash=$(echo "${oras_sums}" | awk "/oras_${oras_bare}_linux_amd64\.tar\.gz/ {print \$1}")
if [[ -z "${hash}" ]]; then
  echo "  WARNING: no checksum found for oras linux amd64 — skipping"
else
  echo "  amd64: ${hash}"
  update_var "ORAS_SHA256_amd64" "${hash}"
fi

echo ""
echo "Done. Review the diff in tool-versions.sh, then commit alongside the version bump."
