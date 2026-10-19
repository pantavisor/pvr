#!/bin/bash
# Cut a pvr release: changelog commit, annotated tag, optional push.
# Usage: ci/release.sh NNN [--push] [--dry-run]
set -euo pipefail

CHGLOG=${CHGLOG:-"go run github.com/git-chglog/git-chglog/cmd/git-chglog@v0.15.4"}

usage() {
	echo "usage: $0 NNN [--push] [--dry-run]" >&2
	exit 2
}

die() {
	echo "error: $*" >&2
	exit 1
}

version=""
push=0
dry=0
for arg in "$@"; do
	case "$arg" in
	--push) push=1 ;;
	--dry-run) dry=1 ;;
	-*) usage ;;
	*) [ -z "$version" ] || usage; version=$arg ;;
	esac
done
[ -n "$version" ] || usage
[[ "$version" =~ ^[0-9]{3}$ ]] || die "version must be three digits, got '$version'"

cd "$(git rev-parse --show-toplevel)"

[ "$(git rev-parse --abbrev-ref HEAD)" = master ] || die "not on master"
[ -z "$(git status --porcelain)" ] || die "work tree is not clean"
git fetch -q origin master
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/master)" ] || die "master differs from origin/master"
! git rev-parse -q --verify "refs/tags/$version" >/dev/null || die "tag $version exists locally"
[ -z "$(git ls-remote --tags origin "refs/tags/$version")" ] || die "tag $version exists on origin"

# Previous release is the highest plain three-digit tag.
prev=$(git tag -l '[0-9][0-9][0-9]' | sort | tail -1)
[ -n "$prev" ] || die "no previous release tag found"

[ "$(git rev-list --count "$prev..HEAD")" -gt 0 ] || die "no commits since $prev"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Only the new section is generated, so older sections are never rewritten.
section=$($CHGLOG --next-tag "$version" "$prev.." | awk '/^<a name=/ {n++} n == 1' | sed '/./,$!d')
printf '%s\n' "$section" >"$tmp/section.md"
grep -q "^## \[$version\]" "$tmp/section.md" || die "changelog generation failed"

# Tag message: version, blank line, then the section without its anchor and heading.
{
	echo "$version"
	echo
	sed '1,3d' "$tmp/section.md"
} >"$tmp/tag.msg"

echo "== release $version (previous $prev) =="
cat "$tmp/section.md"
echo "== tag message =="
cat "$tmp/tag.msg"

if [ "$dry" = 1 ]; then
	echo "dry run: nothing committed, tagged or pushed"
	exit 0
fi

# The file starts with a blank line and sections are two blank lines apart.
{ echo; cat "$tmp/section.md"; echo; cat CHANGELOG.md; } >"$tmp/CHANGELOG.md"
cp "$tmp/CHANGELOG.md" CHANGELOG.md
git add CHANGELOG.md
git commit -q -m "chore: update CHANGELOG.md for release $version"
# verbatim keeps the "###" lines, which git would otherwise strip as comments
git tag -a --cleanup=verbatim "$version" -F "$tmp/tag.msg"

if [ "$push" = 0 ]; then
	echo "Created commit and tag $version. To publish:"
	echo "  git push origin master"
	echo "  git push origin $version"
	exit 0
fi

git push origin master
git push origin "$version"

cat <<EOF
Pushed $version. Wait for the tag pipeline, then check the packages:
  curl -fsSLI https://gitlab.com/api/v4/projects/pantacor%2Fpvr/packages/generic/pvr/$version/pvr.$version.src.tar.gz
  curl -fsSLI https://gitlab.com/api/v4/projects/pantacor%2Fpvr/packages/generic/pvr/$version/pvr.$version.vendor.tar.gz
Then bump pvr in meta-pantavisor (rename recipes-pv/pvr/pvr_$prev.bb to pvr_$version.bb, update the sha256 sums).
EOF
