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
| `push <device> [container...] [--watch] [--yes] [--keep-owner]` | `pvcontrol devmode files put <c> <path> - --mode M --uid U --gid G` (file on stdin), `... put <c> <path> --type symlink --target T`, `... put <c> <path> --type dir --mode M`; then `pvcontrol devmode files rm <c> <path> --recursive` for deleted-list paths |
| `sync <device> [container...] [--no-apply] [--yes] [--keep-owner]` | per container `pvcontrol devmode reset <c>`, the uploads of `push`, `pvcontrol devmode files rm <c> <path> --recursive` for the deleted list; then the steps of `apply` |
| `pull <device> [container...]` | `pvcontrol devmode files ls <c>`, `pvcontrol devmode files get <c> <path>` |
| `apply <device> [container...] [--yes]` | `pvcontrol devmode apply <c>`, or `... --reboot` for a system container |
| `reset <device> <container...>` | `pvcontrol devmode reset <c>` |
| `rm <device> <container> <path...> [--recursive]` | `pvcontrol devmode files rm <c> <path> [--recursive]` |
| `leave <device> [--rev REV]` | `pvcontrol devmode status` (base), `pvcontrol cmd run <base>` |
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
| 11 | `not_allowed` | devmode is off on the device, or the caller is not a management container |
| 12 | `not_active` | the device does not run a devmode revision |
| 13 | `reboot_needed` | apply needs a reboot and was not confirmed |
| 14 | `apply_failed` | the device refused the apply |
