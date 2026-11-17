#!/bin/bash
# Cut a pvr release: changelog commit, annotated tag, optional push.
# Usage: ci/release.sh NNN [--push] [--dry-run]
#
# Releases are cut from main (main-draft until the rename). The branch and tag
# go to origin (GitHub). With --push they also go to gitlab.com/pantacor/pvr
# when a remote named `gitlab` exists or PVR_GITLAB_PUSH=1, so the GitLab
# package registry that pvr 059 and older read gets the release too.
# For a dry run on another branch (to preview the changelog), set
# PVR_RELEASE_ANY_BRANCH=1; the branch and origin checks are skipped.
set -euo pipefail

GITLAB_URL=https://gitlab.com/pantacor/pvr.git

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

branch=$(git rev-parse --abbrev-ref HEAD)
if [ "$dry" = 1 ] && [ "${PVR_RELEASE_ANY_BRANCH:-0}" = 1 ]; then
	echo "note: PVR_RELEASE_ANY_BRANCH=1, skipping the branch and origin checks (on $branch)"
else
	case "$branch" in
	main | main-draft) ;;
	*) die "not on main (or main-draft)" ;;
	esac
	[ -z "$(git status --porcelain)" ] || die "work tree is not clean"
	git fetch -q origin "$branch"
	[ "$(git rev-parse HEAD)" = "$(git rev-parse "origin/$branch")" ] || die "$branch differs from origin/$branch"
fi
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
	echo "  git push origin $branch"
	echo "  git push origin $version"
	echo "  git push $GITLAB_URL $branch:main $version   # GitLab bridge"
	exit 0
fi

git push origin "$branch"
git push origin "$version"

# GitLab bridge: the old packages and registry stay in sync for pvr <= 059.
if git remote get-url gitlab >/dev/null 2>&1; then
	gitlab=gitlab
elif [ "${PVR_GITLAB_PUSH:-0}" = 1 ]; then
	gitlab=$GITLAB_URL
else
	gitlab=
	echo "note: no gitlab remote and PVR_GITLAB_PUSH is not 1, so nothing was pushed to GitLab"
fi
if [ -n "$gitlab" ]; then
	git push "$gitlab" "HEAD:refs/heads/main"
	git push "$gitlab" "$version"
fi

cat <<EOT
Pushed $version. Wait for the release workflow (GitHub) and the tag pipeline (GitLab), then check:
  gh release view $version -R pantavisor/pvr --json assets --jq '.assets[].name'
  curl -fsSLI https://github.com/pantavisor/pvr/releases/download/$version/pvr.$version.src.tar.gz
  curl -fsSLI https://github.com/pantavisor/pvr/releases/download/$version/pvr.$version.vendor.tar.gz
  curl -fsSLI https://gitlab.com/api/v4/projects/pantacor%2Fpvr/packages/generic/pvr/$version/pvr.$version.src.tar.gz
  curl -fsSLI https://gitlab.com/api/v4/projects/pantacor%2Fpvr/packages/generic/pvr/$version/pvr.$version.vendor.tar.gz
Then bump pvr in meta-pantavisor: rename recipes-pv/pvr/pvr_$prev.bb to pvr_$version.bb, update the sha256 sums.
EOT
