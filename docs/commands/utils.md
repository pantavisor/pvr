---
sidebar_position: 10
---

# Utility Commands

---

## pvr version

Print the PVR version, Go version, and source revision.

```bash
pvr version
```

**Flags:** none

---

## pvr completion

Generate and install bash completion.

```bash
pvr completion
```

Prints the bash completion script. Redirect to a file or source directly:

```bash
pvr completion > /etc/bash_completion.d/pvr
source /etc/bash_completion.d/pvr
```

**Flags:** none

---

## pvr self-upgrade

Update the PVR binary to the latest version.

```bash
pvr self-upgrade
```

Requires root/sudo to write to the installation path. Downloads the release
asset from [GitHub releases](https://github.com/pantavisor/pvr/releases),
verifies its sha256 and installs it. If GitHub is unreachable, or the version
only exists on GitLab (up to 059), it falls back to the GitLab package
registry, then to the docker images on `ghcr.io/pantavisor/pvr` and
`registry.gitlab.com/pantacor/pvr`. The message says which source was used.

```bash
sudo pvr self-upgrade --channel stable      # newest release
sudo pvr self-upgrade --channel main        # rolling main-snapshot build
sudo pvr self-upgrade --channel 059         # a specific version
```

`stable` is the newest release tag (`NNN` or `NNN-revNNN`); `latest`, `master`,
`develop` and `main` are the rolling `main-snapshot` prerelease; anything else
is taken as a release tag. Set `GITHUB_TOKEN` to avoid GitHub API rate limits.

**Flags:**

| Flag | Description |
|------|-------------|
| `--channel` | Distribution channel or version to install; overrides global-config for this upgrade |

---

## pvr remoteinfo

Resolve and print information about a remote PVR repository.

```bash
pvr remoteinfo https://pvr.pantahub.com/user/device
pvr remoteinfo user/device
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--canonical, -c` | Canonical JSON output |

---

## pvr stepinfo

Display step information for a remote revision.

```bash
pvr stepinfo https://api.pantahub.com/trails/<DEVICE_ID>/steps/<REV>
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--canonical, -c` | Canonical JSON output |
