---
sidebar_position: 90
---

# Moving from GitLab to GitHub

pvr now lives at <https://github.com/pantavisor/pvr>. The old home,
<https://gitlab.com/pantacor/pvr>, stays up for a while so that installed
pvr 059 and older keep working. This page says what moved, what is still on
GitLab, and how a release is cut during the move.

## What moved

| What | Before | Now |
|------|--------|-----|
| Source, issues, pull requests | gitlab.com/pantacor/pvr | github.com/pantavisor/pvr |
| Default branch | `master` | `main` |
| Go module | `gitlab.com/pantacor/pvr` | `github.com/pantavisor/pvr` |
| Release binaries | GitLab generic packages | GitHub releases, `pvr.<VERSION>.<ARCH>.<EXT>` |
| Rolling build of the default branch | package `master` | prerelease `main-snapshot` |
| Docker images | `registry.gitlab.com/pantacor/pvr` | `ghcr.io/pantavisor/pvr` |
| CI | `.gitlab-ci.yml` | `.github/workflows/` |
| Test data (100 MB) | in git | release asset `test-data/initial_state_01.tar.gz` |

The history on `main` is the GitLab `master` history without the 100 MB
test data file, so commit hashes from 2023-05 on differ from GitLab's. The tags
were rewritten the same way. The file is downloaded and checked against its
sha256 by `_test/helpers` when the tests run; do not delete the `test-data`
release.

## What stayed on GitLab

Only the default branch and the release tags were carried over. Everything
else is still on <https://gitlab.com/pantacor/pvr> and nowhere else:

- `master`, the old default branch, frozen at release 059 (`e410bc3a`). Its
  history continues as `main` on GitHub, with new commit hashes from 2023-05 on.
  The `main` branch on GitLab is a copy of GitHub `main` at release 060, made
  for the move; it is not updated anymore.
- `develop` and the other branches (75 in all), and the open merge requests.
  Each open merge request has a note asking to move it to GitHub.
- The GitLab issues (5 open).

To move a GitLab branch, cherry-pick its commits onto `main` and change the
imports to `github.com/pantavisor/pvr`:

```bash
git fetch https://gitlab.com/pantacor/pvr.git master:gitlab-master my-branch:gitlab-my-branch
git fetch https://github.com/pantavisor/pvr.git main
git checkout -b my-branch FETCH_HEAD
git cherry-pick gitlab-master..gitlab-my-branch
```

To find a GitLab commit from 2023-05 or later on GitHub, look it up by its
subject, since the hash changed:

```bash
git log main --format='%h %ad %s' --date=short | grep -F "<commit subject>"
```

## Installing and upgrading

```bash
# install from GitHub releases (default)
curl -fsSL https://raw.githubusercontent.com/pantavisor/pvr/main/install.sh | bash

# the old source, for as long as it is kept in sync
curl -fsSL https://raw.githubusercontent.com/pantavisor/pvr/main/install.sh | PVR_SOURCE=gitlab bash

# upgrade an installed pvr
sudo pvr self-upgrade --channel stable
```

`go install github.com/pantavisor/pvr@latest` does not work, because `go.mod`
has `replace` directives, which `go install` rejects. Build from a clone
instead (`go build -o pvr .`).

Channels: `stable` is the newest release (`NNN` or `NNN-revNNN`); `latest`,
`master`, `develop` and `main` are the rolling `main-snapshot`; anything else
is a release tag. On Windows use `install.ps1` with the same `PVR_*`
variables.

`pvr self-upgrade` tries, in order, and prints which one it used:

1. GitHub releases of `pantavisor/pvr` (sha256 of the download is checked).
   Set `GITHUB_TOKEN` to avoid API rate limits.
2. GitLab generic packages, the only source for versions up to 059.
3. The docker image on `ghcr.io/pantavisor/pvr`, then on
   `registry.gitlab.com/pantacor/pvr`.

## How old clients get the new release

pvr 059 and older only know GitLab. They read GitLab packages and the GitLab
registry, so 060 is also published on GitLab, once, by hand. Old clients
upgrade into it, and from then on read GitHub.

060 is the last release on GitLab. After it, nothing is published or synced to
GitLab, and the GitLab project gets archived.

## Cutting 060

060 is the first release that upgrades from GitHub. It is cut after this
change is merged and the default branch is `main`.

```bash
git checkout main && git pull
ci/release.sh 060 --dry-run     # preview the CHANGELOG section and tag message
PVR_GITLAB_PUSH=1 ci/release.sh 060 --push
```

`--push` pushes the branch and tag to GitHub. With `PVR_GITLAB_PUSH=1` (or a
remote named `gitlab`, added with
`git remote add gitlab https://gitlab.com/pantacor/pvr.git`) it also pushes
them to `gitlab.com/pantacor/pvr`, where the GitLab tag pipeline publishes the
packages. Use this only for 060.

To preview the section from another branch, run
`PVR_RELEASE_ANY_BRANCH=1 ci/release.sh 060 --dry-run`.

Then check each part:

```bash
# GitHub release and assets (the release workflow takes a few minutes)
gh run list -R pantavisor/pvr --workflow release --limit 3
gh release view 060 -R pantavisor/pvr --json assets --jq '.assets[].name'

# GitLab packages (the GitLab tag pipeline)
curl -fsSLI https://gitlab.com/api/v4/projects/pantacor%2Fpvr/packages/generic/pvr/060/pvr.060.src.tar.gz

# The src and vendor sums match on both sides (Yocto pins them)
curl -fsSL https://github.com/pantavisor/pvr/releases/download/060/pvr.060.src.tar.gz | sha256sum
curl -fsSL https://gitlab.com/api/v4/projects/pantacor%2Fpvr/packages/generic/pvr/060/pvr.060.src.tar.gz | sha256sum
```

Check the upgrade chain on a clean machine or container:

```bash
# a 059 client moves to 060 (it reads GitLab)
PVR_SOURCE=gitlab PVR_VERSION=059 sh install.sh
pvr global-config DistributionTag=stable
pvr self-upgrade        # prints that it used GitLab packages
pvr version             # 060

# a 060 client reads GitHub
sudo pvr self-upgrade --channel stable   # prints that it used GitHub releases
sudo pvr self-upgrade --channel main     # the main-snapshot build
```

Then bump pvr in meta-pantavisor: rename `recipes-pv/pvr/pvr_059.bb` to
`pvr_060.bb`, point the sources at the GitHub release URLs
(`https://github.com/pantavisor/pvr/releases/download/060/pvr.060.src.tar.gz`
and the vendor tarball), set the Go import path to `github.com/pantavisor/pvr`,
and update the sha256 sums.

## The end of the transition

The GitLab project is archived (read-only) once its open work has moved to
GitHub. After a few releases, when old clients have moved:

1. Remove the GitLab fallbacks from `libpvr/packageupdater.go` and
   `libpvr/selfupdater.go`, and the `PVR_SOURCE=gitlab` option from
   `install.sh` and `install.ps1`.
2. Archive `github.com/pantacor/pvr`, with a README that points to
   <https://github.com/pantavisor/pvr>.
