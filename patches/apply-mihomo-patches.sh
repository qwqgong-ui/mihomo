#!/bin/sh

set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
patch_root="$repo_root/patches/mihomo"
series_file="$patch_root/series"
listed_file=$(mktemp "${TMPDIR:-/tmp}/mihomo-series-listed.XXXXXX")
actual_file=$(mktemp "${TMPDIR:-/tmp}/mihomo-series-actual.XXXXXX")

cleanup() {
	rm -f -- "$listed_file" "$actual_file"
}
trap cleanup 0 1 2 3 15

if [ ! -f "$series_file" ]; then
	echo "missing Mihomo patch series: $series_file" >&2
	exit 1
fi

while IFS= read -r entry || [ -n "$entry" ]; do
	case "$entry" in
		''|'#'*)
			continue
			;;
		/*|..|../*|*/..|*/../*)
			echo "invalid Mihomo patch path in series: $entry" >&2
			exit 1
			;;
	esac
	case "$entry" in
		*.patch)
			;;
		*)
			echo "non-patch entry in Mihomo series: $entry" >&2
			exit 1
			;;
	esac
	if [ ! -f "$patch_root/$entry" ]; then
		echo "missing Mihomo patch listed in series: $entry" >&2
		exit 1
	fi
	printf '%s\n' "$entry" >> "$listed_file"
done < "$series_file"

if [ ! -s "$listed_file" ]; then
	echo "Mihomo patch series is empty: $series_file" >&2
	exit 1
fi

duplicate=$(LC_ALL=C sort "$listed_file" | uniq -d | head -n 1)
if [ -n "$duplicate" ]; then
	echo "duplicate Mihomo patch in series: $duplicate" >&2
	exit 1
fi

LC_ALL=C sort "$listed_file" -o "$listed_file"
(
	cd "$patch_root"
	find . -type f -name '*.patch' -print | sed 's#^\./##' | LC_ALL=C sort
) > "$actual_file"

if ! cmp -s "$listed_file" "$actual_file"; then
	echo "Mihomo series does not list every patch exactly once" >&2
	diff -u "$listed_file" "$actual_file" >&2 || true
	exit 1
fi

while IFS= read -r entry || [ -n "$entry" ]; do
	case "$entry" in
		''|'#'*)
			continue
			;;
	esac
	patch="$patch_root/$entry"
	git -C "$repo_root" apply --check --whitespace=error-all "$patch"
	git -C "$repo_root" apply --whitespace=error-all "$patch"
done < "$series_file"

git -C "$repo_root" diff --check
