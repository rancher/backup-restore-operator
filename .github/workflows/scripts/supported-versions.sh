#!/bin/bash
# Prints supported versions based on the current release branch targeted
# Version output is in JSON

set -e
set -x

if git merge-base --is-ancestor origin/main HEAD
then
  printf '["%s", "%s"]' v1.30.9-k3s1 v1.32.1-k3s1
  exit 0
elif git merge-base --is-ancestor origin/release/v6.x HEAD
then
  printf '["%s", "%s"]' v1.28.15-k3s1 v1.31.5-k3s1
  exit 0
elif git merge-base --is-ancestor origin/release/v5.0 HEAD
then
  printf '["%s", "%s"]' v1.27.16-k3s1 v1.30.9-k3s1
  exit 0
elif git merge-base --is-ancestor origin/release/v4.0 HEAD
then
  printf '["%s", "%s"]' v1.26.15-k3s1 v1.28.15-k3s1
  exit 0
fi


exit 1
