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
only while the debug shell is on. The pvr-sdk container ships it listening on
`0.0.0.0`, so anyone on the device's network can reach it; set it to `127.0.0.1`
on the device and use an ssh tunnel to keep it on the device. There is no login. The calls pvr makes are listed in [What each transport calls](#what-each-transport-calls).

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
- `--keep-owner` works: the ops of `apply_changes` carry `uid` and `gid`. A Hub
  from before that change refuses them; files owned by root are then sent
  without an owner, and `--keep-owner` fails with a message saying so.
- `status` reads `devmode_allowed` from `get_devmode_context`: `false` shows
  devmode as disabled. When the Hub gives `null` or nothing, `status` reports it
  as enabled and `enter` fails with exit code 11 when it is not.
- `status`, `push` and `sync` ask `ls` for deleted image paths (`include_deleted`),
  so a path devmode already deleted is not deleted again. A Hub that does not
  know the argument lists none, and they show every deleted-list path as
  `to delete` each time; deleting it again is harmless.
- `exec --timeout` is at most 45 seconds; the Hub refuses more.

## Workflow

`pull` is a checkout. It copies the files of a container (the image plus what
devmode changed) into `./devmode/<container>/`. Edit, add or delete anything
there; `push` sends exactly what you changed.

```
pvr devmode enter bench              # device reboots into a devmode revision
pvr devmode pull bench nginx         # check out the whole container into ./devmode/nginx/
$EDITOR devmode/nginx/etc/nginx/nginx.conf
rm devmode/nginx/etc/nginx/conf.d/default.conf
pvr devmode status bench nginx       # what differs from the checkout
pvr devmode push bench nginx         # send the edited, new and deleted files; nginx restarts
pvr devmode push bench nginx --no-restart   # send them, restart later
pvr devmode apply bench nginx        # restart the container (also: reboot for a system container, with --yes)
pvr devmode push bench --watch       # push and apply on every save
pvr devmode sync bench nginx         # make the container match ./devmode exactly
pvr devmode leave bench              # run the committed revision again
```

Example session:

```
$ pvr devmode pull bench nginx
nginx: 1834 written, 0 unchanged, 0 removed, 0 kept (changed here), 0 conflicts, 0 skipped
$ echo "# tuned" >> devmode/nginx/etc/nginx/nginx.conf
$ rm devmode/nginx/etc/nginx/conf.d/default.conf
$ pvr devmode status bench nginx
devmode: active, revision devmode/r/1, base 1
nginx (apply: restart)
  changed  etc/nginx/nginx.conf (content)
  deleted  etc/nginx/conf.d/default.conf (push deletes it on the device)
$ pvr devmode push bench nginx
nginx: 1 pushed, 1 image path deleted, 1832 unchanged
nginx: restarted
$ pvr devmode exec bench nginx -- nginx -t     # sees the change
```

`push`, `rm`, `sync` and `reset` restart each changed app container by default,
on every transport, so an `exec` right after sees the change. A system container
is not restarted by default, because that reboots the device. pvr says so in one
line and you restart it yourself:

```
$ pvr devmode push bench pv-sys
pv-sys: 1 pushed, 0 unchanged
pv-sys: not restarted. is a system container: the change shows after it restarts, which reboots the device; restart it with pvr devmode apply <device> pv-sys --yes
$ pvr devmode apply bench pv-sys --yes
```

Add `--no-restart` to keep the files without a restart, for example to push to
several containers and restart them together with `pvr devmode apply`.

The local tree is `./devmode/<container>/<path>`; change it with `--dir`.
Backup and editor files (`*~`, `*.swp`, `.git`, ...) are ignored.

### Work on one file

Name the files you want after `--`. Only those are checked out:

```
pvr devmode pull bench nginx -- etc/nginx/nginx.conf
$EDITOR devmode/nginx/etc/nginx/nginx.conf
pvr devmode push bench nginx          # sends only that file
pvr devmode pull bench nginx -- usr/share/nginx/html/index.html   # adds a second file
```

A path can be a file or a folder. The folders above a single file are created
locally but are not part of the checkout, and a partial checkout never treats
the files outside it as deleted. Each pull adds to the checkout, so the second
`pull` above keeps the first file. `push` sends an edited file with the owner
and mode the device had for it.

A `pull` without paths later refreshes only these files, not the whole
container. To check out the whole container from there, use `--all`:

```
pvr devmode pull bench nginx          # refreshes the two files above
pvr devmode pull bench nginx --all    # the whole container
```

### Pull again

`pull` again refreshes the checkout without losing your edits. Without paths it
refreshes what the earlier pulls checked out: the whole container after a
whole-container pull (or `--all`), otherwise only the paths given before. The
first pull of a container, with no `.devmode-index` yet, is always the whole
container.

- a file you did not change is updated to the device version, and a file gone
  from the device is removed here;
- a file you changed and the device did not is left as it is;
- a file changed here and on the device is a conflict: it is listed, left alone,
  and `pull --force` takes the device version (it also removes files you changed
  that are gone from the device).

```
$ pvr devmode pull bench nginx
nginx: 0 written, 1830 unchanged, 0 removed, 1 kept (changed here), 1 conflicts, 0 skipped
  conflict etc/nginx/nginx.conf: changed here and on the device
  left alone; pull again with --force to take the device version
$ pvr devmode pull bench nginx --force
```

### The checkout index

`./devmode/<container>/.devmode-index` records what `pull` checked out, as JSON
(never pushed). `status` and `push` compare the folder with it, so they need no
call to the device.

```
{
 "version": 1,
 "roots": ["."],
 "files": [
  {"path": "etc/nginx/nginx.conf", "type": "file", "mode": "0644", "uid": 33, "gid": 33,
   "size": 10, "sha256": "...", "target": ""},
  {"path": "usr/bin/sh", "type": "symlink", "mode": "0777", "uid": 0, "gid": 0, "target": "run.sh"}
 ],
 "deleted": ["usr/share/doc"]
}
```

`type` is `file`, `dir` or `symlink`; `mode` is octal with setuid, setgid and
sticky bits; `roots` are the paths that were pulled (`"."` is the whole
container; an index without `roots` counts as whole); `deleted` lists deleted-list paths already deleted on the device.
`push` sends files whose content, mode or symlink target differ from the index,
new files, and deletes for indexed paths missing here. It updates the index
afterwards. Do not edit the index by hand; delete it to make the folder an
unindexed one that is compared with the device instead.

pvr unpacks the tar safely: it refuses absolute names, `..` and names that pass
through a symlink, never writes through a symlink, and lists every entry it did
not write. Modes are kept; owners are set only when pvr runs as root, otherwise
they are only recorded in the index.

### A device without tar support

A Pantavisor from before the tar format (or a Hub agent that answers
`PANTAVISOR_TOO_OLD`) cannot send a folder in one step. `pull` then prints a
one-line notice and pulls file by file: the files devmode changed, with the
deleted list, and no index. `status` and `push` compare such a folder with the
device as before.

### The deleted list

`./devmode/<container>/.devmode-deleted` lists image paths to delete in the
container, one container path per line, without a leading slash. Blank lines and
`#` comments are allowed. The name is reserved: the file is never uploaded.

```
# the default site is replaced by my own
etc/nginx/conf.d/default.conf
usr/share/doc
```

`pvr devmode rm` adds paths to it, and `push` and `sync` delete the listed paths.
A pull from a device without tar support also writes it from the files devmode
deleted on the device. Deleting a file in a checkout needs no list: `push`
deletes it because it is gone from the folder.

## Commands

| Command | Runs on the device |
|---|---|
| `enter <device> [--base N] [--resume]` | `pvcontrol devmode create [N] [--resume]` |
| `status <device> [container...]` | `pvcontrol devmode status`, `pvcontrol containers ls`; for a folder without an index also `pvcontrol devmode files ls <c>` |
| `push <device> [container...] [--watch] [--no-restart] [--yes] [--keep-owner] [--no-wait] [--timeout D]` | `pvcontrol devmode files put <c> <path> - --mode M --uid U --gid G` (file on stdin), `... put <c> <path> --type symlink --target T`, `... put <c> <path> --type dir --mode M`; then `pvcontrol devmode files rm <c> <path> --recursive` for deleted-list paths; then `pvcontrol devmode apply <c>` for each changed app container |
| `sync <device> [container...] [--no-restart] [--yes] [--keep-owner] [--no-wait] [--timeout D]` | per container `pvcontrol devmode reset <c>`, the uploads of `push`, `pvcontrol devmode files rm <c> <path> --recursive` for the deleted list; then the restart of each app container (the steps of `apply`) |
| `pull <device> [container...] [--force] [-- path...]` | `pvcontrol devmode files get <c> [path] --tar`; on a device without tar `pvcontrol devmode files ls <c>` and `... get <c> <path>` |
| `apply <device> [container...] [--yes] [--no-wait] [--timeout D]` | `pvcontrol devmode apply <c>`, or `... --reboot` for a system container; `pvcontrol containers ls` every second to wait |
| `reset <device> <container...> [--no-restart] [--no-wait] [--timeout D]` | `pvcontrol devmode reset <c>`, then `pvcontrol devmode apply <c>` for an app container |
| `rm <device> <container> <path...> [--recursive] [--no-restart] [--no-wait] [--timeout D]` | `pvcontrol devmode files rm <c> <path> [--recursive]`, then `pvcontrol devmode apply <c>` for an app container |
| `leave <device> [--rev REV]` | `pvcontrol devmode status` (base), `pvcontrol cmd run <base>` |
| `exec <device> <container> [--timeout D] [--cwd DIR] [--env K=V] [--stdin] [--max-output N] -- <command> [args...]` | `pvcontrol containers exec --timeout N <c> -- <command> [args...]` |
| `logs <device> <container> [--tail N]` | `pvcontrol devmode status` for the revision, then `tail -n N /pantavisor/logs/<revision>/<container>/lxc/console.log` |
| `devices [--no-scan]` | local only |
| `add-device <alias> <target>` | local only |

Rules:

- `status` of a checkout compares the folder with `.devmode-index`. `added`: a
  new file. `changed`: content, mode, type or symlink target differs. `deleted`:
  a checked-out path gone from the folder; `push` deletes it. `to delete`: a
  deleted-list path the device has not deleted yet. For a folder without an index
  it compares sha256 and mode with the device manifest, and `only on device`
  lists a file the folder lacks (`sync` removes it, `push` leaves it). It also
  shows what `apply` will do: `restart` for a container, `reboot` for a system
  container.
- `push` of a checkout sends the changes described above. For a folder without an
  index it adds and updates only, besides the deleted list, so image directories
  that devmode copied up as parents are never touched.
- `sync` makes the container exactly the image plus `./devmode/<container>/`
  minus the deleted list. It first checks the whole local tree of every selected
  container (paths, types, deleted list) and stops before touching the device if
  anything is wrong. It prints one line per container, for example
  `pv-example-app: emptying devmode files, uploading 12 files, deleting 1 image file`,
  then resets, uploads and deletes per container, and applies all containers
  once its files are done restarts each app container. For a checkout it also
  deletes the checked-out paths gone from the folder and rewrites the index.
  `--no-restart` (or `--no-apply`) skips the restarts. A system container is not
  restarted unless you pass `--yes`, which reboots the device for it. Without
  container names every container with a directory in `./devmode` is synced.
- `push` and `sync` keep the permission bits of the local file. An edited file
  of a checkout keeps the uid/gid the index recorded. A new file is owned by root
  (0/0) because local owners rarely mean anything on the device; `--keep-owner`
  sends the local numeric uid/gid instead. Symlinks are pushed as symlinks and
  empty local directories as directories. File names can hold any character.
- `pull` creates symlinks and directories and skips special files (device nodes,
  sockets, pipes) and unsafe names with a reason. Conflicts are listed and leave
  the exit code at 0.
- `reset` empties the devmode files of the named containers on the device and
  restarts the app containers. The local tree and the deleted list stay as they
  are.
- `rm` deletes the path on the device, adds it to the deleted list and removes
  the local copy under `./devmode/<container>/` (and its checkout index entry), so
  the tree, the list and the device agree. A later `sync` still refuses a path
  that is both in the folder and on the deleted list.
- Restart after a change: `push`, `rm`, `sync` and `reset` restart the changed
  app containers and wait for them as `apply` does (`--timeout`, `--no-wait`). A
  container is an app container or a system container by its restart policy, the
  same `apply: restart` / `apply: reboot` that `status` shows. A system container
  is not restarted; the note says to run `pvr devmode apply <device> <container> --yes`.
  `--no-restart` restarts nothing. `push --watch` restarts through its own apply
  step, never twice.
- `apply` returns when the container runs again. Before the request it waits
  until the container is `STARTED` (a restart takes about 11 s on an appengine,
  and a second apply during it would be refused). After the request it polls
  `pvcontrol containers ls` every second until the status is `STARTED` with a
  different pid, start time or restart count, and prints how long it took. The
  same wait applies to the restart after `push`, `rm`, `sync` and `reset` and to `push --watch`.
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
the result. Over the Hub the timeout is at most 45 seconds. Over pvcontrol a command
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
| `status`, `push`, `sync` (folder without an index), `pull` (device without tar) | `GET /devmode/files/<container>` |
| `pull` | `GET /devmode/files/<container>[/<path>]?format=tar` (a 400 or an answer that is not `application/x-tar` means no tar support) |
| `pull` (device without tar) | `GET /devmode/files/<container>/<path>` |
| `push`, `sync` | `PUT /devmode/files/<container>/<path>?mode=M&uid=U&gid=G` (file body), with `type=dir`, or `type=symlink&target=T` and no body |
| `rm`, `push`, `sync` (deleted list) | `DELETE /devmode/files/<container>/<path>[?recursive=1]` |
| `reset`, `sync` | `DELETE /devmode/files/<container>` (no path) |
| `apply`, `push --watch`, the restart after `push`, `rm`, `sync`, `reset` | `PUT /containers/<container>[?reboot=1]` with `{"action":"restart"}` |
| `apply` (restart policies and waiting for the container) | `GET /containers` |
| `exec` | `POST /containers/<container>/exec` |
| `logs` | `GET /cgi-bin/logs?rev=<revision>&source=<container>/lxc/console.log&tail=1&n=N` (not under `/cgi-bin/pvctrl`) |

Hub, the tools of `<hub>/mcp`:

| pvr | Tools |
|---|---|
| `status` | `get_devmode_context` (`devmode_allowed`), `container_status` |
| `enter` | `lease`, `dev_begin` |
| `leave` | `get_devmode_context`, `lease`, `dev_end` |
| `status`, `push`, `sync` (folder without an index), `pull` (device without tar) | `ls` (recursive, with `include_deleted`; entries from the devmode changes and deleted image paths), `hash` |
| `pull` | `get_tree` (a download link, after `pending` answers); `PANTAVISOR_TOO_OLD` or an unknown tool means no tar support |
| `pull` (device without tar) | `get_file` (in parts, or a download link for large files) |
| `push`, `sync` | `lease`, `apply_changes` (the restart is left to the device on the last call of a command, `restart:false` on the others and with `--no-restart`) with `put`, `mkdir`, `symlink` ops (with `uid` and `gid`); files over 256 KiB go through `get_object_upload_link` and a `source` |
| `rm`, deleted list | `lease`, `apply_changes` with `rm` |
| `reset` | `lease`, `reset_changes` (answers carry `restarted` and `restart_note`) |
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

The results of `push`, `rm` and `sync` carry `restarted` per container, and
`restart_note` when a system container was not restarted. `reset` lists the same
under `restarts`. Over the Hub the values come from the answers of the device
agent; an agent that does not report them is followed by a restart through
`container_ctl`.

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
