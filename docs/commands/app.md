---
sidebar_position: 4
---

# Application Commands

Manage container applications in a PVR checkout.

---

## pvr app add

Add a new container application from a Docker image, root filesystem, or PVR
repository.

```bash
# From Docker Hub
pvr app add nginx --from=nginx:alpine
pvr app add nginx --from=nginx --source=remote,local --platform=linux/arm64

# From a rootfs tarball or directory
pvr app add my-app --from=~/rootfs.tar -t rootfs
pvr app add my-app --from=~/rootfs-dir -t rootfs

# From a PVR repository
pvr app add tailscale -t pvr --from https://pvr.pantahub.com/user/marketplace#tailscale

# With custom template args
pvr app add my-app --from=nginx --arg KEY=VALUE --arg-json args.json

# Application extending another (shared base rootfs)
pvr app add --group=data --status-goal=MOUNTED --from=ros:rolling-ros-base rolling-ros-base
pvr app add --base rolling-ros-base --from=another-container extended-app
```

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--from` | `PVR_FROM` | Container image to use as source |
| `--type, -t` | `PVR_SOURCE_TYPE` | Source type: `docker` (default), `pvr`, `rootfs` |
| `--source` | `PVR_SOURCE` | Source priority: comma-separated `remote,local` (default) |
| `--platform` | `PVR_PLATFORM` | Target platform (e.g. `linux/arm/v7`) |
| `--group` | `PVR_GROUP` | Container group: `data`, `root`, `platform`, `app` |
| `--restart-policy` | `PVR_RESTART_POLICY` | Restart policy: `system` or `container` |
| `--status-goal` | `PVR_STATUS_GOAL` | Desired status: `MOUNTED`, `STARTED`, `READY` |
| `--base` | `PVR_APP_ADD_BASE` | Base app name for diff/patch rootfs |
| `--config-json` | `PVR_CONFIG_JSON` | Docker image config as a JSON file |
| `--config` | `PVR_CONFIG_JSON` | Config values as `KEY=VALUE` parameters (repeatable) |
| `--arg` | `PVR_TEMPLATE_ARG` | Template arguments as `KEY=VALUE` (repeatable) |
| `--arg-json` | `PVR_TEMPLATE_ARG` | Template arguments as a JSON file |
| `--volume` | `PVR_PERSISTENCE_VOLUME` | Persistence volumes (repeatable) |
| `--format-options, -o` | `PVR_FORMAT_OPTIONS` | SquashFS format options (e.g. `-comp gzip`) |
| `--username, -u` | `PVR_REGISTRY_USERNAME` | Docker registry username |
| `--password, -p` | `PVR_REGISTRY_PASSWORD` | Docker registry password |
| `--add-only, -ao` | `PVR_APP_ADD_ONLY` | Only write src.json, skip rootfs generation |
| `--force, -f` | `PVR_APP_ADD_FORCE` | Overwrite if app already exists |

---

## pvr app ls

List all applications in the PVR checkout.

```bash
pvr app ls
```

**Flags:** none

---

## pvr app info

Show details and state of a specific application.

```bash
pvr app info nginx
```

Output includes the full `src.json` content (args, config, docker metadata, etc.).

**Flags:** none

---

## pvr app rm

Remove an application from the PVR checkout.

```bash
pvr app rm nginx
```

**Flags:** none

---

## pvr app install

Regenerate application files (rootfs, overlay) from the `src.json` manifest
without re-pulling the Docker image.

```bash
pvr app install nginx
pvr app install
```

Useful after editing `src.json` to apply new template arguments or config.

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--type, -t` | `PVR_SOURCE_TYPE` | Source type override |
| `--source` | `PVR_SOURCE` | Source priority override |
| `--base` | `PVR_APP_ADD_BASE` | Base app name for patch rootfs |
| `--arg` | `PVR_TEMPLATE_ARG` | Template arguments `KEY=VALUE` (repeatable); merged into src.json |
| `--arg-json` | `PVR_TEMPLATE_ARG` | Template arguments as JSON file |
| `--config` | `PVR_CONFIG_JSON` | Config values `KEY=VALUE` (repeatable) |
| `--unset-arg` | — | Remove template arg from src.json (repeatable) |
| `--unset-config` | — | Remove config key from src.json (repeatable) |
| `--username, -u` | `PVR_REGISTRY_USERNAME` | Docker registry username |
| `--password, -p` | `PVR_REGISTRY_PASSWORD` | Docker registry password |

---

## pvr app update

Update an existing application — re-pull the Docker image and regenerate files.

```bash
pvr app update nginx
pvr app update nginx --from=nginx:1.25    # also change image/tag
pvr app update nginx --patch              # only update overlay
pvr app update nginx --newbase            # clean patch overlay
```

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--from` | `PVR_APP_FROM` | Update docker_name and docker_tag before updating |
| `--type, -t` | `PVR_SOURCE_TYPE` | Source type: `docker`, `pvr`, `rootfs`, `gitlab-pkg` |
| `--source` | `PVR_SOURCE` | Source priority override |
| `--platform` | `PVR_PLATFORM` | Target platform |
| `--patch` | `PVR_APP_UPDATE_PATCH` | Only update the patch overlay |
| `--newbase` | `PVR_APP_UPDATE_PATCH_CLEAN` | Rebase: clean patch overlay |
| `--base` | `PVR_APP_ADD_BASE` | Base app for patch rootfs |
| `--arg` | `PVR_TEMPLATE_ARG` | Template arguments `KEY=VALUE` (repeatable) |
| `--arg-json` | `PVR_TEMPLATE_ARG` | Template arguments as JSON file |
| `--config` | `PVR_CONFIG_JSON` | Config values `KEY=VALUE` (repeatable) |
| `--unset-arg` | — | Remove template arg (repeatable) |
| `--unset-config` | — | Remove config key (repeatable) |
| `--username, -u` | `PVR_REGISTRY_USERNAME` | Docker registry username |
| `--password, -p` | `PVR_REGISTRY_PASSWORD` | Docker registry password |
