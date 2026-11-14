# pvr

pvr is the command-line tool for Pantavisor device state. A Pantavisor device
runs a *state*: a versioned JSON document that lists its BSP, containers,
volumes and configuration, with large files stored as content-addressed
objects. pvr lets you check that state out into a directory, change it, commit
it as a revision, sign it, and push it to a device or to
[Pantahub](https://pantahub.com), which then updates the device over the air.
It also builds apps and flashable images, and manages devices, Wi-Fi
provisioning and account secrets.

Pantavisor itself lives at [github.com/pantavisor/pantavisor](https://github.com/pantavisor/pantavisor).
The full product documentation is at <https://docs.pantavisor.io>.

## Install

```bash
curl -sL https://raw.githubusercontent.com/pantavisor/pvr/main/install.sh | bash
```

The script installs `pvr` to `~/.local/bin`. Set `PVR_VERSION=059` to pin a
version. Binaries for Linux (amd64, arm, arm64), macOS (amd64, arm64) and
Windows (x32, x64) are also published on the
[GitHub releases page](https://github.com/pantavisor/pvr/releases).

The install script and `pvr self-upgrade` read GitHub releases and fall back
to the old GitLab packages, see [docs/github-migration.md](docs/github-migration.md).

To upgrade an installed pvr:

```bash
sudo pvr self-upgrade --channel stable
```

`--channel` overrides `DistributionTag` for this upgrade without saving it. It
also accepts `master` (or `latest`) and explicit versions such as `056`.
Without it, pvr uses the current user's global configuration: `sudo pvr`
normally reads root's configuration, independently of your own `pvr global-config`.
On older clients without `--channel`, set the channel for root first:

```bash
sudo pvr global-config DistributionTag=stable
sudo pvr self-upgrade
```

### Build from source

```bash
git clone https://github.com/pantavisor/pvr.git
cd pvr
go build -o ~/bin/pvr .
```

Requires Go 1.24 (`go.mod`). `make build-linux-amd64` and the other
`build-*` targets in the `Makefile` produce static binaries.

## Quick start

```bash
# Clone the state of a device from Pantahub
pvr login
pvr clone https://pvr.pantahub.com/<user>/<device> my-device
cd my-device

# Add a container app from a registry image
pvr app add nginx --from=nginx:alpine

# Stage, review and commit
pvr add .
pvr status
pvr diff
pvr commit -m "add nginx"

# Deploy to the device
pvr post https://api.pantahub.com/trails/<DEVICE_ID>
```

Start a new, empty state instead of cloning with `pvr init`.

## Commands

Run `pvr --help` or `pvr <command> --help` for flags. Groups:

| Group | Commands | Reference |
|-------|----------|-----------|
| Repository | `init`, `add`, `status`, `diff`, `commit`, `json`, `inspect`, `reset` | [repo.md](docs/commands/repo.md) |
| Clone and merge | `clone`, `get`, `merge` | [clone-get-merge.md](docs/commands/clone-get-merge.md) |
| Push | `put`, `post`, `export`, `import`, `putobjects` | [push.md](docs/commands/push.md) |
| Apps | `app add`, `app ls`, `app info`, `app rm`, `app install`, `app update` | [app.md](docs/commands/app.md) |
| Remote builds | `build start`, `status`, `logs`, `download`, `cancel`, `list`, `usage`; `secret`, `signing` | [build.md](docs/commands/build.md) |
| Devices | `device ps`, `logs`, `scan`, `ssh`, `tty`, ... | [device.md](docs/commands/device.md) |
| Signatures | `sig add`, `update`, `ls`, `download` | [sig.md](docs/commands/sig.md) |
| Wi-Fi provisioning | `wifi ...` | [wifi.md](docs/commands/wifi.md) |
| Auth | `login`, `whoami`, `register`, `curl`, `claim` | [auth.md](docs/commands/auth.md) |
| Low level | `dm-apply`, `dm-convert`, `fakeroot`, `fastcopy`, `deploy`, `global-config` | [lowlevel.md](docs/commands/lowlevel.md) |
| Utilities | `version`, `completion`, `self-upgrade`, `remoteinfo`, `stepinfo` | [utils.md](docs/commands/utils.md) |

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
| Templates and manifests | [docs/PVR_TEMPLATES.md](docs/PVR_TEMPLATES.md) |

## Templates and Manifests

For template variables and how the manifest generation system works, see
[docs/PVR_TEMPLATES.md](docs/PVR_TEMPLATES.md).

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

## Contributing

Issues and pull requests are welcome at <https://github.com/pantavisor/pvr>.
See [CONTRIBUTING.md](CONTRIBUTING.md) for commit style, tests and releases,
and [AGENTS.md](AGENTS.md) for notes aimed at AI coding agents.
[llms.txt](llms.txt) indexes the docs for LLMs.

## License

Apache License 2.0, see [LICENSE](LICENSE).
