# PVR Builtin Templates & Variables

The `pvr` tool uses a builtin template system to generate the `run.json` manifest and `lxc.container.conf` configuration file for containers. This generation happens when using commands like `pvr app add` or when processing `pvrexport` contents.

These templates utilize the standard Go `text/template` engine with custom functions (`pvr_ifNull`, `pvr_isSlice`, etc.) and the `sprig` library.

## Template Source

The core templates are defined in `templates/builtin-lxc-docker.go`.

Template functions come from three sets:
- `sprig` functions, available both unprefixed (`splitList`, `trimPrefix`, `quote`, `join`, `hasKey`, ...) and with a `sprig_` prefix.
- `gtf` functions, available with a `gtf_` prefix only.
- `pvr` helpers, available with a `pvr_` prefix only (`pvr_ifNull`, `pvr_isSlice`, `pvr_sliceIndex`, `pvr_sliceFrom`, `pvr_sliceTo`, `pvr_isEmpty`, `pvr_jsonIndent`, `pvr_dockerVolumeName`, `pvr_mergePersistentMaps`).

## Context Namespaces

The templates have access to several namespaces:
- `.Source`: The container's `src.json` as a map (see below).
- `.Docker`: Configuration derived from the Docker image manifest, overlaid with `src.json`'s `config` section.
- `.EffectiveGroup`: The group written to `run.json`. Resolved from `PV_GROUP`; if neither `PV_GROUP` nor `PV_RUNLEVEL` is set, the default group from `groups.json` is used.

### Source Context (`.Source`)

| Field | Description |
|---|---|
| `.Source.name` | Container/app name (the directory name). |
| `.Source.base` | Base app name when the container is an overlay on another rootfs. |
| `.Source.args` | The `args` map — all `PV_*` / `LXC_*` variables documented below. |
| `.Source.persistence` | Per-path persistence map (`permanent`, `revision`, `boot`, optional `@<disk>` suffix, optional `ovl:` prefix). |
| `.Source.exports` | List of export globs, copied into `run.json` as `exports`. |
| `.Source.logs` | Log configuration, copied into `run.json` as `logs`. |
| `.Source.dm_enabled` | Map of volumes to be verity/dm protected; those entries get a `dm:` prefix in `run.json`. |
| `.Source.docker_ovl_digest` | Set when the rootfs is an overlay (`root.ovl.squashfs`) on top of `base`. |

## Docker Context (`.Docker`)

When a Docker image is converted to a Pantavisor `pvrexport`, `pvr` extracts metadata from the Docker image manifest and populates the `.Docker` namespace. These values are used to determine the default execution behavior (init command, environment, working directory).

### Standard Docker Variables

| Variable | Source | Description |
|---|---|---|
| `.Docker.Entrypoint` | Entrypoint | The command to run as PID 1. |
| `.Docker.Cmd` | Cmd | Arguments to the Entrypoint. |
| `.Docker.Env` | Env | Environment variables defined in the image. |
| `.Docker.WorkingDir` | WorkingDir | Default working directory for the process. |
| `.Docker.Volumes` | Volumes | List of volumes defined as `VOLUME` in Dockerfile. |

### Overloading with `config.json`

You can explicitly override these Docker manifest values using a `config.json` file. This is useful when you want to change the entrypoint or environment of an existing image without rebuilding it.

In the `pvr` CLI, use the `--config-json` flag:
```bash
pvr app add myapp --from=nginx --config-json=custom-config.json
```

Individual keys can also be set/removed without a file (`app add`, `app update`, `app install`):
```bash
pvr app update myapp --config WorkingDir=/app --unset-config Entrypoint
```

**Example `custom-config.json`**:
```json
{
  "Entrypoint": ["/usr/bin/custom-init"],
  "Cmd": ["--flag", "value"],
  "Env": [
    "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
    "CUSTOM_VAR=enabled"
  ],
  "WorkingDir": "/app"
}
```

When provided, these values will replace the ones extracted from the image manifest in the template rendering process.

## Variables (`args.json`)

The following variables can be defined in the `args` section of your container source (typically `args.json`) to customize the generated configuration.

