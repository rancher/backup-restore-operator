#!/usr/bin/env bash
# Reads the GitHub release of a tag, and prints the tag of the release, which is the
# version the charts are staged for. The charts are staged only for a release that
# is published and has its chart archives: the release is made public after the
# images are pushed, and its chart archives are made by the release itself, so a
# release that has both is a release that finished.
#
# Usage: read-release.sh <tag>
#
# Environment:
#   GH_TOKEN           can read the releases of the repository
#   GITHUB_REPOSITORY  the repository, such as rancher/backup-restore-operator
#
# Requires gh and jq on PATH.
set -euo pipefail

fail() {
  echo "::error::$1" >&2
  exit 1
}

TAG=${1:?usage: $0 <tag>}
REPOSITORY=${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is missing}

[[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]] ||
  fail "The tag '$TAG' must look like vX.Y.Z or vX.Y.Z-rc.N"

# A release that is still a draft is not visible to a token that only reads.
release=$(gh release view "$TAG" --repo "$REPOSITORY" --json tagName,isDraft,assets 2>/dev/null) ||
  fail "There is no published release for the tag $TAG in $REPOSITORY"

[ "$(jq -r .isDraft <<<"$release")" = false ] ||
  fail "The release $TAG is still a draft: its images may not be published yet"

release_tag=$(jq -r .tagName <<<"$release")
[ "$release_tag" = "$TAG" ] ||
  fail "The release of the tag $TAG is for the tag $release_tag"

version=${release_tag#v}
missing=()
for archive in "rancher-backup-${version}.tgz" "rancher-backup-crd-${version}.tgz"; do
  jq -e --arg name "$archive" 'any(.assets[]; .name == $name)' <<<"$release" >/dev/null || missing+=("$archive")
done
[ "${#missing[@]}" -eq 0 ] ||
  fail "The release $release_tag has no ${missing[*]}, so it did not finish"

echo "$release_tag"
