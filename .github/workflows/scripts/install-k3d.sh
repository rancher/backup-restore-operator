#!/bin/bash

set -e
set -x

K3D_URL=https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh
DEFAULT_K3D_VERSION=v5.4.6

install_k3d(){
  local k3dVersion=${K3D_VERSION:-${DEFAULT_K3D_VERSION}}
  echo -e "Downloading k3d@${k3dVersion} see: ${K3D_URL}"
  curl --silent --fail ${K3D_URL} | TAG=${k3dVersion} bash
}

install_k3d

k3d version