They can be supplied on the CLI as well (`app add`, `app update`, `app install`):
```bash
pvr app add myapp --from=nginx --arg PV_GROUP=platform --arg PV_VOLUME_MOUNTS=data:/var/lib/data
pvr app update myapp --arg-json args.json          # whole file, merged into src.json args
pvr app update myapp --unset-arg PV_DEBUG_MODE     # applied after --arg
```

### Run Manifest (`run.json`)

These variables directly affect the structure and metadata of the `run.json` file.

| Variable | Type | Description |
|---|---|---|
| `PV_GROUP` | string | Group the container belongs to (e.g., "data", "root", "platform", "app", or any group from `groups.json`). Preferred over `PV_RUNLEVEL` on Pantavisor 015+. |
| `PV_RUNLEVEL` | string | Deprecated runlevel: "data", "root", "platform" or "app". Mutually exclusive with `PV_GROUP`. |
| `PV_STATUS_GOAL` | string | Status the container must reach after bootup: "MOUNTED", "STARTED" or "READY". |
| `PV_CONDITIONS` | map | Conditional startup logic. |
| `PV_RESTART_POLICY` | string | What is restarted when this container is modified/fails: "system" or "container". |
| `PV_ROLES` | array | List of roles assigned to this container (e.g., `["mgmt"]`). |
| `PV_AUTO_RECOVERY` | object | Auto-recovery configuration settings. |
| `PV_DRIVERS_MANUAL` | array | List of manual drivers. |
| `PV_DRIVERS_REQUIRED` | array | List of required drivers. |
| `PV_DRIVERS_OPTIONAL` | array | List of optional drivers. |
| `PV_SERVICES_MANUAL` | array | List of manual services. |
| `PV_SERVICES_REQUIRED` | array | List of required services (used for pv-xconnect). |
| `PV_SERVICES_OPTIONAL` | array | List of optional services. |
| `PV_NETWORK_POOL` | string | Network pool to use (e.g., "lxcbr0"). |
| `PV_NETWORK_HOSTNAME` | string | Container hostname. Defaults to container name. |
| `PV_NETWORK_IP` | string | Static IP address. |
| `PV_NETWORK_MAC` | string | MAC address. |
| `PV_EXTRA_VOLUMES` | csv | Comma-separated list of extra volumes to create. Volumes referenced by `PV_VOLUME_MOUNTS` are added automatically. |
| `PV_DEV_LOG` | bool | Renders `"dev-log"`. Value `false` (or the string `"false"`) disables the per-container devlog; any other value enables it. Omit the key to leave `dev-log` out of `run.json` entirely. |
| `PV_AUTOMODFW` | bool | Renders `"automodfw"`: auto-mount of `/lib/modules` and `/lib/firmware` into the container. |
| `PV_REMOUNT` | object | Renders `"remount"`: per-container remount policies, e.g. `{"default": [{"/.*": "nosuid,noexec,noatime,nodev"}, {"/": "ro"}]}`. The active policy is chosen by the device's `PV_REMOUNT_POLICY` config key. |

> **Note:** when `PV_RUNLEVEL` is `data` or `PV_STATUS_GOAL` is `MOUNTED`, the container is a pure data/volume container: no `lxc.container.conf` is generated at all, and `config`, `type`, `roles`, `exports` and `logs` are omitted from `run.json`.

### LXC Configuration (`lxc.container.conf`)

These variables control the low-level LXC configuration parameters.

#### General & Security
| Variable | Default | Description |
|---|---|---|
| `PV_DEBUG_MODE` | false | If set, redirects `lxc.log.file` to `/pv/logs/<name>.log`. |
| `PV_LXC_NAMESPACE_KEEP` | `user net ipc` | Space-separated namespaces to inherit from Pantavisor. When unset, the default is `user ipc` plus `net` — `net` is only included when `PV_LXC_NETWORK_TYPE` is unset or `host`. |
| `PV_LXC_CAP_DROP` | - | List of capabilities to drop (e.g., `["SYS_TIME"]`); lowercased on render. |
| `PV_LXC_CAP_KEEP` | - | List of capabilities to keep. Use either keep or drop, not both. |
| `PVR_LXC_CGROUP_DEVICES_WHITE` | - | List of cgroup device allow rules. When set, `lxc.cgroup.devices.deny = a` is emitted first; when unset, `lxc.cgroup.devices.allow = a` is emitted. |
| `PVR_LXC_CGROUP_V1` | - | List of raw cgroup v1 settings appended after `lxc.cgroup.`, e.g. `["memory.limit_in_bytes = 100M"]`. |
| `PV_LXC_EXTRA_CONF` | - | Raw string appended to `lxc.container.conf`. |
| `PV_LXC_EXTRA_ENV` | - | List of extra `KEY=VALUE` entries rendered as `lxc.environment`, on top of `.Docker.Env`. |
| `PV_DISABLE_AUTODEV` | false | Sets `lxc.autodev = 0`. Only do this if you know what you are doing. |

