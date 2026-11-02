#!/bin/sh
# image-pins.sh: refuse a base image that is not pinned by version AND digest.
#
# usage: sh scripts/image-pins.sh [DOCKERFILE]   (default: the Dockerfile at the repository root)
#
# Checks, each against <name>:<version>@sha256:<64 hex>:
#   - GOLANG_IMAGE and RUNTIME_IMAGE as the build will use them (the environment, else versions.env);
#   - every `ARG *_IMAGE=` default in the Dockerfile, which must also equal the value in versions.env;
#   - every FROM line that names an image directly instead of one of those settings or an earlier stage.
# A refusal line names the setting or the Dockerfile line, and what is missing. Exit codes: 0 pinned, 2 refused.
set -u
. "$(CDPATH='' cd -- "$(dirname "$0")" && pwd)/image-lib.sh"

dockerfile=${1:-$ROOT/Dockerfile}
[ -f "$dockerfile" ] || die "image-pins: no Dockerfile at $dockerfile"

PINNED_RE='^[a-z0-9][a-z0-9._/-]*:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$'
refused=0

refuse() {
	echo "image-pins: refused: $1" >&2
	refused=1
}

pinned() {
	printf '%s\n' "$1" | grep -Eq "$PINNED_RE"
}

for key in GOLANG_IMAGE RUNTIME_IMAGE; do
	eval "val=\${$key:-}"
	if [ -z "$val" ]; then
		refuse "$key is empty"
	elif ! pinned "$val"; then
		refuse "$key=$val is not pinned by version and digest (<name>:<version>@sha256:<digest>)"
	fi
done

n=0
stages=''
while IFS= read -r line || [ -n "$line" ]; do
	n=$((n + 1))
	case $line in
	ARG\ *_IMAGE=*)
		decl=${line#ARG }
		key=${decl%%=*}
		val=${decl#*=}
		if ! pinned "$val"; then
			refuse "$(basename "$dockerfile"):$n: the default of $key is not pinned by version and digest"
		elif [ "$val" != "$(file_value "$key")" ]; then
			refuse "$(basename "$dockerfile"):$n: the default of $key differs from versions.env; change both together"
		fi
		;;
	FROM\ * | from\ *)
		# FROM [--platform=...] <ref> [AS <stage>]
		set -- $line
		shift
		case ${1:-} in --*) shift ;; esac
		ref=${1:-}
		stage=''
		if [ "$#" -ge 3 ]; then stage=$3; fi
		case $ref in
		'${GOLANG_IMAGE}' | '${RUNTIME_IMAGE}' | '$GOLANG_IMAGE' | '$RUNTIME_IMAGE') ;;
		*)
			case " $stages " in
			*" $ref "*) ;;
			*) pinned "$ref" || refuse "$(basename "$dockerfile"):$n: FROM $ref is not pinned by version and digest" ;;
			esac
			;;
		esac
		[ -n "$stage" ] && stages="$stages $stage"
		;;
	esac
done <"$dockerfile"

if [ "$refused" -ne 0 ]; then
	echo "image-pins: refusing the build: every base image must be pinned by version and digest (versions.env says how)." >&2
	exit 2
fi
echo "image-pins: ok: GOLANG_IMAGE and RUNTIME_IMAGE pinned by version and digest; the Dockerfile agrees with versions.env"
