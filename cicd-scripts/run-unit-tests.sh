#!/bin/bash
set -e

echo "> Running build/run-unit-tests.sh"
GO111MODULE=on go get github.com/golangci/golangci-lint/cmd/golangci-lint@v1.33.0
#go get -u github.com/golang/dep/cmd/dep
#dep ensure -v
make unit-tests