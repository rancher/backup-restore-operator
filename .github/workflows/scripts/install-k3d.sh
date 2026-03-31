#!/bin/bash

set -e
set -x

DEFAULT_K3D_VERSION=v5.8.3

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

install_k3d(){
  local k3dVersion=${K3D_VERSION:-${DEFAULT_K3D_VERSION}}
  local base_url="https://github.com/k3d-io/k3d/releases/download/${k3dVersion}"
  local binary="k3d-linux-${ARCH}"

  echo "Downloading k3d@${k3dVersion}"
  curl -sL --fail "${base_url}/${binary}" > k3d
  curl -sL --fail "${base_url}/sha256sum.txt" > k3d.sha256sum

  grep "${binary}" k3d.sha256sum | sed 's|k3d-linux-[^ ]*|k3d|' | sha256sum -c

  chmod +x k3d
  cp k3d /usr/local/bin/k3d
}

install_k3d

k3d version