#### Network & TTY
| Variable | Default | Description |
|---|---|---|
| `PV_LXC_NETWORK_TYPE` | - | Network type: `veth`, `host`, `phys`, `empty`. When unset, no `lxc.net.0.*` is emitted and `net` is inherited from the host. |
| `PV_LXC_NETWORK_LINK` | - | Physical interface to link (required for `phys`). |
| `PV_LXC_NETWORK_IPV4_ADDRESS` | - | Static IPv4 address for `veth`. |
| `PV_LXC_NETWORK_IPV4_GATEWAY` | "auto" | IPv4 gateway. Only emitted together with `PV_LXC_NETWORK_IPV4_ADDRESS`. |
| `LXC_TTY_MIN` | - | Min TTYs (`lxc.tty.min`); omitted when unset. |
| `LXC_TTY_MAX` | "8" | Max TTYs. |
| `LXC_PTY_MAX` | "1024" | Max PTYs. |
| `PV_LXC_DISABLE_CONSOLE` | false | Sets `lxc.console.path = none`. |

#### Mounts & Storage
| Variable | Default | Description |
|---|---|---|
| `LXC_MOUNT_AUTO_PROC` | "proc" | First `lxc.mount.auto` entry (e.g. `proc:mixed`). |
| `LXC_MOUNT_AUTO_SYS` | "sys:rw" | Second `lxc.mount.auto` entry (e.g. `sys:ro`). |
| `LXC_MOUNT_AUTO_GROUP` | "cgroup" | Third `lxc.mount.auto` entry (e.g. `cgroup:mixed`). |
| `PV_VOLUME_MOUNTS` | - | Comma-separated `<volume-name>:<target-path>`. Mounts a volume owned by this container at additional places; the volume is also added to `run.json` `volumes`. |
| `PV_VOLUME_IMPORTS` | - | List of `<source-container>:[<subpath>@]<source-volume>:<target-path>[:<perms>]` (perms default `rw`). Imports a volume, or a subpath within a volume, from another container. |
| `PV_IMPORT_CONFIGVOLUMES` | - | Comma-separated `<volume-name>:<target-path>`. Mounts *config volumes* — volumes holding configs for several containers, keyed by container name at the top level. For a container `foo`, `<volume-name>/foo` is mounted at `<target-path>` (bind, rw, `optional`, `noexec`). |
| `PV_FILEIMPORTS` | - | Comma-separated `<source>:<target>` file imports from `/exports/`. |
| `PV_SECURITY_FULLDEV` | false | Mounts host `/dev` into container (bind, rw). Usually you do not want this. |
| `PV_SECURITY_WITH_HOST` | false | Mounts host `/` at `host/` in the container (bind, rw). |
| `PV_SECURITY_WITH_HOSTPROC` | false | Mounts host `/proc` at `host/proc` in the container. |
| `PV_SECURITY_WITH_STORAGE` | false | Mounts the Pantavisor `/storage` partition into the container. |
| `PV_RUN_TMPFS_DISABLE` | false | If true, disables default `/run` tmpfs. |
| `PV_RUN_TMPFS_PATH` | "run" | Target path of the default tmpfs mount (container-relative). |
| `PV_RESOLV_CONF_DISABLE` | false | If true, disables default `/etc/resolv.conf` bind mount. |
| `PV_RESOLV_CONF_PATH` | "etc/resolv.conf" | Target path of the host `resolv.conf` bind mount (container-relative). |

## Generation Process

