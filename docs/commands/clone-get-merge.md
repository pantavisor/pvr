---
sidebar_position: 2
title: Clone, Get & Merge
---

# Clone, Get & Merge

Fetch state from local or remote repositories.

---

## pvr clone

Clone a PVR repository into a new directory. Combines `init` + `get` + `checkout`
in one step.

```bash
pvr clone /path/to/pvr/repo my-directory
pvr clone https://pvr.pantahub.com/user/device my-device
pvr clone https://api.pantahub.com/trails/<DEVICE_ID>
pvr clone https://api.pantahub.com/trails/<DEVICE_ID>/steps/<REV>
pvr clone 192.168.1.100              # local device (CGI frontend)
pvr clone --platform=linux/arm64 https://gitlab.com/group/project my-dir
pvr clone --platform=linux/arm64 https://gitlab.com/group/project@1.2.3 my-dir
```

**Notes:**
- IP shorthand (`1.2.3.4`) expands to `http://1.2.3.4:12368/cgi-bin/pvr`
- GitLab package cloning uses `--platform` to select the target architecture

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--objects, -o` | `PVR_OBJECTS_DIR` | Custom object pool directory |
| `--canonical, -c` | `PVR_CANONICAL_JSON` | Clone using canonical JSON format |
| `--spec, -s` | — | State format spec. Default: `pantavisor-service-system@1` |
| `--platform, -p` | — | Target platform for GitLab packages (e.g. `linux/arm/v7`) |

---

## pvr get

Update the local repository's pristine state from another repository without
touching the working copy. Follow with `pvr checkout` to apply.

```bash
pvr get                                    # last-used repo
pvr get /path/to/repo
pvr get /path/to/repo#bsp                  # just the BSP fragment
pvr get export.tar.gz
pvr get pantahub-ci/rpi3_initial_latest    # shorthand
pvr get https://pvr.pantahub.com/user/device
pvr get 192.168.1.100
pvr get --platform=linux/arm64 https://gitlab.com/group/project
```

**Sources:**
- Local `.pvr` directory
- PVR export tarball (`.tar.gz`)
- Pantahub URL or `user/device` shorthand
- Local device IP
- GitLab package (with `--platform`)

**Flags:**

| Flag | Description |
|------|-------------|
| `--platform, -p` | Target platform (e.g. `linux/arm/v7`) |

---

## pvr merge

Merge content from another repository into the working directory.

```bash
pvr merge
pvr merge /path/to/repo
pvr merge my-user/my-device
pvr merge 192.168.1.123#my-app
pvr merge --platform=linux/amd64 https://gitlab.com/group/project@1.2.3
```

Same source syntax as `pvr get`. Tarballs (`.gz`, `.tgz`, `.zip`, `.tar`) are
supported — zip files must contain a tar/tar.gz with the same base name.

**Flags:**

| Flag | Description |
|------|-------------|
| `--platform, -p` | Target platform (e.g. `linux/arm/v7`) |
