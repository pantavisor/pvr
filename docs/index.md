---
sidebar_position: 0
---

# PVR — Pantavisor Repository

PVR is the Pantavisor repository tool. It manages device state as a versioned
JSON document with an object store backend, enabling atomic updates, diffs,
merges, and OTA-style pushes for Pantavisor-powered devices.

## Core Concepts

- **State JSON** — a single JSON file that unambiguously describes the entire
  device directory tree. JSON files are inlined; binary blobs are referenced by
  SHA hash.
- **Object Store** — flat directory of content-addressed objects (SHA-256). Objects
  are shared across repos and hardlinked on checkout.
- **Repo** (`.pvr/`) — hidden directory holding the state JSON (`json`), pristine
  state (`pristine.json`), and object store.
- **Revision** — an atomically committed snapshot of the working tree. Revisions
  are pushed to device trails on Pantahub.
- **Trail** — a REST endpoint (Pantahub trail) holding a sequence of revisions for
  a device.

## Typical Workflow

```bash
# Clone an existing device state
pvr clone https://pvr.pantahub.com/user/device my-device

# Add a container application
pvr app add nginx --from=nginx:alpine

# Stage changes
pvr add .
pvr add --raw large-config.json    # keep large JSON as raw object

# Review
pvr status
pvr diff

# Commit
pvr commit -m "add nginx app"

# Deploy to device
pvr post https://api.pantahub.com/trails/<DEVICE_ID>
```

## Global Flags

These flags are declared on the root `pvr` command and can be set on any
subcommand invocation, or via their environment variable.

| Flag | Env | Default | Description |
|------|-----|---------|-------------|
| `--user, -u` | `PVR_USERNAME` | — | Username used for authentication with core services |
| `--password, -p` | `PVR_PASSWORD` | — | Password used for authentication with core services |
| `--access-token, -a` | `PVR_ACCESSTOKEN` | — | Use `ACCESS_TOKEN` for authorization with core services |
| `--baseurl, -b` | `PVR_BASEURL` | `https://api.pantahub.com` | Use `BASEURL` for resolving prn URIs to core service endpoints |
| `--http-proxy` | `PVR_HTTP_PROXY` | `system` | Use `PVR_HTTP_PROXY` to configure the proxy mode. Values 'no' (disable), '<URL>' (use proxy by url), 'system' (use system settings - default) |
| `--repo-baseurl, -r` | `PVR_REPO_BASEURL` | `https://pvr.pantahub.com` | Use `PVR_REPO_BASEURL` for resolving PVR repositories like docker through user/name syntax. |
| `--config-dir, -c` | `PVR_CONFIG_DIR` | `~/.pvr` | Use `PVR_CONFIG_DIR` for using a custom global config directory (used to store auth.json etc.). |
| `--debug, -d` | `PVR_DEBUG` | — | enable debugging output for rest calls |
| `--disable-self-upgrade` | `PVR_DISABLE_SELF_UPGRADE` | — | disable self-upgrades |
| `--insecure, -i` | `PVR_INSECURE` | — | skip tls verify |

## Command Reference

| Category | Commands |
|---|---|
| **Repo** | [`init`](commands/repo.md#pvr-init), [`add`](commands/repo.md#pvr-add), [`status`](commands/repo.md#pvr-status), [`diff`](commands/repo.md#pvr-diff), [`commit`](commands/repo.md#pvr-commit), [`json`](commands/repo.md#pvr-json), [`inspect`](commands/repo.md#pvr-inspect), [`reset` / `checkout`](commands/repo.md#pvr-reset) |
| **Clone / Get / Merge** | [`clone`](commands/clone-get-merge.md#pvr-clone), [`get`](commands/clone-get-merge.md#pvr-get), [`merge`](commands/clone-get-merge.md#pvr-merge) |
| **Push** | [`put`](commands/push.md#pvr-put), [`post`](commands/push.md#pvr-post), [`export`](commands/push.md#pvr-export), [`import`](commands/push.md#pvr-import), [`putobjects`](commands/push.md#pvr-putobjects) |
| **Applications** | [`app add`](commands/app.md#pvr-app-add), [`app ls`](commands/app.md#pvr-app-ls), [`app info`](commands/app.md#pvr-app-info), [`app rm`](commands/app.md#pvr-app-rm), [`app install`](commands/app.md#pvr-app-install), [`app update`](commands/app.md#pvr-app-update) |
| **Remote Builds** | [`build start`, `status`, `logs`, `download`, `cancel`, `list`](commands/build.md) |
| **Published Artifacts** | [`artifact publish`, `ls`, `rm`, `sdk publish`, `ls`, `rm`](commands/artifact.md) |
| **Devices** | [`device ps`](commands/device.md#pvr-device-ps), [`device logs`](commands/device.md#pvr-device-logs), [`device scan`](commands/device.md#pvr-device-scan), [`device create`](commands/device.md#pvr-device-create), [`device get`](commands/device.md#pvr-device-get), [`device set`](commands/device.md#pvr-device-set), [`device delete`](commands/device.md#pvr-device-delete), [`device accept-ownership`](commands/device.md#pvr-device-accept-ownership), [`device status`](commands/device.md#pvr-device-status), [`device steps`](commands/device.md#pvr-device-steps), [`device statediff`](commands/device.md#pvr-device-statediff), [`device tty`](commands/device.md#pvr-device-tty), [`device tokens`](commands/device.md#pvr-device-tokens) |
| **Signatures** | [`sig add`](commands/sig.md#pvr-sig-add), [`sig update`](commands/sig.md#pvr-sig-update), [`sig ls`](commands/sig.md#pvr-sig-ls), [`sig download`](commands/sig.md#pvr-sig-download) |
| **Wi-Fi Provisioning** | [`wifi device-scan`](commands/wifi.md#pvr-wifi-device-scan), [`wifi provision`](commands/wifi.md#pvr-wifi-provision), [`wifi identify`](commands/wifi.md#pvr-wifi-identify), [`wifi device-info`](commands/wifi.md#pvr-wifi-device-info), [`wifi hostname`](commands/wifi.md#pvr-wifi-hostname), [`wifi wifi-scan`](commands/wifi.md#pvr-wifi-wifi-scan), [`wifi claim-info`](commands/wifi.md#pvr-wifi-claim-info) |
| **Auth** | [`login`](commands/auth.md#pvr-login), [`whoami`](commands/auth.md#pvr-whoami), [`register`](commands/auth.md#pvr-register), [`curl`](commands/auth.md#pvr-curl), [`claim`](commands/auth.md#pvr-claim) |
| **Low-Level** | [`dm-apply`](commands/lowlevel.md#pvr-dm-apply), [`dm-convert`](commands/lowlevel.md#pvr-dm-convert), [`fakeroot`](commands/lowlevel.md#pvr-fakeroot), [`fastcopy`](commands/lowlevel.md#pvr-fastcopy), [`deploy`](commands/lowlevel.md#pvr-deploy), [`global-config`](commands/lowlevel.md#pvr-global-config) |
| **Utilities** | [`version`](commands/utils.md#pvr-version), [`completion`](commands/utils.md#pvr-completion), [`self-upgrade`](commands/utils.md#pvr-self-upgrade), [`remoteinfo`](commands/utils.md#pvr-remoteinfo), [`stepinfo`](commands/utils.md#pvr-stepinfo) |
| **Deprecated** | [`scan`](commands/deprecated.md#pvr-scan), [`ps`](commands/deprecated.md#pvr-ps), [`logs`](commands/deprecated.md#pvr-logs) |
