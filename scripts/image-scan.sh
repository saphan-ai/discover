#!/bin/sh
# image-scan.sh: build the image for this machine and scan it with Docker Scout (`make scan`).
# Fails closed: it exits non-zero, naming the image, when Scout is missing, when the scan cannot run, or when it
# reports any finding of SCOUT_REFUSE_SEVERITIES, with a fix or without one. `make push` does not rely on this run:
# it scans the exact layouts it publishes.
set -eu
. "$(CDPATH='' cd -- "$(dirname "$0")" && pwd)/image-lib.sh"
need_docker
need_scout

sh "$ROOT/scripts/image.sh"
image="$DISCOVER_IMAGE:latest"
echo "scout: $image ($(docker image inspect "$image" --format '{{.Id}}'))"
if ! docker scout cves "local://$image" --only-severity "$SCOUT_REFUSE_SEVERITIES" --exit-code; then
	die "scout: refusing $image: the scan failed or reported a $SCOUT_REFUSE_SEVERITIES finding"
fi
echo "scout: ok: $image has no $SCOUT_REFUSE_SEVERITIES finding"
