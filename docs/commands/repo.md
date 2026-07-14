---
sidebar_position: 1
---

# Repo Commands

Core commands for creating and managing a PVR repository's working directory.

---

## pvr init

Initialize a new PVR repository in the current directory.

```bash
pvr init
pvr init --spec pantavisor-multi-platform@1
pvr init --objects /custom/object/pool
```

Creates `.pvr/json` with the default spec. Also creates a `systemc` directory if
it does not exist.

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--objects, -o` | `PVR_OBJECTS_DIR` | Custom object pool directory (absolute or relative). Default: `~/.pvr/objects` |
| `--spec, -s` | — | State format spec. Default: `pantavisor-service-system@1`. Legacy: `pantavisor-multi-platform@1` |

---

## pvr add

Put files under PVR management so they appear in status, diff, and commit.

```bash
pvr add lxc-platform.conf lxc-platform.json
pvr add .
```

Once added, files are tracked in `.pvr/tracking.json` until the next commit.

### pvr add --raw

By default, JSON files are inlined into the state JSON. Use `--raw` to store
them as content-addressed objects instead (preserves exact formatting and avoids
bloating the state JSON for large files).

```bash
pvr add --raw large-config.json
```

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--raw, -r` | `PVR_ADD_RAW` | Store files as raw objects (including .json) |

---

## pvr status

Show working directory changes compared to the last committed state.

```bash
pvr status
```

Output format:
```
A newfile.txt         # added
D deleted.txt         # deleted
C some.json           # changed
```

**Flags:** none

---

## pvr diff

Show a JSON-patch-format diff of the working directory vs. the last commit.

```bash
pvr diff
pvr diff config.json platform.conf
```

Filter by specific files by passing them as arguments.

```json
{
  "deleted.txt": null,
  "newfile.txt": "sha256:abc...",
  "some.json": { "key": "new-value" }
}
```

**Flags:** none

---

## pvr commit

Atomically save the working directory state as a new revision.

```bash
pvr commit
pvr commit -m "added wifi config"
pvr commit --checkpoint
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--message, -m` | Commit message |
| `--checkpoint, -c` | Update the checkpoint token so this revision is tested and used as a fallback |

---

## pvr json

Print the aggregate state JSON for the current working directory.

```bash
pvr json
pvr json --canonical
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--canonical, -c` | Canonical JSON output (sorted keys) |

---

## pvr inspect

Inspect a repository's state without checking it out.

```bash
pvr inspect                         # local .pvr
pvr inspect /path/to/repo
pvr inspect /path/to/repo#bsp       # just the BSP fragment
pvr inspect user/device             # remote Pantahub device
pvr inspect export.tar.gz           # exported tarball
```

Useful in CI/CD pipelines for verifying container configuration before
deployment.

**Flags:**

| Flag | Description |
|------|-------------|
| `--canonical, -c` | Canonical JSON output |

---

## pvr reset

Reset (checkout) the working directory to match the last committed state.
Forgets about added files; `status` and `diff` become empty afterwards.

Aliases: `checkout`, `co`

```bash
pvr reset
pvr reset --hardlink
pvr reset --canonical
```

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--hardlink, -hl` | — | Use hardlinks to objects (files become read-only) |
| `--canonical, -c` | `PVR_CANONICAL_JSON` | Canonical JSON formatting for checked-out JSON files; hardlink implies this |
