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

Requires root/sudo to write to the installation path. Downloads and extracts
the latest release from the GitLab package registry.

**Flags:** none

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
