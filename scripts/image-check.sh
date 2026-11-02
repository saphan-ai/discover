#!/bin/sh
# image-check.sh: prove the image runs the binary it claims to carry (`make image-check`).
#
# usage: sh scripts/image-check.sh [IMAGE [BINARY]]   (defaults: $DISCOVER_IMAGE:latest and ./bin/discover)
#
# Compares the image against a binary built from the same commit: the same `discover version` line, and the same
# output of the README's "Try it first" scan, run on a fresh clone of this checkout (a git target) and on a small
# plain folder. The container is started the way the README starts it: no network, the target mounted read-only,
# the results in a mounted folder, as the calling user. Both runs must succeed: two runs failing the same way print
# the same thing, and comparing them alone would call a broken image good. Exits non-zero naming the first difference.
set -eu
. "$(CDPATH='' cd -- "$(dirname "$0")" && pwd)/image-lib.sh"
need_docker

image=${1:-$DISCOVER_IMAGE:latest}
binary=${2:-$ROOT/bin/discover}
case $binary in /*) ;; *) binary=$(CDPATH='' cd -- "$(dirname "$binary")" && pwd)/$(basename "$binary") ;; esac
[ -x "$binary" ] || die "image-check: no binary at $binary; run make build first"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() {
	echo "image-check: FAIL: $1" >&2
	shift
	for f in "$@"; do
		echo "--- $f" >&2
		cat "$f" >&2
	done
	exit 1
}

# in_image TARGET OUT ARGS...: discover in the container, as the README runs it: the target mounted read-only
# at /<its name>, the results folder at /out.
in_image() {
	target=$1
	out=$2
	shift 2
	docker run --rm --pull=never --network none --read-only \
		--user "$(id -u):$(id -g)" \
		-v "$target:/$(basename "$target"):ro" \
		-v "$out:/out" \
		"$image" "$@"
}

"$binary" version >"$work/version.bin" 2>&1 || fail "the binary's version exited $?" "$work/version.bin"
docker run --rm --pull=never --network none "$image" version >"$work/version.img" 2>&1 \
	|| fail "the image's version exited $?" "$work/version.img"
cmp -s "$work/version.bin" "$work/version.img" || fail "the version lines differ" "$work/version.bin" "$work/version.img"

# A git target: a fresh clone of this checkout's HEAD, named discover as in the README.
mkdir -p "$work/targets" "$work/plain"
git clone -q "$ROOT" "$work/targets/discover"
# A plain folder: no git, one regular and one executable file.
mkdir -p "$work/plain/sample"
printf 'hello\n' >"$work/plain/sample/a.txt"
printf '#!/bin/sh\n' >"$work/plain/sample/run.sh"
chmod 755 "$work/plain/sample/run.sh"

for target in "$work/targets/discover" "$work/plain/sample"; do
	name=$(basename "$target")
	mkdir -p "$work/out.bin.$name" "$work/out.img.$name"
	(cd "$(dirname "$target")" && "$binary" scan --out "$work/out.bin.$name" "$name") >"$work/scan.bin.$name" 2>&1 \
		|| fail "the binary's scan of $name exited $?" "$work/scan.bin.$name"
	in_image "$target" "$work/out.img.$name" scan --out /out "/$name" >"$work/scan.img.$name" 2>&1 \
		|| fail "the image's scan of $name exited $?" "$work/scan.img.$name"
	cmp -s "$work/scan.bin.$name" "$work/scan.img.$name" \
		|| fail "the scans of $name differ" "$work/scan.bin.$name" "$work/scan.img.$name"
	ls "$work/out.bin.$name" >"$work/files.bin.$name"
	ls "$work/out.img.$name" >"$work/files.img.$name"
	cmp -s "$work/files.bin.$name" "$work/files.img.$name" \
		|| fail "the results folders of $name hold different files" "$work/files.bin.$name" "$work/files.img.$name"
	echo "image-check: scan of $name: same output from the image and the binary"
	cat "$work/scan.img.$name"
done

label=$(docker image inspect "$image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')
head=$(git -C "$ROOT" rev-parse HEAD)
[ "$label" = "$head" ] || fail "the image was built from $label, this checkout is $head"
user=$(docker image inspect "$image" --format '{{.Config.User}}')
case $user in '' | 0 | 0:* | root | root:*) fail "the image runs as root (User=\"$user\")" ;; esac

echo "image-check: ok: $image and $binary print the same version and the same scans"
echo "image-check: version $(cat "$work/version.img")"
echo "image-check: revision $head, user $user"
echo "image-check: size $(docker image inspect "$image" --format '{{.Size}}') bytes"
