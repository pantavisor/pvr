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
| Default branch | `master` | `main` (`main-draft` until CI is proven) |
| Go module | `gitlab.com/pantacor/pvr` | `github.com/pantavisor/pvr` |
| Release binaries | GitLab generic packages | GitHub releases, `pvr.<VERSION>.<ARCH>.<EXT>` |
| Rolling build of the default branch | package `master` | prerelease `main-snapshot` |
| Docker images | `registry.gitlab.com/pantacor/pvr` | `ghcr.io/pantavisor/pvr` |
| CI | `.gitlab-ci.yml` | `.github/workflows/` |
| Test data (100 MB) | in git | release asset `test-data/initial_state_01.tar.gz` |

The history on `main-draft` is the GitLab `master` history without the 100 MB
test data file, so commit hashes from 2023-05 on differ from GitLab's. The tags
were rewritten the same way. The file is downloaded and checked against its
sha256 by `_test/helpers` when the tests run; do not delete the `test-data`
release.

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
registry, so at least one release with the GitHub-aware self-upgrade (060) must
be published on GitLab too. Old clients upgrade into it, and from then on read
GitHub.

Two things keep GitLab fed:

- The `sync-gitlab` workflow pushes `main` and every tag to
  `gitlab.com/pantacor/pvr` (branch `main`, never `master`, because the
  histories differ). It runs only when the repository secret
  `GITLAB_PUSH_TOKEN` is set; without it the job does nothing. Add it under
  Settings, Secrets and variables, Actions, with a GitLab token that has
  `write_repository`.
- `.gitlab-ci.yml` builds `main` like it used to build `master` and publishes
  the packages and images under `master` too, so the `latest` channel of old
  clients keeps working. Tag jobs are unchanged.

## Cutting 060

060 is the first release that upgrades from GitHub. It is cut after this
change is merged and the default branch is `main` (or `main-draft`).

```bash
git checkout main && git pull
ci/release.sh 060 --dry-run     # preview the CHANGELOG section and tag message
ci/release.sh 060 --push
```

`--push` pushes the branch and tag to GitHub. It also pushes them to
`gitlab.com/pantacor/pvr` when a remote named `gitlab` exists
(`git remote add gitlab https://gitlab.com/pantacor/pvr.git`) or when
`PVR_GITLAB_PUSH=1` is set. Do this even if `GITLAB_PUSH_TOKEN` is not set up,
so 060 reaches GitLab.

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

After a few releases have gone out and old clients have moved:

1. Stop the sync: delete the `GITLAB_PUSH_TOKEN` secret and the
   `sync-gitlab` workflow.
2. Remove the GitLab fallbacks from `libpvr/packageupdater.go` and
   `libpvr/selfupdater.go`, and the `PVR_SOURCE=gitlab` option from
   `install.sh` and `install.ps1`.
3. Archive the GitLab project and `github.com/pantacor/pvr`, with a README
   that points to <https://github.com/pantavisor/pvr>.