1.  **Input**: `pvr` takes the Docker container configuration (Env, Cmd, Entrypoint, Volumes) and the user-supplied `args.json`.
2.  **Processing**: The values are merged into a context object (`.Source`, `.Docker`, etc.).
3.  **Rendering**:
    *   **lxc.container.conf**: Generates the LXC config file using the template logic.
    *   **run.json**: Generates the Pantavisor manifest, mapping Docker settings and `args` variables to the standard fields.
4.  **Output**: These files are written to the container directory (or `pvrexport`).

## Advanced Feature Configuration

### IPAM Networking

The `PV_NETWORK_*` keys configure container networking via Pantavisor's IPAM subsystem.

```json
{
  "PV_NETWORK_POOL": "internal",
  "PV_NETWORK_IP": "10.0.3.50",
  "PV_NETWORK_HOSTNAME": "my-custom-host"
}
```

### Volumes, Imports and Config Volumes

```json
{
  "PV_EXTRA_VOLUMES": "data,cache",
  "PV_VOLUME_MOUNTS": "data:/var/lib/myapp,cache:/var/cache/myapp",
  "PV_VOLUME_IMPORTS": ["pvr-sdk:certs@config:/etc/ssl/certs:ro"],
  "PV_IMPORT_CONFIGVOLUMES": "_config:/etc/myapp-config"
}
```

* `PV_VOLUME_MOUNTS` mounts volumes owned by this container (`/volumes/<container>/<volume>`) at extra paths.
* `PV_VOLUME_IMPORTS` mounts a volume of *another* container; the optional `subpath@volume` form imports only a subdirectory, and the trailing field sets mount options (`ro`/`rw`).
* `PV_IMPORT_CONFIGVOLUMES` mounts a shared config volume, picking the subdirectory named after this container. For a container `foo`, the entry above mounts `/volumes/_config/foo` at `/etc/myapp-config`. The mount is `optional`, so a missing directory does not fail container start, and `noexec`.

### Service Mesh & Ingress (pv-xconnect)

The `PV_SERVICES_REQUIRED` key is used to define dependencies on other containers or to configure external traffic ingress.

#### TCP Ingress
Routes external traffic from port 2222 into the container, which then proxies it to a backend service named `raw-unix`.
```json
{
  "PV_SERVICES_REQUIRED": [
    {
      "name": "raw-unix",
      "type": "tcp",
      "target": "0.0.0.0:2222"
    }
  ]
}
```

#### HTTP Ingress with Path Rewriting
Routes external traffic on port 80 with path `/api` to a backend service named `network-manager`. The path prefix is stripped when forwarding to the provider.
```json
{
  "PV_SERVICES_REQUIRED": [
    {
      "name": "network-manager",
      "type": "http",
      "target": "0.0.0.0:80/api"
    }
  ]
}
```

### Auto-Recovery (Restart Policies)

The `PV_AUTO_RECOVERY` key defines how Pantavisor should handle container crashes using an exponential backoff strategy.

#### Option 1: Wholesale JSON object
```json
{
  "PV_AUTO_RECOVERY": {
    "policy": "on-failure",
    "max_retries": 10,
    "retry_delay": 5,
    "backoff_factor": 2.0,
    "reset_window": 300
  }
}
```

#### Option 2: Granular variables (Less error-prone)
```json
{
  "PV_AUTO_RECOVERY_POLICY": "on-failure",
  "PV_AUTO_RECOVERY_MAX_RETRIES": 10,
  "PV_AUTO_RECOVERY_RETRY_DELAY": 5,
  "PV_AUTO_RECOVERY_BACKOFF_FACTOR": 2.0,
  "PV_AUTO_RECOVERY_RESET_WINDOW": 300
}
```

If `PV_AUTO_RECOVERY` is present it wins; the granular keys are only used when it is absent.

### Remount Policies

`PV_REMOUNT` defines per-container remount operations applied after the rootfs and volumes are mounted. Each policy is a list of `path-regex: mount-options` directives; the policy actually executed is selected by the device's `PV_REMOUNT_POLICY` configuration key (default: `default`).

```json
{
  "PV_REMOUNT": {
    "default": [
      { "/.*": "nosuid,noexec,noatime,nodev" },
      { "/dev": "dev" },
      { "/": "ro" }
    ],
    "dev": [
      { "/.*": "nosuid,exec,noatime,nodev" }
    ]
  }
}
```
