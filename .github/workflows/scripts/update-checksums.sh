#!/usr/bin/env bash
# update-checksums.sh — refresh all SHA256 values in install-mc.sh to match the current MC_VERSION.
# Run this after Renovate bumps MC_VERSION, then commit the result.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_FILE="${SCRIPT_DIR}/install-mc.sh"

# shellcheck source=install-mc.sh
source "${INSTALL_FILE}"

# update_var rewrites a VAR="value" line in install-mc.sh in-place.
update_var() {
  local var="$1"
  local value="$2"
  perl -i -pe "s|^${var}=.*|${var}=\"${value}\"|" "${INSTALL_FILE}"
}

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

echo ""
echo "Done. Review the diff in install-mc.sh, then commit alongside the version bump."
