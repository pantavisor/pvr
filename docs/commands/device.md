---
sidebar_position: 5
---

# Device Commands

Manage and interact with Pantavisor devices through Pantahub.

---

## pvr device ps

List devices you manage, with a quick overview table.

```bash
pvr device ps
```

Output columns: ID, nick, rev, status, state, seen, IP, message.

**Flags:** none

---

## pvr device logs

Fetch and stream device logs from Pantahub.

```bash
pvr device logs <DEVICE_ID>/pantavisor.log@INFO
pvr device logs <DEVICE_ID>/pantavisor.log@INFO#linux
pvr device logs --from=2024-01-01 --to=2024-01-07
pvr device logs --from=P10D --to=P5D       # ISO 8601 duration
pvr device logs --template=json
pvr device logs --no-tail
pvr device logs --platform=linux,windows
pvr device logs --level=DEBUG,INFO
pvr device logs --device=ID1,ID2            # multiple devices
pvr device logs --source=/pantavisor.log,/updater
```

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--no-tail, -n` | `PVR_LOGS_NO_TAIL` | Do not tail; stop at `--to` or now |
| `--from, -f` | `PVR_LOGS_FROM_DATE` | Start datetime (RFC 3339 or ISO 8601 duration) |
| `--to, -t` | `PVR_LOGS_TO_DATE` | End datetime (RFC 3339 or ISO 8601 duration) |
| `--rev, -r` | `PVR_LOGS_REV` | Filter by revision. Default: `-1` (disabled) |
| `--template, -s` | `PVR_LOGS_TEMPLATE` | Output format: `short` (default), `json`, or Go template |
| `--platform, -p` | `PVR_LOGS_PLATFORM` | Comma-separated platforms |
| `--device, -d` | `PVR_LOGS_DEVICE` | Comma-separated device IDs |
| `--source, -src` | `PVR_LOGS_SOURCE` | Comma-separated log sources |
| `--level, -l` | `PVR_LOGS_LEVEL` | Comma-separated log levels |

**Filter syntax (positional):**

```
<deviceid>[/source][@level][#platform]
```

Multiple values can be comma-separated within each segment:
```
pvr device logs ID1,ID2/pantavisor.log,updater@INFO,DEBUG#linux,windows
```

**Go template support:**

The `--template` flag accepts Go template strings with the full `LogsEntry`
struct:

| Field | Description |
|-------|-------------|
| `.ID` | Log entry ID |
| `.Device` | Device ID |
| `.Owner` | Owner PRN |
| `.TimeCreated` | Timestamp |
| `.LogPlat` | Platform name |
| `.LogSource` | Source name |
| `.LogLevel` | Log level |
| `.LogText` | Log message |

Available template functions: all [sprig](https://github.com/Masterminds/sprig)
functions, `sprintf`, `basename`, `timeformat`.

---

## pvr device scan

Scan the local network for Pantavisor devices announcing themselves via mDNS.

```bash
pvr device scan
```

**Flags:** none

---

## pvr device create

Create a new device on Pantahub. After creation, PVR uploads the local state,
logs in the device, and creates a trail.

```bash
pvr device create my-device
pvr device create user/my-device
```

**Flags:** none

---

## pvr device get

Get device details from Pantahub by nick or ID.

```bash
pvr device get <DEVICE_ID>
pvr device get my-device
pvr device get user/my-device
```

Output is the full device JSON (id, nick, owner, PRN, timestamps, etc.).

**Flags:** none

---

## pvr device set

Set `user-meta` or `device-meta` fields on a device.

```bash
pvr device set <DEVICE_ID> key1=value1 key2=value2
```

If logged in as a USER, updates `user-meta`. If logged in as a DEVICE, updates
`device-meta`.

**Flags:** none

---

## pvr device delete

Delete a device from Pantahub.

```bash
pvr device delete <DEVICE_ENDPOINT>
pvr device delete --yes <DEVICE_ENDPOINT>
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--yes, -y` | Confirm deletion (skips prompt) |

---

## pvr device status

Show the current or specified revision's status for a device.

```bash
pvr device status <DEVICE_ID>
pvr device status <DEVICE_ID> --rev=4
```

Without `--rev`, shows the step for the current running revision (from
device-meta `pantavisor.revision`).

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--rev, -r` | `PVR_STATUS_REV` | Show status for a specific revision |

---

## pvr device steps

List revisions (steps) in a device's trail.

```bash
pvr device steps <DEVICE_ID>
pvr device steps <DEVICE_ID> --rev=4    # show a specific step
```

Without `--rev`, lists all steps.

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--rev, -r` | `PVR_STEPS_REV` | Show a specific step revision |

---

## pvr device statediff

Diff the state between two revisions of a device trail.

```bash
pvr device statediff <DEVICE_ID> <REV1> <REV2>
pvr device statediff <DEVICE_ID> 3 5 --format=jsondiff
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--format, -f` | Output format: `unified` (default) or `jsondiff` |

---

## pvr device tty

Interact with a Pantavisor device via USB serial console. Uses pure Go — no
external tools like `picocom` required.

```bash
pvr device tty                          # interactive shell (default /dev/ttyUSB0)
pvr device tty -d /dev/ttyACM0          # specify port
pvr device tty run uname -r             # execute a command
pvr device tty ps                        # list containers
pvr device tty log                       # tail device logs (default: pantavisor, 30 lines)
pvr device tty log pvr-sdk 10           # specific platform + line count
pvr device tty grep error               # search logs
pvr device tty probe                     # scan for available USB serial ports
```

**Subcommands:**

| Subcommand | Description |
|------------|-------------|
| `shell` (default) | Interactive serial console. Exit: `Ctrl+A` then `Ctrl+X` |
| `run CMD` | Execute one command and print output |
| `ps` | List containers (runs `lxc-ls` + `pvcontrol ls`) |
| `log [PLAT [N]]` | Tail device logs. Default: `pantavisor`, 30 lines |
| `grep PATTERN` | Search device logs for a pattern |
| `probe` | Scan `/dev/ttyUSB*` and `/dev/ttyACM*` for active devices |

**Flags:**

| Flag | Description |
|------|-------------|
| `--device, -d` | Serial port path. Default: `/dev/ttyUSB0` |
| `--baud, -b` | Serial console baud rate (e.g. 921600). Default: `115200` |
| `--verbose, -v` | Show verbose probe output |

Linux-only.

---

## pvr device tokens

Manage device authentication tokens.

### pvr device tokens list (ls)

List device tokens with ID, nick, disabled status, and user-meta.

```bash
pvr device tokens list
```

### pvr device tokens create

Create a new device token with optional key=value metadata.

```bash
pvr device tokens create key1=value1 key2=value2
```

### pvr device tokens disable

Disable a device token by ID.

```bash
pvr device tokens disable <TOKEN_ID>
```

**Flags:** none (all subcommands)
