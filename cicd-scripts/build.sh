#!/bin/bash
set -e

echo "> Running build/build.sh"

echo "<repo>/<component>:<tag> : $1"

operator-sdk build $1