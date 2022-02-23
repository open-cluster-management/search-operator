# Copyright (c) 2021 Red Hat, Inc.
# Copyright Contributors to the Open Cluster Management project
# Build the manager binary
FROM registry.ci.openshift.org/stolostron/builder:go1.17-linux AS builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY addon/ addon/

# Build
RUN CGO_ENABLED=0 go build -a -o manager main.go

FROM registry.access.redhat.com/ubi8/ubi-minimal:8.3

RUN microdnf update &&\
    microdnf install ca-certificates vi --nodocs &&\
    microdnf clean all

WORKDIR /
COPY --from=builder /workspace/manager .
ENV OPERATOR=/usr/local/bin/search-operator \
    USER_UID=1001 \
    USER_NAME=search-operator \
    DEPLOY_REDISGRAPH="false" 

USER ${USER_UID}
ENTRYPOINT ["/manager"]
