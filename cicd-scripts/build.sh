#!/bin/bash
# Copyright (c) 2021 Red Hat, Inc.
# Copyright Contributors to the Open Cluster Management project
set -e

echo "> Running build/build.sh"

echo "<repo>/<component>:<tag> : $1"

docker build . -t $1