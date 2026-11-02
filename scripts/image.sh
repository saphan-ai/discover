#!/bin/sh
# image.sh: build Discover's image for this machine's platform and load it into the local engine (`make image`).
# One platform only: a local engine cannot load a multi-platform index. `make push` builds both platforms.
set -eu
. "$(CDPATH='' cd -- "$(dirname "$0")" && pwd)/image-lib.sh"
need_docker
sh "$ROOT/scripts/image-pins.sh"

oci_values
platform=$(docker version --format '{{.Server.Os}}/{{.Server.Arch}}')
echo "image: $DISCOVER_IMAGE:latest and $DISCOVER_IMAGE:$version for $platform"

docker buildx build \
	--file "$ROOT/Dockerfile" \
	--load \
	--platform "$platform" \
	--build-arg "GOLANG_IMAGE=$GOLANG_IMAGE" \
	--build-arg "RUNTIME_IMAGE=$RUNTIME_IMAGE" \
	--build-arg "CREATED=$created" \
	--build-arg "REVISION=$revision" \
	--build-arg "VERSION=$version" \
	--build-arg "COMMIT=$commit" \
	--tag "$DISCOVER_IMAGE:latest" \
	--tag "$DISCOVER_IMAGE:$version" \
	"$ROOT"
