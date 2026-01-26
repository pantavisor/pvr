# PVR Builtin Templates & Variables

The `pvr` tool uses a builtin template system to generate the `run.json` manifest and `lxc.container.conf` configuration file for containers. This generation happens when using commands like `pvr app add` or when processing `pvrexport` contents.

These templates utilize the standard Go `text/template` engine with custom functions (`pvr_ifNull`, `pvr_isSlice`, etc.) and the `sprig` library.

## Template Source

The core templates are defined in `src/gitlab.com/pantacor/pvr/templates/builtin-lxc-docker.go`.

## Context Namespaces

The templates have access to several namespaces:
- `.Source`: Metadata about the container source (name, base, args, etc.).
- `.Docker`: Configuration derived from the Docker image manifest.

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

### Run Manifest (`run.json`)

These variables directly affect the structure and metadata of the `run.json` file.

| Variable | Type | Description |
|---|---|---|
| `PV_RUNLEVEL` | string | Defines the runlevel (e.g., "root", "app"). Default: derived. |
| `PV_STATUS_GOAL` | string | Target status (e.g., "MOUNTED"). |
| `PV_CONDITIONS` | map | Conditional startup logic. |
| `PV_RESTART_POLICY` | string | Restart policy (e.g., "always", "on-failure", "unless-stopped"). |
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
| `PV_EXTRA_VOLUMES` | csv | Comma-separated list of extra volumes to create. |

### LXC Configuration (`lxc.container.conf`)

These variables control the low-level LXC configuration parameters.

#### General & Security
| Variable | Default | Description |
|---|---|---|
| `PV_DEBUG_MODE` | false | If set, redirects `lxc.log.file` to `/pv/logs/<name>.log`. |
| `PV_LXC_NAMESPACE_KEEP` | "user,net,ipc" | Namespaces to share with host. Defaults to `user` + `net` (if host net) + `ipc`. |
| `PV_LXC_CAP_DROP` | - | List of capabilities to drop (e.g., `["SYS_TIME"]`). |
| `PV_LXC_CAP_KEEP` | - | List of capabilities to keep. |
| `PVR_LXC_CGROUP_DEVICES_WHITE` | - | List of cgroup device allow rules. |
| `PV_LXC_EXTRA_CONF` | - | Raw string appended to `lxc.container.conf`. |

#### Network & TTY
| Variable | Default | Description |
|---|---|---|
| `PV_LXC_NETWORK_TYPE` | "veth" | Network type: `veth`, `host`, `phys`, `empty`. |
| `PV_LXC_NETWORK_LINK` | - | Physical interface to link (required for `phys`). |
| `PV_LXC_NETWORK_IPV4_ADDRESS` | - | Static IPv4 address for `veth`. |
| `PV_LXC_NETWORK_IPV4_GATEWAY` | "auto" | IPv4 gateway. |
| `LXC_TTY_MAX` | "8" | Max TTYs. |
| `LXC_PTY_MAX` | "1024" | Max PTYs. |

#### Mounts & Storage
| Variable | Description |
|---|---|
| `PV_VOLUME_MOUNTS` | Comma-separated `volume:target` mounts. |
| `PV_VOLUME_IMPORTS` | List of imports `source:origin@vol:dest:perms`. |
| `PV_FILEIMPORTS` | Comma-separated `source:target` file imports. |
| `PV_SECURITY_FULLDEV` | Mounts host `/dev` into container (bind, rw). |
| `PV_SECURITY_WITH_HOST` | Mounts host `/` into container (bind, rw). |
| `PV_SECURITY_WITH_HOSTPROC` | Mounts host `/proc` into container. |
| `PV_SECURITY_WITH_STORAGE` | Mounts `/storage` into container. |
| `PV_RUN_TMPFS_DISABLE` | If true, disables default `/run` tmpfs. |
| `PV_RESOLV_CONF_DISABLE` | If true, disables default `/etc/resolv.conf` bind mount. |

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
