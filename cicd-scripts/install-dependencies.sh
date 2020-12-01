#!/bin/bash
set -e

echo "> Running build/install-dependencies.sh"

_OPERATOR_SDK_VERSION=v1.2.0

echo "Installing operator-sdk..."

if ! [ -x "$(command -v operator-sdk)" ]; then
    if [[ "$OSTYPE" == "linux-gnu" ]]; then
            curl -L https://github.com/operator-framework/operator-sdk/releases/download/${_OPERATOR_SDK_VERSION}/operator-sdk-${_OPERATOR_SDK_VERSION}-x86_64-linux-gnu -o operator-sdk
    elif [[ "$OSTYPE" == "darwin"* ]]; then
            curl -L https://github.com/operator-framework/operator-sdk/releases/download/${_OPERATOR_SDK_VERSION}/operator-sdk-${_OPERATOR_SDK_VERSION}-x86_64-apple-darwin -o operator-sdk
    fi
    chmod +x operator-sdk
    sudo mv operator-sdk /usr/local/bin/operator-sdk
fi