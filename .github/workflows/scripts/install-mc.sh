#!/bin/bash

set -e
set -x


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

BASE_URL="https://dl.min.io/client/mc/release/linux-${ARCH}"

curl -sL --fail "${BASE_URL}/mc" > mc
curl -sL --fail "${BASE_URL}/mc.sha256sum" > mc.sha256sum

sha256sum -c mc.sha256sum

chmod +x mc
cp mc /usr/local/bin/mc

mc --version