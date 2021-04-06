# Copyright (c) 2021 Red Hat, Inc.
# Copyright Contributors to the Open Cluster Management project
FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

RUN microdnf update &&\
    microdnf install ca-certificates vi --nodocs &&\
    microdnf clean all

ENV OPERATOR=/usr/local/bin/search-operator \
    USER_UID=1001 \
    USER_NAME=search-operator

# install operator binary
COPY build/_output/bin/search-operator ${OPERATOR}

COPY build/bin /usr/local/bin
RUN  /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
