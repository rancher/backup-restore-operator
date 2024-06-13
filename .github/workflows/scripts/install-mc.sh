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

curl -sL --fail https://dl.min.io/client/mc/release/linux-${ARCH}/mc > mc;
chmod +x mc;

cp mc /usr/local/bin/mc

mc --version