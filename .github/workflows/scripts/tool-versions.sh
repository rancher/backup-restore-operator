#!/usr/bin/env bash
# tool-versions.sh — single source of truth for pinned CI tool versions and checksums.
# k3d and ORAS: Renovate auto-updates both version and checksums (github-release-attachments).
# mc: Renovate updates the version only — run update-checksums.sh after a version bump.

# renovate: datasource=github-release-attachments depName=k3d-io/k3d
K3D_VERSION="v5.8.3"
# renovate: datasource=github-release-attachments depName=k3d-io/k3d digestVersion=v5.8.3
K3D_SHA256_amd64="dbaa79a76ace7f4ca230a1ff41dc7d8a5036a8ad0309e9c54f9bf3836dbe853e"
# renovate: datasource=github-release-attachments depName=k3d-io/k3d digestVersion=v5.8.3
K3D_SHA256_arm64="0b8110f2229631af7402fb828259330985918b08fefd38b7f1b788a1c8687216"
# renovate: datasource=github-release-attachments depName=k3d-io/k3d digestVersion=v5.8.3
K3D_SHA256_arm="c4ef4e8008edb55cf347d846a7fc70af319883f9a474167689bd8af04693401d"

# renovate: datasource=github-releases depName=minio/mc versioning=loose
MC_VERSION="RELEASE.2025-08-13T08-35-41Z"
# SHA256 for mc.{MC_VERSION} per arch — run update-checksums.sh after changing MC_VERSION
MC_SHA256_amd64="01f866e9c5f9b87c2b09116fa5d7c06695b106242d829a8bb32990c00312e891"
MC_SHA256_arm64="14c8c9616cfce4636add161304353244e8de383b2e2752c0e9dad01d4c27c12c"

# renovate: datasource=github-release-attachments depName=oras-project/oras
ORAS_VERSION="v1.2.0"
# renovate: datasource=github-release-attachments depName=oras-project/oras digestVersion=v1.2.0
ORAS_SHA256_amd64="5b3f1cbb86d869eee68120b9b45b9be983f3738442f87ee5f06b00edd0bab336"
