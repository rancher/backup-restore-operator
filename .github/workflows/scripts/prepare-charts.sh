#!/usr/bin/env bash
# Prepares the charts of a release for the ship-family-staged action of
# rancher/charts: copies rancher-backup and rancher-backup-crd from a checkout of
# the release tag to <output dir>/<chart>, and fills in the copies with what the
# tag gives, the way scripts/package-helm does for the GitHub release. The action
# stamps, checks, packages and pushes the copies, so the charts in the repository
# stay as they are, and the push-to-charts workflow keeps working with them.
#
# What it sets in the copies:
#   rancher-backup      appVersion and catalog.cattle.io/upstream-version, the tag
#                       of the image in values.yaml, and the bounds of the
#                       rancher-version and kube-version annotations
#   rancher-backup-crd  appVersion
#
# A bound that is already there is kept. When only the lower bound is written, the
# upper bound is added: the next minor of Rancher, and three minors after the
# lower bound of Kubernetes, which is the window of Kubernetes versions that
# Rancher supports. The chart version is not set here: the action works it out
# from the appVersion and the rancher-version annotation of the main chart.
#
# Usage: prepare-charts.sh <tag> <source dir> <output dir>
#   e.g. prepare-charts.sh v12.0.0-rc.4 release "$RUNNER_TEMP/charts"
#
# <source dir> is a checkout of the tag. This script is run from another one, so a
# tag that is older than the script can be released.
set -euo pipefail

TAG=${1:?usage: $0 <tag> <source dir> <output dir>}
SOURCE_DIR=${2:?usage: $0 <tag> <source dir> <output dir>}
OUT_DIR=${3:?usage: $0 <tag> <source dir> <output dir>}

if ! [[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
  echo "Error: tag $TAG must look like vX.Y.Z or vX.Y.Z-rc.N" >&2
  exit 1
fi
UPSTREAM_VERSION="${TAG#v}"

MAIN=rancher-backup
CRD=rancher-backup-crd
for chart in "$MAIN" "$CRD"; do
  if [ ! -f "$SOURCE_DIR/charts/$chart/Chart.yaml" ]; then
    echo "Error: there is no Chart.yaml in $SOURCE_DIR/charts/$chart" >&2
    exit 1
  fi
done

# bound_annotation <Chart.yaml> <annotation> <minors>: when the annotation is
# written as ">= X.Y.Z-0", with no upper bound, adds "< X.(Y+minors).0-0".
bound_annotation() {
  local file=$1 name=$2 minors=$3 value major minor
  value=$(sed -n "s|^ *catalog.cattle.io/${name}: *['\"]\{0,1\}\(.*[^'\"]\)['\"]\{0,1\} *\$|\1|p" "$file")
  if [[ "$value" =~ ^\>=\ *([0-9]+)\.([0-9]+)\.[0-9]+-0$ ]]; then
    major="${BASH_REMATCH[1]}"
    minor="${BASH_REMATCH[2]}"
    sed -i "s|\(catalog.cattle.io/${name}:\).*|\1 '${value} < ${major}.$((minor + minors)).0-0'|" "$file"
  fi
}

mkdir -p "$OUT_DIR"
for chart in "$MAIN" "$CRD"; do
  rm -rf "${OUT_DIR:?}/$chart"
  cp -r "$SOURCE_DIR/charts/$chart" "$OUT_DIR/$chart"
  sed -i "s|^appVersion:.*|appVersion: \"${TAG}\"|" "$OUT_DIR/$chart/Chart.yaml"
done

main_chart="$OUT_DIR/$MAIN/Chart.yaml"
sed -i "s|\(catalog.cattle.io/upstream-version:\).*|\1 ${UPSTREAM_VERSION}|" "$main_chart"
bound_annotation "$main_chart" rancher-version 1
bound_annotation "$main_chart" kube-version 3

sed -i "s|%TAG%|${TAG}|" "$OUT_DIR/$MAIN/values.yaml"
if grep -rq '%TAG%' "$OUT_DIR"; then
  echo "Error: a %TAG% placeholder is left in $OUT_DIR" >&2
  exit 1
fi

echo "Prepared $MAIN and $CRD for $TAG in $OUT_DIR" >&2
