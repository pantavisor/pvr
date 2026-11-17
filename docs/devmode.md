# pvr devmode

Edit files on your workstation and sync them into the containers of a device
that runs a Pantavisor devmode revision, like `adb sync`. pvr reaches the
device in one of three ways, all of which end at the devmode calls of pv-ctrl:

| Transport | Device argument | What pvr does |
|---|---|---|
| pvcontrol | `host[:port]`, `user@host[:port]` or `--exec CMD` | starts a shell on the device (ssh, or your own command) and runs `pvcontrol` |
| http | `http://host[:port]` | calls the pv-ctrl pass-through of the pvr-sdk container |
| Hub | `hub:<device id or nick>` | calls the devmode tools of the Hub's MCP endpoint |

The device needs `PV_DEVMODE_ALLOWED` on and a Pantavisor with the devmode file
endpoints (pantavisor/pantavisor#848).

## Reaching the device

```
pvr devmode status 192.168.1.50           # _pv_@192.168.1.50:8222
pvr devmode status root@box.tail1234.ts.net:22
pvr devmode add-device bench 192.168.1.50 # save it
pvr devmode status bench
pvr devmode devices                       # saved devices and an mDNS scan
```

A target is `user@host[:port]` or `host[:port]`. The default user is `_pv_` and
the default port is 8222 (the Pantavisor debug ssh). pvr uses the system `ssh`
with `ControlMaster`/`ControlPersist`, so many file operations share one
connection. Add ssh arguments with `--ssh-opt`, repeatable:
`--ssh-opt=-oIdentityFile=$HOME/.ssh/dev`. New host keys are accepted on first
use (`StrictHostKeyChecking=accept-new`).

Aliases are saved in `<config dir>/devmode-devices.json`. The config dir is
`--config-dir` / `PVR_CONFIG_DIR` (default `~/.pvr`).

Any command that runs a shell on the device works instead of ssh. With `--exec`
there is no `<device>` argument:

```
pvr devmode status --exec "docker exec -i appengine"
pvr devmode add-device ae --exec "docker exec -i appengine"
pvr devmode status ae
```

### Over http

```
pvr devmode status http://192.168.1.50            # port 12368
pvr devmode status http://localhost:12368
pvr devmode add-device bench http://192.168.1.50  # save it
```

This uses the web server of the pvr-sdk container, which passes calls to pv-ctrl
at `http://<host>:12368/cgi-bin/pvctrl/<pv-ctrl path>`. Nothing is needed on the
device besides that container, which needs the `mgmt` role. The web server runs
only while the debug shell is on and listens on 127.0.0.1 by default, so reach it
from a workstation through a tunnel or from a container on the device. There is
no login. The calls pvr makes are listed in [What each transport calls](#what-each-transport-calls).

### Over the Hub

```
pvr login                                         # once, as for pvr clone
pvr devmode status hub:my-device-nick
pvr devmode add-device bench hub:my-device-nick
pvr devmode push bench nginx
```

The device must be connected to the Hub through the pv-mqtt-sdk container. pvr
uses the Hub of `--baseurl` (default https://api.pantahub.com), and your
`pvr login` approves pvr as an application at the Hub's `/mcp` endpoint without
opening a browser. The login needs your user name and password; a personal
access token cannot approve it. The Hub token and its refresh token are saved in
`<config dir>/devmode-hub.json` (readable by you only) and renewed on their own.
Delete the file to start over.

Changing commands take the Hub's lease on the device and give it back when they
end. If another connection holds it, they fail with the Hub's message
(`LEASE_HELD`).

Differences from the other transports:

- `leave` returns to the committed revision only; `--rev` must name it.
- `--keep-owner` is refused; files are written as root.
- `status` cannot tell whether `PV_DEVMODE_ALLOWED` is on; `enter` fails with
  exit code 11 when it is not.
- `status`, `push` and `sync` cannot see which deleted-list paths devmode has already
  deleted (the Hub does not list them), so they show them as `to delete` each
  time; deleting them again is harmless.

## Workflow

```
pvr devmode enter bench              # device reboots into a devmode revision
pvr devmode pull bench nginx         # ./devmode/nginx/... from the device
$EDITOR devmode/nginx/etc/nginx/nginx.conf
pvr devmode status bench             # what differs
pvr devmode push bench nginx         # add and update files
pvr devmode apply bench nginx        # restart the container
pvr devmode push bench --watch       # push and apply on every save
pvr devmode rm bench nginx etc/nginx/conf.d/default.conf   # delete an image file
pvr devmode sync bench nginx         # make the container match ./devmode exactly
pvr devmode leave bench              # run the committed revision again
```

The local tree is `./devmode/<container>/<path>`; change it with `--dir`.
Backup and editor files (`*~`, `*.swp`, `.git`, ...) are ignored.

### The deleted list

`./devmode/<container>/.devmode-deleted` lists image paths to delete in the
container, one container path per line, without a leading slash. Blank lines and
`#` comments are allowed. The name is reserved: the file is never uploaded.

```
# the default site is replaced by my own
etc/nginx/conf.d/default.conf
usr/share/doc
```

`pvr devmode rm` adds paths to it, `pvr devmode pull` writes it from the files
devmode deleted on the device, and `push` and `sync` delete the listed paths.

## Commands

| Command | Runs on the device |
|---|---|
| `enter <device> [--base N] [--resume]` | `pvcontrol devmode create [N] [--resume]` |
| `status <device> [container...]` | `pvcontrol devmode status`, `pvcontrol devmode files ls <c>`, `pvcontrol containers ls` |
| `push <device> [container...] [--watch] [--yes] [--keep-owner] [--no-wait] [--timeout D]` | `pvcontrol devmode files put <c> <path> - --mode M --uid U --gid G` (file on stdin), `... put <c> <path> --type symlink --target T`, `... put <c> <path> --type dir --mode M`; then `pvcontrol devmode files rm <c> <path> --recursive` for deleted-list paths |
| `sync <device> [container...] [--no-apply] [--yes] [--keep-owner] [--no-wait] [--timeout D]` | per container `pvcontrol devmode reset <c>`, the uploads of `push`, `pvcontrol devmode files rm <c> <path> --recursive` for the deleted list; then the steps of `apply` |
| `pull <device> [container...]` | `pvcontrol devmode files ls <c>`, `pvcontrol devmode files get <c> <path>` |
| `apply <device> [container...] [--yes] [--no-wait] [--timeout D]` | `pvcontrol devmode apply <c>`, or `... --reboot` for a system container; `pvcontrol containers ls` every second to wait |
| `reset <device> <container...>` | `pvcontrol devmode reset <c>` |
| `rm <device> <container> <path...> [--recursive]` | `pvcontrol devmode files rm <c> <path> [--recursive]` |
| `leave <device> [--rev REV]` | `pvcontrol devmode status` (base), `pvcontrol cmd run <base>` |
| `exec <device> <container> [--timeout D] [--cwd DIR] [--env K=V] [--stdin] [--max-output N] -- <command> [args...]` | `pvcontrol containers exec --timeout N <c> -- <command> [args...]` |
| `logs <device> <container> [--tail N]` | `pvcontrol devmode status` for the revision, then `tail -n N /pantavisor/logs/<revision>/<container>/lxc/console.log` |
| `devices [--no-scan]` | local only |
| `add-device <alias> <target>` | local only |

Rules:

- `status` compares sha256 and mode of local files with the device manifest.
  `added`: only local. `changed`: content or mode differs. `only on device`: a
  file the local tree lacks; `sync` removes it, `push` leaves it. `to delete`: a
  deleted-list path the device has not deleted yet. It also shows what `apply`
  will do: `restart` for a container, `reboot` for a system container.
- `push` only adds and updates. Besides the deleted list it removes nothing, so
  image directories that devmode copied up as parents are never touched.
- `sync` makes the container exactly the image plus `./devmode/<container>/`
  minus the deleted list. It first checks the whole local tree of every selected
  container (paths, types, deleted list) and stops before touching the device if
  anything is wrong. It prints one line per container, for example
  `pv-example-app: emptying devmode files, uploading 12 files, deleting 1 image file`,
  then resets, uploads and deletes per container, and applies all containers
  once. `--no-apply` skips the apply. Without container names every container
  with a directory in `./devmode` is synced.
- `push` and `sync` keep the permission bits of the local file. The owner is root
  (0/0) because local owners rarely mean anything on the device; `--keep-owner`
  sends the local numeric uid/gid instead. Symlinks are pushed as symlinks and
  empty local directories as directories. File names can hold any character.
- `pull` creates symlinks and directories, writes the deleted list, and skips
  special files (device nodes, sockets, pipes) with a reason.
- `reset` empties the devmode files of the named containers on the device. The
  local tree and the deleted list stay as they are.
- `apply` returns when the container runs again. Before the request it waits
  until the container is `STARTED` (a restart takes about 11 s on an appengine,
  and a second apply during it would be refused). After the request it polls
  `pvcontrol containers ls` every second until the status is `STARTED` with a
  different pid, start time or restart count, and prints how long it took. The
  same wait applies to the apply step of `sync` and to `push --watch`.
  `--timeout` (default 90s) limits it; when the container does not come back the
  exit code is 15. `--no-wait` returns right after the request. A reboot is never
  waited for, because the connection drops.
- `apply` of a system container reboots the device. It asks first, or needs
  `--yes`. Without a terminal and without `--yes` it exits with 13.
- `push --watch` waits 400 ms after the last change, pushes, then applies the
  containers that changed. System containers only reboot the device with `--yes`.

## Run a command and read logs

```
pvr devmode exec bench nginx -- nginx -t
pvr devmode exec bench nginx --env DEBUG=1 --cwd /srv -- sh -c 'ls | wc -l'
echo hello | pvr devmode exec bench nginx --stdin -- tee /tmp/hello
pvr devmode logs bench nginx --tail 50
```

`exec` runs the command without a shell (write `sh -c '...'` to use one). The
device must run a devmode revision. The command's output goes to your terminal
and pvr exits with its exit code: 124 after `--timeout` (30s by default), 128
plus the signal number when a signal ended it. With `--json` the output is in
the result. The Hub limits the timeout to 45 seconds. Over pvcontrol a command
that exits with 255 after printing a pv-ctrl error is reported as that error.

`logs` prints the last `--tail` lines (200 by default) of the container's console
log in the running devmode revision. The device must run a devmode revision.
Every transport reads the same file, `lxc/console.log` of the container:

- pvcontrol runs `tail -n N /pantavisor/logs/<revision>/<container>/lxc/console.log`
  in the management container. `<revision>` is the devmode revision from
  `pvr devmode status` (for example `devmode/r/0`), not the committed one. A
  missing file is an error that names the path.
- http reads `/cgi-bin/logs?rev=<revision>&source=<container>/lxc/console.log&tail=1&n=N`
  of the pvr-sdk container.
- Hub calls the `logs` tool.

## What each transport calls

pvcontrol: the commands in the table above, and `status` plus `tail` for `logs`.

http, all under `/cgi-bin/pvctrl`, answering with pv-ctrl's status and body:

| pvr | Call |
|---|---|
| `status` and every check for a devmode revision | `GET /devmode` |
| `enter` | `POST /devmode` with `{"base":"N","resume":false}` |
| `leave` | `POST /commands` with `{"op":"LOCAL_RUN","payload":"<revision>"}` |
| `status`, `push`, `sync`, `pull` (device file list) | `GET /devmode/files/<container>` |
| `pull` | `GET /devmode/files/<container>/<path>` |
| `push`, `sync` | `PUT /devmode/files/<container>/<path>?mode=M&uid=U&gid=G` (file body), with `type=dir`, or `type=symlink&target=T` and no body |
| `rm`, `push`, `sync` (deleted list) | `DELETE /devmode/files/<container>/<path>[?recursive=1]` |
| `reset`, `sync` | `DELETE /devmode/containers/<container>` |
| `apply`, `push --watch` | `PUT /containers/<container>[?reboot=1]` with `{"action":"restart"}` |
| `apply` (restart policies and waiting for the container) | `GET /containers` |
| `exec` | `POST /containers/<container>/exec` |
| `logs` | `GET /cgi-bin/logs?rev=<revision>&source=<container>/lxc/console.log&tail=1&n=N` (not under `/cgi-bin/pvctrl`) |

Hub, the tools of `<hub>/mcp`:

| pvr | Tools |
|---|---|
| `status` | `get_devmode_context`, `container_status` |
| `enter` | `lease`, `dev_begin` |
| `leave` | `get_devmode_context`, `lease`, `dev_end` |
| `status`, `push`, `sync`, `pull` (device file list) | `ls` (recursive, entries from the devmode changes only), `hash` |
| `pull` | `get_file` (in parts, or a download link for large files) |
| `push`, `sync` | `lease`, `apply_changes` with `put`, `mkdir`, `symlink` ops; files over 256 KiB go through `get_object_upload_link` and a `source` |
| `rm`, deleted list | `lease`, `apply_changes` with `rm` |
| `reset` | `lease`, `reset_changes` |
| `apply` | `lease`, `container_ctl` (restart), `container_status` to wait |
| `exec` | `lease`, `exec` |
| `logs` | `logs` |

## JSON and exit codes

Every command takes `--json` and prints one document:

```
{"schema":1,"command":"push","ok":true,"result":{...}}
{"schema":1,"command":"push","ok":false,"error":{"kind":"not_active","message":"..."},"exit_code":12}
```

`push --watch --json` prints one document per cycle. The `apply` result lists
per container `{"name", "state", "seconds"}`: `state` is `STARTED`,
`restart_requested` (with `--no-wait`) or `rebooting`.

| Exit code | Kind | Meaning |
|---|---|---|
| 0 | | ok |
| 1 | `error` | other failure |
| 2 | `usage` | wrong arguments |
| 10 | `unreachable` | no connection to the device |
| 11 | `not_allowed` | devmode is off on the device, or the caller is not a management container |
| 12 | `not_active` | the device does not run a devmode revision |
| 13 | `reboot_needed` | apply needs a reboot and was not confirmed |
| 14 | `apply_failed` | the device refused the apply |
| 15 | `wait_timeout` | a restarted container was not `STARTED` again within `--timeout` |
| 16 | `not_available` | this transport cannot do it (no command uses it now) |

`exec` is the exception: it exits with the exit code of the command, so a
command that exits with 10 looks like an unreachable device. Use `--json` to
tell them apart.
