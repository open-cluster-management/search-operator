#!/bin/bash

KEY="$SHARED_DIR/private.pem"
chmod 400 "$KEY"

IP="$(cat "$SHARED_DIR/public_ip")"
HOST="ec2-user@$IP"
OPT=(-q -o "UserKnownHostsFile=/dev/null" -o "StrictHostKeyChecking=no" -i "$KEY")

scp "${OPT[@]}" cicd-scripts/run-e2e-tests.sh "$HOST:/tmp/test.sh"
ssh "${OPT[@]}" "$HOST" /tmp/test.sh $COMPONENT_IMAGE_REF > >(tee "$ARTIFACT_DIR/test.log") 2>&1
