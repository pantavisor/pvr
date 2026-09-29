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

## pvr device live-logs <DEVICE_NICK|ID> [SOURCE...]

Streams a device's log files live through Pantahub, as the Hub UI's Live logs
tab does. It needs a device connected to the Hub over MQTT (the pv-mqtt-sdk
agent) and a token with the full API scope or `devices.logs`. Alias:
`livelogs`.

Without sources it lists the log files of the revision (paths relative to
`/pantavisor/logs/<rev>/`, with their size; `gz` marks rotated files) and
exits:

```
$ pvr device live-logs mydevice
Revision: current
Other revisions: locals/hub-3, locals/hub-2
pantavisor/
  pantavisor.log          47.1 KiB
  pantavisor.log.2.gz      8.9 KiB  gz
os/
  lxc/
    console.log            1.2 KiB
```

With sources (files, or folders to stream every live file under them; at most
10) it prints each line as `[source] line`, the source coloured when stdout is
a terminal:

```
$ pvr device live-logs mydevice pantavisor/pantavisor.log os/lxc/console.log
[pantavisor/pantavisor.log] [pantavisor] 1790… INFO -- [ctrl]: …
[os/lxc/console.log] …
```

Options:

- `--rev REV`: `current` (the default, followed live) or another revision
  name, read once.
- `--tail N`: lines read back from each source first, 0 to 500 (default 200).
- `--filter TEXT`: only lines containing TEXT, compared case-insensitively
  on the device (at most 256 bytes, no control characters).
- `--no-follow`: read the sources once and exit.
- `--all`: stream the whole revision (its live files, at most 64).
- `--json`: one JSON object per line, `{"src","line","seq"}`; with no
  sources, the raw listing.

A session lasts at most 30 minutes. pvr renews its lease every 20 s, reports
lost batches and lines the device dropped (over its 64 KiB/s limit) on stderr,
and stops the session on Ctrl-C or SIGTERM. It exits 0 when the session
finishes or is stopped, 1 when the device refuses it (`busy`, `invalid`) or it
ends with an error, 130 on Ctrl-C.

## pvr device ssh <DEVICE_NICK|ID> [TARGET [COMMAND...]]

Opens an SSH session to a device through Pantahub, as the Hub UI's Terminal
tab does. It needs a device connected to the Hub over MQTT (the pv-mqtt-sdk
agent) with Pantavisor's SSH enabled, and a token with the full API scope or
`devices.ssh`. SSH runs end to end with the device's SSH server: the Hub only
relays the encrypted bytes over a WebSocket.

`TARGET` is the SSH user: `_pv_` (the Pantavisor host, the default), a
container name, or `<tty>@<container>` for that container's console.
`--list` shows the targets:

```
$ pvr device ssh mydevice --list
TARGET   GROUP     STATUS
_pv_     (host)
os       root      STARTED
pvr-sdk  platform  STARTED
$ pvr device ssh mydevice os
$ pvr device ssh mydevice _pv_ cat /proc/cmdline
```

pvr generates an ed25519 key pair for each session and keeps the private key in
memory; the device accepts its public key only while the session lasts. The
terminal follows the local window size.

The device's host key is trusted on first use and kept in
`~/.pvr/known_hosts_web` (OpenSSH known_hosts format, under
`pantahub:<device id>`). The first time, pvr shows the fingerprint and asks;
when stdin is not a terminal it refuses unless `--accept-new`. A changed key
is refused with a warning unless `--replace-host-key`.

With `--proxy`, pvr only relays stdin/stdout to the session, so the regular
OpenSSH client can use it as its ProxyCommand. The session must authorize the
key that ssh offers, so pass its public half (ssh-ed25519 only) with
`--pubkey`:

```
ssh -o ProxyCommand='pvr device ssh %h --proxy --pubkey ~/.ssh/id_ed25519.pub' _pv_@<device>
```

or in `~/.ssh/config` (`%r` passes the target, the ssh user, to the Hub for
its audit record):

```
Host mydevice
    User _pv_
    ProxyCommand pvr device ssh %h %r --proxy --pubkey ~/.ssh/id_ed25519.pub
```

The exit status is the remote shell's or command's when known; 255 when the
SSH connection fails, 4 when the Hub refuses the session.

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

## pvr device accept-ownership

Accept ownership of a device that registered through a device token whose
owner verification mode is `manual`. Until the owner accepts it, hub only
issues the device read-only credentials, so it cannot post state or logs.

```bash
pvr device accept-ownership <NICK|ID>
pvr device accept-ownership <USER_NICK>/<NICK|ID>
```

Run it logged in as the device owner. Devices whose verification is already
complete are reported as-is; devices using `tls` verification are rejected,
since only the device itself can complete that handshake. The same action is
available from the device page in the Hub UI.

Alias: `pvr device accept`.

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

### pvr device steps cancel

Cancel a step that the device has not finished applying yet. The Hub marks
the step `CANCEL` if it is still `NEW`, `QUEUED`, `DOWNLOADING` or
`INPROGRESS`; the device drops the update and keeps its current revision.

```bash
pvr device steps cancel <DEVICE_ID> --rev=4
pvr device steps cancel https://pvr.pantahub.com/<USER>/<NICK>/4
```

The revision comes from `--rev` or from the trailing revision of a pvr URL.
Prints the resulting step progress. Use this instead of the deprecated
`wontgo` endpoint.

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--rev, -r` | `PVR_STEPS_REV` | Step revision to cancel |

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
