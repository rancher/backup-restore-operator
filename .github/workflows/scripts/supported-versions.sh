#!/bin/bash
# Prints supported versions based on the current release branch targeted
# Version output is in JSON

set -e
set -x

if git merge-base --is-ancestor origin/release/v5.0 HEAD
then
  echo -n "[\"v1.23.9-k3s1\", \"v1.29.3-k3s1\"]"
  exit 0
elif git merge-base --is-ancestor origin/release/v4.0 HEAD
then
  echo -n "[\"v1.23.9-k3s1\", \"v1.28.8-k3s1\"]"
  exit 0
elif git merge-base --is-ancestor origin/release/v3.0 HEAD
then
  echo -n "[\"v1.16.9-k3s1\", \"v1.27.9-k3s1\"]"
  exit 0
fi


exit 1