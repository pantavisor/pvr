# pvr devmode

Edit files on your workstation and sync them into the containers of a device
that runs a Pantavisor devmode revision, like `adb sync`. Nothing extra runs on
the device: pvr starts a shell on it and calls `pvcontrol`.

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

## Workflow

```
pvr devmode enter bench              # device reboots into a devmode revision
pvr devmode pull bench nginx         # ./devmode/nginx/... from the device
$EDITOR devmode/nginx/etc/nginx/nginx.conf
pvr devmode status bench             # what differs
pvr devmode push bench nginx         # upload changed files
pvr devmode apply bench nginx        # restart the container
pvr devmode push bench --watch       # push and apply on every save
pvr devmode leave bench              # run the committed revision again
```

The local tree is `./devmode/<container>/<path>`; change it with `--dir`.
Backup and editor files (`*~`, `*.swp`, `.git`, ...) are ignored.

## Commands

| Command | Runs on the device |
|---|---|
| `enter <device> [--base N] [--resume]` | `pvcontrol devmode create [N] [--resume]` |
| `status <device> [container...]` | `pvcontrol devmode status`, `pvcontrol devmode files ls <c>`, `pvcontrol containers ls` |
| `push <device> [container...] [--delete] [--watch] [--yes] [--keep-owner]` | `pvcontrol devmode files put <c> <path> - --mode M --uid U --gid G` (file on stdin); with `--delete` `pvcontrol devmode files rm <c> <path>` |
| `pull <device> [container...]` | `pvcontrol devmode files ls <c>`, `pvcontrol devmode files get <c> <path>` |
| `apply <device> [container...] [--yes]` | `pvcontrol devmode apply <c>`, or `... --reboot` for a system container |
| `reset <device> <container>` | `pvcontrol devmode reset <c>` |
| `leave <device> [--rev REV]` | `pvcontrol devmode status` (base), `pvcontrol cmd run <base>` |
| `devices [--no-scan]` | local only |
| `add-device <alias> <target>` | local only |

Rules:

- `status` compares sha256 and mode of local files with the device manifest.
  `added`: only local. `changed`: content or mode differs. `deleted`: only on the
  device; only `push --delete` removes it. It also shows what `apply` will do:
  `restart` for a container, `reboot` for a system container.
- `push` keeps the permission bits of the local file. The owner is root (0/0)
  because local owners rarely mean anything on the device; `--keep-owner` sends
  the local numeric uid/gid instead. Symlinks are skipped for now.
- `apply` of a system container reboots the device. It asks first, or needs
  `--yes`. Without a terminal and without `--yes` it exits with 13.
- `push --watch` waits 400 ms after the last change, pushes, then applies the
  containers that changed. System containers only reboot the device with `--yes`.

## JSON and exit codes

Every command takes `--json` and prints one document:

```
{"schema":1,"command":"push","ok":true,"result":{...}}
{"schema":1,"command":"push","ok":false,"error":{"kind":"not_active","message":"..."},"exit_code":12}
```

`push --watch --json` prints one document per cycle.

| Exit code | Kind | Meaning |
|---|---|---|
| 0 | | ok |
| 1 | `error` | other failure |
| 2 | `usage` | wrong arguments |
| 10 | `unreachable` | no connection to the device |
| 11 | `not_allowed` | devmode is off on the device |
| 12 | `not_active` | the device does not run a devmode revision |
| 13 | `reboot_needed` | apply needs a reboot and was not confirmed |
| 14 | `apply_failed` | the device refused the apply |
