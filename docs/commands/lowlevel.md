---
sidebar_position: 9
---

# Low-Level Commands

Specialized operations for advanced use cases.

---

## pvr dm-apply

Apply device mapper state transforms to a normalized Pantavisor state. Only
processes `_dm` matches for platforms starting with the given prefix.

```bash
pvr dm-apply
pvr dm-apply my-component
```

**Flags:** none

---

## pvr dm-convert

Convert a volume to a device-mapper dm-verity based volume.

```bash
pvr dm-convert <container>/<volume> <new-volume-name>
```

**Flags:** none

---

## pvr fakeroot

Execute commands in a fakeroot environment.

```bash
pvr fakeroot <command> [args...]
```

On Linux with Go 1.21+ and the experimental build tag, uses the native
fakeroot library. On other platforms, shells out to the system `fakeroot`
binary (must be in `PATH`).

**Flags:** none

---

## pvr fastcopy

Copy device state between two Pantahub device URLs without downloading to the
local machine.

```bash
pvr fastcopy https://pvr.pantahub.com/source/device https://pvr.pantahub.com/dest/device
pvr fastcopy -m "update bsp" https://pvr.pantahub.com/source/device#bsp https://pvr.pantahub.com/dest/device
pvr fastcopy -m "copy app" https://pvr.pantahub.com/source/device#my-app https://pvr.pantahub.com/dest/device#renamed-app
```

Fragment syntax selects specific parts of the source/destination state:

| Fragment | Effect |
|----------|--------|
| `#bsp` | Copy only the BSP section |
| `#my-app` | Copy only the named app |
| `#part1,part2,path/part3` | Copy multiple parts |
| `#src-part,dest-part` | Copy with rename |

**Flags:**

| Flag | Description |
|------|-------------|
| `--envelope, -e` | JSON envelope for the post. Default: `{}` |
| `--commit-msg, -m` | Commit message |
| `--rev` | Revision tag. Default: `-1` |
| `--force, -f` | Force re-upload of existing objects |
| `--merge` | Merge into target instead of replacing |
| `--platform, -p` | Target platform (e.g. `linux/arm/v7`) |

---

## pvr deploy

Deploy a checked-out repository to a device: hardlink objects into a deploy
directory and create canonical BSP symlinks.

```bash
pvr deploy /path/to/deploy-dir
pvr deploy /path/to/deploy-dir --nomerge
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--nomerge, -n` | Replace full target state (default: merge) |
| `--objects, -o` | Object pool directory. Default: `~/.pvr/objects` |

---

## pvr global-config

Read or set the PVR global configuration.

```bash
pvr global-config                  # print current config as JSON
pvr global-config DistributionTag=stable
pvr global-config AutoUpgrade=true
```

Configuration keys: `Spec`, `AutoUpgrade`, `DistributionTag`.

**Flags:** none
