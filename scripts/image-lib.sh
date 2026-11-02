# Shared by the image scripts (image.sh, image-check.sh, image-scan.sh, image-push.sh, image-pins.sh).
# Sourced, never run. ROOT is the repository root; the settings come from versions.env.

if [ -z "${ROOT:-}" ]; then
	ROOT=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
fi
export ROOT

die() {
	echo "$1" >&2
	exit 2
}

# file_value KEY: the value versions.env gives KEY, ignoring the environment.
file_value() {
	sed -n "s/^$1=//p" "$ROOT/versions.env" | tail -n 1
}

# load_versions: every KEY=value of versions.env becomes a variable, unless the environment already sets it.
load_versions() {
	[ -f "$ROOT/versions.env" ] || die "image: versions.env is missing at the repository root"
	while IFS= read -r line || [ -n "$line" ]; do
		case $line in '' | \#*) continue ;; esac
		key=${line%%=*}
		val=${line#*=}
		case $key in *[!A-Za-z0-9_]* | '') die "image: versions.env holds a line that is not KEY=value" ;; esac
		eval "isset=\${$key+x}"
		if [ -z "$isset" ]; then
			export "$key=$val"
		fi
	done <"$ROOT/versions.env"
}

need_docker() {
	command -v docker >/dev/null 2>&1 || die "image: docker is not on PATH. Install Docker (or put its CLI on PATH) and run this again."
	docker buildx version >/dev/null 2>&1 || die "image: docker buildx is not available. Install the buildx plugin and run this again."
}

need_scout() {
	docker scout version >/dev/null 2>&1 \
		|| die "image: refusing: the Docker Scout plugin is not installed, so nothing can be scanned. Install it and run this again; nothing is published without a scan."
}

oci_created() { date -u +%Y-%m-%dT%H:%M:%SZ; }
oci_revision() { git -C "$ROOT" rev-parse HEAD; }
oci_commit() { git -C "$ROOT" rev-parse --short HEAD; }
# The same version string as `make build` gives the binary.
oci_version() { git -C "$ROOT" describe --tags --always --dirty; }

# oci_values: sets created, revision, version and commit, the values every build of the image passes.
oci_values() {
	created=$(oci_created)
	revision=$(oci_revision)
	version=$(oci_version)
	commit=$(oci_commit)
}

load_versions
