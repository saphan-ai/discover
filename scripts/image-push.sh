#!/bin/sh
# image-push.sh: publish Discover's image (`make push`). Pushing is the owner's act.
#
# Nothing reaches a registry before every platform has been scanned, and what is scanned is what is published:
#   1. The working tree must be clean, so the image's revision label names the code it carries.
#   2. The bases must be pinned by version and digest (scripts/image-pins.sh).
#   3. The image is built once for every platform in PUSH_PLATFORMS into a local OCI layout. No build pushes.
#   4. Docker Scout scans, from that layout, the image of every platform.
#   5. Only when every scan is green, skopeo copies the layout, byte for byte with its digests kept, to
#      :latest and :<version>, and each tag is read back and must carry the scanned digest.
# Any refusal in steps 1 to 4 publishes nothing: the first registry write comes after the last green scan.
set -eu
. "$(CDPATH='' cd -- "$(dirname "$0")" && pwd)/image-lib.sh"
need_docker
need_scout
command -v skopeo >/dev/null 2>&1 \
	|| die "push: refusing to publish: skopeo is not installed, and it is what copies the scanned layout to the registry. Install it and run this again."

if [ -n "$(git -C "$ROOT" status --porcelain)" ]; then
	die "push: refusing to publish: the working tree has uncommitted changes, so the image would not be the commit it names. Commit or remove them and run this again."
fi
sh "$ROOT/scripts/image-pins.sh" || die "push: refusing to publish: a base image is not pinned (see above). Nothing was pushed."

builder=discover-builder
docker buildx inspect "$builder" >/dev/null 2>&1 || docker buildx create --name "$builder" --driver docker-container >/dev/null

oci_values
name="$DISCOVER_REGISTRY/$DISCOVER_IMAGE"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

echo "push: $name:latest and $name:$version, platforms $PUSH_PLATFORMS, revision $revision"
echo "push: building once to a local layout and scanning it; the registry is written only after every scan is green"

docker buildx build \
	--builder "$builder" \
	--file "$ROOT/Dockerfile" \
	--platform "$PUSH_PLATFORMS" \
	--provenance=mode=max \
	--sbom=true \
	--output "type=oci,dest=$work/discover.tar,name=$name:scan" \
	--build-arg "GOLANG_IMAGE=$GOLANG_IMAGE" \
	--build-arg "RUNTIME_IMAGE=$RUNTIME_IMAGE" \
	--build-arg "CREATED=$created" \
	--build-arg "REVISION=$revision" \
	--build-arg "VERSION=$version" \
	--build-arg "COMMIT=$commit" \
	"$ROOT"
mkdir "$work/layout"
tar -xf "$work/discover.tar" -C "$work/layout"
rm -f "$work/discover.tar"

digest=$(tr -d ' \n\t' <"$work/layout/index.json" | grep -o '"digest":"sha256:[0-9a-f]*"' | head -n 1 | cut -d '"' -f 4)
[ -n "$digest" ] || die "push: refusing to publish: the built layout has no index digest. Nothing was pushed."

refused=0
for platform in $(printf '%s' "$PUSH_PLATFORMS" | tr ',' ' '); do
	echo "scout: $name $platform $digest"
	if ! docker scout cves "oci-dir://$work/layout" --platform "$platform" \
		--only-severity "$SCOUT_REFUSE_SEVERITIES" --exit-code; then
		echo "scout: refusing $platform: the scan failed, the platform is missing, or a $SCOUT_REFUSE_SEVERITIES finding was reported" >&2
		refused=1
	fi
done
[ "$refused" -eq 0 ] || die "push: refusing to publish: a Docker Scout scan did not pass (the platform is named above). Nothing was pushed."

for tag in "$name:latest" "$name:$version"; do
	skopeo copy --all --preserve-digests "oci:$work/layout" "docker://$tag"
	got=$(docker buildx imagetools inspect "$tag" --format '{{json .Manifest.Digest}}' | tr -d '"')
	[ "$got" = "$digest" ] || die "push: $tag carries $got, not the scanned $digest"
	echo "push: $tag = $digest"
done
echo "push: published the scanned digest $digest"
