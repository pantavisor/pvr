#!/bin/bash
# Package release assets named pvr.<VERSION>.<ARCH>.<EXT>.
# Usage:
#   ci/package-assets.sh src <version> <outdir> <source-dir>
#   ci/package-assets.sh default <version> <outdir> <docker-image>
#   ci/package-assets.sh go1.21 <version> <outdir> <docker-image>
set -euo pipefail

[ "$#" -eq 4 ] || { sed -n '2,6p' "$0" >&2; exit 2; }
variant=$1
version=$2
outdir=$(mkdir -p "$3" && cd "$3" && pwd)
input=$4

# Same bytes for the same input on any host: Yocto recipes pin these sha256 sums.
# Default mtime is 2016-11-11 UTC (Pantacor Ltd's incorporation date).
repro_tar() { LC_ALL=C tar --sort=name --format=gnu --owner=0 --group=0 --numeric-owner --mtime="@${SOURCE_DATE_EPOCH:-1478822400}" "$@"; }

if [ "$variant" = src ]; then
	root="pvr-$version"
	cd "$input"
	# --sort=name does not order command-line arguments, so feed a sorted list.
	{ printf '%s\n' go.mod go.sum Makefile; find . -name '*.go' -type f -not -path './vendor/*' -printf '%P\n'; } |
		LC_ALL=C sort -u |
		repro_tar -czf "$outdir/pvr.$version.src.tar.gz" -C . --transform "s,^,$root/," -T -
	exit 0
fi

case "$variant" in
default) infix=""; vendor="vendor" ;;
go1.21) infix="go1.21."; vendor="vendor-go1.21" ;;
*) echo "unknown variant: $variant" >&2; exit 2 ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
docker run --rm "$input" | tar -C "$tmp" -x

for t in linux.amd64 linux.arm linux.arm64 darwin.amd64 darwin.arm64; do
	repro_tar -czf "$outdir/pvr.$version.$infix$t.tar.gz" -C "$tmp/bin/${t%.*}_${t#*.}" pvr
done
for w in x32:windows_386 x64:windows_amd64; do
	(cd "$tmp/bin/${w#*:}" && zip -q "$outdir/pvr.$version.${infix}windows.${w%%:*}.zip" pvr.exe)
done
cp "$tmp/pvr-vendor.tar.gz" "$outdir/pvr.$version.$vendor.tar.gz"
