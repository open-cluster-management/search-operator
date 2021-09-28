#!/usr/bin/env bash

set -euxo pipefail


KEY="$SHARED_DIR/private.pem"
chmod 400 "$KEY"

IP="$(cat "$SHARED_DIR/public_ip")"
HOST="ec2-user@$IP"
OPT=(-q -o "UserKnownHostsFile=/dev/null" -o "StrictHostKeyChecking=no" -i "$KEY")


scp "${OPT[@]}" -r ../search-operator "$HOST:/tmp/search-operator"
ssh "${OPT[@]}" "$HOST" /tmp/search-operator/cicd-scripts/run-e2e-test-in-prow.sh $COMPONENT_IMAGE_REF $QUAY_TOKEN
