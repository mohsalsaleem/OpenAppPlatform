#!/bin/sh
# R5.3: the release journey against vultr_oap, before hand-off.
H=$HOME/.config/oap/hosts/139.180.132.23
LOGS=${LOGS:-$PWD}
cd "$(git rev-parse --show-toplevel)/cmd/oapctl" || exit 1
OAP_TEST_DOCKER=1 \
OAP_JOURNEY_SERVER=root@139.180.132.23 \
OAP_JOURNEY_SSH_KEY=$H/bootstrap_ed25519 \
OAP_JOURNEY_KNOWN_HOSTS=$H/known_hosts \
OAP_JOURNEY_PUBLIC=https://oap.139-180-132-23.sslip.io \
OAP_JOURNEY_DOMAIN=139-180-132-23.sslip.io \
OAP_JOURNEY_RUN=${OAP_JOURNEY_RUN:-r2} \
OAP_JOURNEY_OUT=$LOGS/receipt-${OAP_JOURNEY_RUN:-r2}.json \
go test -count=1 -timeout 60m -run TestReleaseJourney -v . 2>&1 | tee $LOGS/10-journey-${OAP_JOURNEY_RUN:-r2}.log
