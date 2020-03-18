#!/bin/bash
set -e

echo "> Running build/run-unit-tests.sh"
go get -u github.com/golangci/golangci-lint/cmd/golangci-lint
go get -u github.com/golang/dep/cmd/dep
dep ensure -v
make unit-tests