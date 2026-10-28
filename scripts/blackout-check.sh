#!/bin/sh
# blackout-check.sh: refuse a change that carries what must stay out of this repository.
#
# usage: sh scripts/blackout-check.sh [--commits <range>]
#
# Checks every file `git ls-files` lists and, with --commits, the message of every commit in the range.
# If DISCOVER_BLACKOUT_LIST names a file, each line of it is an extended regular expression
# (`#` starts a comment, blank lines are ignored). That file is private and never enters this repository.
#
# A refusal line is `<file>:<line>: <category>` or `commit <sha>: <category>`. It never prints the
# matched text. Exit codes: 0 clean, 2 refusal or usage error.

set -u

# The patterns are written so that this file does not match them.
TRAILER_RE='^[[:space:]]*(co-authored-by|assisted-by)[[:space:]]*:'
GENERATED_RE='generated with[[:space:]]+(\[|https?://)'
HOME_RE='/(Users|home)/[A-Za-z0-9._-]+'

die() {
	echo "blackout: $1" >&2
	exit 2
}

range=''
while [ "$#" -gt 0 ]; do
	case "$1" in
	--commits)
		[ "$#" -ge 2 ] || die "usage: --commits needs a range"
		range=$2
		shift 2
		;;
	*)
		die "usage: sh scripts/blackout-check.sh [--commits <range>]"
		;;
	esac
done

root=$(git rev-parse --show-toplevel 2>/dev/null) || die "not inside a git repository"
cd "$root" || die "cannot enter the repository root"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/blackout.XXXXXX") || die "cannot create a temporary directory"
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

# Private patterns: comments and blank lines removed. Set but unreadable is a refusal, never a pass.
private="$tmp/private"
: >"$private"
if [ "${DISCOVER_BLACKOUT_LIST+set}" = set ]; then
	if [ ! -f "$DISCOVER_BLACKOUT_LIST" ] || [ ! -r "$DISCOVER_BLACKOUT_LIST" ]; then
		die "blackout list not found"
	fi
	sed -e 's/#.*//' -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' "$DISCOVER_BLACKOUT_LIST" |
		grep -v '^$' >"$private"
	# An invalid pattern must refuse, not pass quietly.
	printf '' | grep -E -f "$private" >/dev/null 2>&1
	if [ "$?" -ge 2 ]; then
		die "blackout list holds an invalid pattern"
	fi
fi

found=0

check_file() {
	f=$1
	hits=$(grep -n -I -i -E -e "$TRAILER_RE" -- "$f" | cut -d: -f1)
	for n in $hits; do
		echo "$f:$n: attribution-trailer" >&2
		found=1
	done
	hits=$(grep -n -I -i -E -e "$GENERATED_RE" -- "$f" | cut -d: -f1)
	for n in $hits; do
		echo "$f:$n: attribution-generated" >&2
		found=1
	done
	hits=$(grep -n -I -E -e "$HOME_RE" -- "$f" | cut -d: -f1)
	for n in $hits; do
		echo "$f:$n: home-path" >&2
		found=1
	done
	if [ -s "$private" ]; then
		hits=$(grep -n -I -E -f "$private" -- "$f" | cut -d: -f1)
		for n in $hits; do
			echo "$f:$n: private-pattern" >&2
			found=1
		done
	fi
}

count=0
git ls-files >"$tmp/files" || die "git ls-files failed"
while IFS= read -r f; do
	[ -f "$f" ] || continue
	count=$((count + 1))
	check_file "$f"
done <"$tmp/files"

check_message() {
	sha=$1
	msg="$tmp/msg"
	git log -1 --format=%B "$sha" >"$msg" || die "cannot read commit $sha"
	if grep -q -i -E -e "$TRAILER_RE" "$msg"; then
		echo "commit $sha: attribution-trailer" >&2
		found=1
	fi
	if grep -q -i -E -e "$GENERATED_RE" "$msg"; then
		echo "commit $sha: attribution-generated" >&2
		found=1
	fi
	if grep -q -E -e "$HOME_RE" "$msg"; then
		echo "commit $sha: home-path" >&2
		found=1
	fi
	if [ -s "$private" ] && grep -q -E -f "$private" "$msg"; then
		echo "commit $sha: private-pattern" >&2
		found=1
	fi
}

if [ -n "$range" ]; then
	git rev-list "$range" >"$tmp/commits" 2>/dev/null || die "cannot read the commit range"
	while IFS= read -r sha; do
		check_message "$sha"
	done <"$tmp/commits"
fi

if [ "$found" -ne 0 ]; then
	exit 2
fi
echo "blackout: clean ($count files)"
