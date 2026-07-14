---
sidebar_position: 3
---

# Push Commands

Send repository state to local or remote destinations.

---

## pvr put

Copy the local repository to a target location (local or REST).

```bash
pvr put /path/to/destination
pvr put https://pvr.pantahub.com/user/device
pvr put                   # uses last target
```

Copies the state JSON and all objects to the destination.

**Flags:**

| Flag | Description |
|------|-------------|
| `--force, -f` | Force re-upload of existing objects |

---

## pvr post

Post the local repository as a new revision to a device trail on Pantahub.

```bash
pvr post https://api.pantahub.com/trails/<DEVICE_ID>
pvr post user/device                    # shorthand
pvr post                                # last-used target
pvr post --envelope '{"key":"val"}' --commit-msg "my message"
pvr post --rev my-tag
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--envelope, -e` | JSON envelope to wrap the post. Default: `{}` |
| `--commit-msg, -m` | Add commit-msg field to the envelope |
| `--rev` | Name for the new revision |
| `--force, -f` | Force re-upload of existing objects |

---

## pvr export

Export the repository into a single-file tarball.

```bash
pvr export device.tar.gz
pvr export device.tar                   # plain tar
pvr export --parts=bsp,my-app device.tgz
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--parts, -p` | Comma-separated list of parts to export; empty = export all |

---

## pvr import

Import a tarball produced by `pvr export` into the current working directory.
Does **not** run `pvr checkout` — the working directory stays untouched.

```bash
pvr import device.tar.gz
pvr import device.tar
pvr import device.tgz
```

Supports `.gz`, `.tgz`, `.zip`, and plain `.tar`. Zip files must contain a
tar/tar.gz with the same base name.

**Flags:** none

---

## pvr putobjects

Upload local objects to a remote Pantahub objects endpoint.

```bash
pvr putobjects https://api.pantahub.com/objects
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--force, -f` | Force re-upload of existing objects |
