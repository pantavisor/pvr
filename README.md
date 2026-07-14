# PVR — Pantavisor Repository

PVR is the Pantavisor repository tool for managing device state as a
versioned JSON document with an object store backend.

## Quick Install

```bash
curl -sL https://gitlab.com/pantacor/pvr/-/raw/master/install.sh | bash
```

Pre-built binaries for various architectures are available on the
[GitLab package registry](https://gitlab.com/pantacor/pvr/-/packages).

### Build from source

```bash
git clone https://gitlab.com/pantacor/pvr.git
cd pvr
go build -o ~/bin/pvr ./cmd/pvr
```

Requires Go 1.21+.

## Basics

```bash
# Start a new repository
pvr init

# Clone an existing device state
pvr clone https://pvr.pantahub.com/user/device my-device

# Add a container app
pvr app add nginx --from=nginx:alpine

# Stage, review, and commit
pvr add .
pvr status
pvr diff
pvr commit -m "add nginx"

# Deploy to device
pvr post https://api.pantahub.com/trails/<DEVICE_ID>
```

## Documentation

Full command reference and usage guides are in the [docs/](docs/) directory:

| Topic | File |
|-------|------|
| Overview & command index | [docs/index.md](docs/index.md) |
| Repo commands (init, add, status, diff, commit, ...) | [docs/commands/repo.md](docs/commands/repo.md) |
| Clone, get, merge | [docs/commands/clone-get-merge.md](docs/commands/clone-get-merge.md) |
| Push (put, post, export, import) | [docs/commands/push.md](docs/commands/push.md) |
| Application management | [docs/commands/app.md](docs/commands/app.md) |
| Device management | [docs/commands/device.md](docs/commands/device.md) |
| Signatures | [docs/commands/sig.md](docs/commands/sig.md) |
| Wi-Fi provisioning | [docs/commands/wifi.md](docs/commands/wifi.md) |
| Authentication | [docs/commands/auth.md](docs/commands/auth.md) |
| Low-level operations | [docs/commands/lowlevel.md](docs/commands/lowlevel.md) |
| Utilities | [docs/commands/utils.md](docs/commands/utils.md) |
| Deprecated commands | [docs/commands/deprecated.md](docs/commands/deprecated.md) |

## Templates and Manifests

For template variables and how the manifest generation system works, see
[PVR_TEMPLATES.md](PVR_TEMPLATES.md).

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PVR_USERNAME` | — | Auth username |
| `PVR_PASSWORD` | — | Auth password |
| `PVR_ACCESSTOKEN` | — | Bearer access token |
| `PVR_BASEURL` | `https://api.pantahub.com` | Pantahub API base URL |
| `PVR_REPO_BASEURL` | `https://pvr.pantahub.com` | PVR repository base URL |
| `PVR_CONFIG_DIR` | `~/.pvr` | Global config directory |
| `PVR_OBJECTS_DIR` | `~/.pvr/objects` | Object store directory |
| `PVR_HTTP_PROXY` | `system` | HTTP proxy mode (`no`, URL, or `system`) |
| `PVR_INSECURE` | false | Skip TLS verification |
| `PVR_DEBUG` | false | Enable debug output |
