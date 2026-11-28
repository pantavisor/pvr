//
// Copyright 2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package devmode

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/pantavisor/pvr/libpvr"
	"github.com/urfave/cli"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, " ") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// options holds every flag any devmode command takes.
type options struct {
	json, yes, watch, keepOwner, force, all, resume, noScan, noApply, noRestart, recursive, noWait bool
	exec, dir, base, rev                                                                           string
	sshOpts, envVars                                                                               multi
	scanTimeout, timeout                                                                           time.Duration
	stdin                                                                                          bool
	maxOutput, tail                                                                                int
	cwd                                                                                            string
	// rest is what follows -- on the command line (the exec command)
	rest []string
}

type spec struct {
	name, args, usage, desc string
	// device is true when the first argument names the device.
	device   bool
	minPos   int // positional arguments after the device
	maxPos   int // -1 for any number
	flags    func(fs *flag.FlagSet, o *options)
	run      func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error)
	noDevice bool // does not need a transport
	// rest is true when a literal -- ends the options and the rest is a command.
	rest bool
	// restOptional lets the -- part be empty.
	restOptional bool
}

func commonFlags(fs *flag.FlagSet, o *options) {
	fs.BoolVar(&o.json, "json", false, "print one JSON document (schema 1) instead of text")
}

func transportFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.exec, "exec", "", "run commands through this prefix instead of ssh, e.g. 'docker exec -i ae' (then no <device> argument)")
	fs.Var(&o.sshOpts, "ssh-opt", "extra ssh argument, repeatable, e.g. --ssh-opt=-oIdentityFile=key")
}

func waitFlags(fs *flag.FlagSet, o *options) {
	fs.BoolVar(&o.noWait, "no-wait", false, "return right after the restart request, do not wait for the container")
	fs.DurationVar(&o.timeout, "timeout", 90*time.Second, "how long to wait for a restarted container (exit code 15 when it does not come back)")
}

func restartFlag(fs *flag.FlagSet, o *options) {
	fs.BoolVar(&o.noRestart, "no-restart", false, "do not restart the changed containers; run 'pvr devmode apply' later")
}

func dirFlag(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.dir, "dir", DefaultDir, "local tree: <dir>/<container>/<path>")
}

func configDir(c *cli.Context) string {
	if s, ok := c.App.Metadata["PVR_CONFIG_DIR"].(string); ok && s != "" {
		return s
	}
	if s := c.GlobalString("config-dir"); s != "" {
		return s
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".pvr")
}

func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// resolve turns the device argument into a backend. The returned function
// closes it.
func resolve(c *cli.Context, o *options, device string) (Backend, func() error, error) {
	if o.exec != "" {
		return pvcontrolBackend(NewExecTransport(o.exec))
	}
	target := device
	if a, ok, err := NewAliasStore(configDir(c)).Get(device); err != nil {
		return nil, nil, err
	} else if ok {
		if a.Exec != "" {
			return pvcontrolBackend(NewExecTransport(a.Exec))
		}
		target = a.Target
	}
	switch {
	case strings.HasPrefix(target, "hub:"):
		id, err := ParseHubTarget(target)
		if err != nil {
			return nil, nil, errf(KindUsage, err.Error())
		}
		base, _ := c.App.Metadata["PVR_BASEURL"].(string)
		if base == "" {
			base = "https://api.pantahub.com"
		}
		h := NewHubBackend(id, base, newHubTokenStore(configDir(c)), pvrLoginToken(c, base))
		return h, h.Close, nil
	case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"):
		h, err := NewHTTPBackend(target)
		if err != nil {
			return nil, nil, errf(KindUsage, err.Error())
		}
		return h, h.Close, nil
	}
	t, err := ParseTarget(target)
	if err != nil {
		return nil, nil, errf(KindUsage, err.Error())
	}
	return pvcontrolBackend(NewSSHTransport(t, o.sshOpts), nil)
}

// pvcontrolBackend wraps a shell transport.
func pvcontrolBackend(t Transport, err error) (Backend, func() error, error) {
	if err != nil {
		return nil, nil, err
	}
	return &Client{T: t}, t.Close, nil
}

func (s spec) command() cli.Command {
	usage := "pvr devmode " + s.name + " " + s.args
	return cli.Command{
		Name:            s.name,
		Usage:           s.usage,
		ArgsUsage:       s.args,
		Description:     s.desc,
		SkipFlagParsing: true,
		Action: func(c *cli.Context) error {
			o := &options{dir: DefaultDir}
			fs := flag.NewFlagSet(s.name, flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			commonFlags(fs, o)
			if s.flags != nil {
				s.flags(fs, o)
			}
			args := []string(c.Args())
			if s.rest {
				for i, a := range args {
					if a == "--" {
						args, o.rest = args[:i], args[i+1:]
						break
					}
				}
				if len(o.rest) == 0 && !s.restOptional {
					return s.fail(o, errf(KindUsage, "missing command after --\nusage: "+usage))
				}
			}
			pos, err := parseArgs(fs, args)
			if err == flag.ErrHelp {
				return cli.ShowCommandHelp(c, s.name)
			}
			if err != nil {
				return s.fail(o, errf(KindUsage, err.Error()+"\nusage: "+usage))
			}
			device := ""
			if s.device && o.exec == "" {
				if len(pos) == 0 {
					return s.fail(o, errf(KindUsage, "missing <device>\nusage: "+usage))
				}
				device, pos = pos[0], pos[1:]
			}
			if len(pos) < s.minPos || (s.maxPos >= 0 && len(pos) > s.maxPos) {
				return s.fail(o, errf(KindUsage, "wrong number of arguments\nusage: "+usage))
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			env := &Env{Dir: o.dir, JSON: o.json, Yes: o.yes, KeepOwner: o.keepOwner, NoWait: o.noWait, NoRestart: o.noRestart, WaitTimeout: o.timeout,
				Out: os.Stdout, Err: os.Stderr, In: os.Stdin}
			if s.device {
				b, closeBackend, err := resolve(c, o, device)
				if err != nil {
					return s.fail(o, err)
				}
				defer closeBackend()
				env.C = b
			}
			res, human, err := s.run(ctx, env, o, c, pos)
			if err != nil {
				return s.fail(o, err)
			}
			if res != nil {
				env.emit(s.name, res, human)
			}
			if env.ExitCode != 0 {
				return cli.NewExitError("", env.ExitCode)
			}
			return nil
		},
	}
}

func (s spec) fail(o *options, err error) error {
	var e *Error
	if !errors.As(err, &e) {
		e = errf(KindGeneric, err.Error())
	}
	if o.json {
		EmitError(os.Stdout, s.name, e, true)
		return cli.NewExitError("", e.Kind.ExitCode())
	}
	return cli.NewExitError("pvr devmode "+s.name+": "+e.Msg, e.Kind.ExitCode())
}

func dev(s spec) spec { s.device = true; return s }

func joinNames(n []string) string { return strings.Join(n, ", ") }

// CommandDevmode : pvr devmode command
func CommandDevmode() cli.Command {
	specs := []spec{
		dev(spec{
			name: "enter", args: "<device> [--base N] [--resume]", maxPos: 0,
			usage: "Create and run a devmode revision; the device reboots",
			desc: "Runs 'pvcontrol devmode create [N] [--resume]' on the device.\n" +
				"Without --base the committed revision is the base. The device reboots into the devmode revision.",
			flags: func(fs *flag.FlagSet, o *options) {
				transportFlags(fs, o)
				fs.StringVar(&o.base, "base", "", "revision number or locals/<name> to base devmode on (default: committed revision)")
				fs.BoolVar(&o.resume, "resume", false, "keep a left-over devmode revision of that base")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				r, err := env.C.Enter(ctx, o.base, o.resume)
				if err != nil {
					return nil, nil, err
				}
				return r, func(w io.Writer) {
					fmt.Fprintf(w, "devmode revision %s (base %s)\n", r.Revision, r.Base)
					if r.Reboot {
						fmt.Fprintln(w, "the device reboots now; wait for it, then run pvr devmode status")
					}
				}, nil
			},
		}),
		dev(spec{
			name: "status", args: "<device> [container...]", maxPos: -1,
			usage: "Show devmode state and the local-versus-device difference per container",
			desc: "Runs 'pvcontrol devmode status' and 'pvcontrol containers ls'. A folder made by pull is compared with its\n" +
				".devmode-index without asking the device; any other folder with 'pvcontrol devmode files ls <container>'.\n" +
				"added: only local. changed: content, mode or symlink target differs. deleted: checked out by pull, gone here (push deletes it).\n" +
				"only on device: a file the local tree lacks (sync removes it, push leaves it).\n" +
				"to delete: a path on ./devmode/<container>/.devmode-deleted that the device has not deleted yet (push and sync delete it).\n" +
				"apply: restart for a container, reboot for a system container.",
			flags: func(fs *flag.FlagSet, o *options) { transportFlags(fs, o); dirFlag(fs, o) },
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				r, err := env.Status(ctx, pos)
				if err != nil {
					return nil, nil, err
				}
				return r, func(w io.Writer) { env.printStatus(w, r) }, nil
			},
		}),
		dev(spec{
			name: "push", args: "<device> [container...] [--watch] [--no-restart] [--yes] [--keep-owner] [--no-wait] [--timeout D]", maxPos: -1,
			usage: "Add and update files from ./devmode/<container>/ on the device",
			desc: "For a folder made by 'pvr devmode pull' (it has a .devmode-index) push sends the files whose content, mode or symlink target differ\n" +
				"from the index and the new ones, keeps the uid/gid the index recorded for edited files, and deletes the checked-out paths that are gone.\n" +
				"Any other folder: uploads every file, symlink and empty directory of ./devmode/<container>/ that is new or different:\n" +
				"'pvcontrol devmode files put <container> <path> - --mode M --uid U --gid G' (file on stdin),\n" +
				"'... put <container> <path> --type symlink --target T' and '... put <container> <path> --type dir --mode M'.\n" +
				"Then it deletes the image paths on ./devmode/<container>/.devmode-deleted that are not deleted yet:\n" +
				"'pvcontrol devmode files rm <container> <path> --recursive'.\n" +
				"Push removes nothing else; use 'pvr devmode sync' to make the container match the local tree exactly.\n" +
				"Files keep their permission bits. The owner is root (0/0) unless --keep-owner uses the local numeric uid/gid.\n" +
				"--watch pushes and applies again whenever the tree changes (400ms quiet time); --yes lets it reboot for system containers.\n" +
				"With --watch each restart is waited for as in 'pvr devmode apply' (--timeout, --no-wait).\n" +
				"Afterwards each changed app container is restarted, so a following 'pvr devmode exec' sees the change; the restart is waited for\n" +
				"as in 'pvr devmode apply' (--timeout, --no-wait). A system container is not restarted (that is a device reboot): run\n" +
				"'pvr devmode apply <device> <container> --yes'. --no-restart restarts nothing.\n" +
				"Without container names every directory in ./devmode is pushed.",
			flags: func(fs *flag.FlagSet, o *options) {
				transportFlags(fs, o)
				waitFlags(fs, o)
				restartFlag(fs, o)
				dirFlag(fs, o)
				fs.BoolVar(&o.watch, "watch", false, "keep running: push and apply on every change")
				fs.BoolVar(&o.yes, "yes", false, "allow a device reboot in watch mode")
				fs.BoolVar(&o.keepOwner, "keep-owner", false, "use the local uid/gid instead of 0/0")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				if o.watch {
					return nil, nil, env.Watch(ctx, pos)
				}
				r, err := env.Push(ctx, pos)
				if err != nil {
					return nil, nil, err
				}
				return map[string]interface{}{"containers": r}, func(w io.Writer) {
					env.printPush(w, r)
				}, nil
			},
		}),
		dev(spec{
			name: "sync", args: "<device> [container...] [--no-restart] [--yes] [--keep-owner] [--no-wait] [--timeout D]", maxPos: -1,
			usage: "Make a container exactly the image plus ./devmode/<container>/ minus the deleted list",
			desc: "Checks the whole local tree of every selected container first (paths, file types, deleted list)\n" +
				"and stops before touching the device if anything is wrong. Then it prints one line per container,\n" +
				"for example 'pv-example-app: emptying devmode files, uploading 12 files, deleting 1 image file', and for each container runs:\n" +
				"'pvcontrol devmode reset <container>', the uploads of 'pvr devmode push', and 'pvcontrol devmode files rm <container> <path> --recursive'\n" +
				"for each path on ./devmode/<container>/.devmode-deleted.\n" +
				"Then each app container is restarted, like 'pvr devmode apply'. A system container is not restarted (that is a device reboot);\n" +
				"--yes reboots the device for it, otherwise run 'pvr devmode apply <device> <container> --yes'.\n" +
				"--no-restart (or --no-apply) skips the restarts.\n" +
				"Restarts are waited for as in 'pvr devmode apply' (--timeout, --no-wait).\n" +
				"Without container names every container that has a directory in ./devmode is synced.",
			flags: func(fs *flag.FlagSet, o *options) {
				transportFlags(fs, o)
				waitFlags(fs, o)
				dirFlag(fs, o)
				restartFlag(fs, o)
				fs.BoolVar(&o.noApply, "no-apply", false, "same as --no-restart")
				fs.BoolVar(&o.yes, "yes", false, "allow a device reboot")
				fs.BoolVar(&o.keepOwner, "keep-owner", false, "use the local uid/gid instead of 0/0")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				r, err := env.Sync(ctx, pos, o.noApply || o.noRestart)
				if err != nil {
					return nil, nil, err
				}
				return r, func(w io.Writer) {
					fmt.Fprintln(w, "synced")
					for _, p := range r.Containers {
						RestartResult{p.Container, p.Restarted, p.RestartNote}.print(w, env.NoRestart)
					}
					if r.Apply != nil && len(r.Apply.Restarted) > 0 {
						printRestarts(w, r.Apply)
					}
					if r.Apply != nil && r.Apply.Reboot {
						fmt.Fprintf(w, "the device reboots now (needed for %s)\n", r.Apply.Rebooted)
					}
				}, nil
			},
		}),
		dev(spec{
			name: "pull", args: "<device> [container...] [--force] [--all] [-- path...]", maxPos: -1, rest: true, restOptional: true,
			usage: "Check out the files of a container into ./devmode/<container>/",
			desc: "Downloads the container's files (the image plus what devmode changed) as one tar and unpacks them into\n" +
				"./devmode/<container>/: 'pvcontrol devmode files get <container> [path] --tar'. Edit any file there, then 'pvr devmode push'.\n" +
				"The first pull without paths checks out the whole container; paths after -- check out only those files or folders,\n" +
				"for example 'pvr devmode pull <device> nginx -- etc/nginx/nginx.conf'. A second pull of other paths adds to the checkout.\n" +
				"A pull without paths of a folder that already has a checkout refreshes only what was pulled before (the paths, or the\n" +
				"whole container); --all checks out the whole container.\n" +
				"./devmode/<container>/.devmode-index records what was checked out (never pushed); status and push compare with it.\n" +
				"Pulling again updates files you did not change, removes files that are gone from the device, and leaves files you\n" +
				"changed alone: a file changed here and on the device is reported as a conflict. --force overwrites those.\n" +
				"Modes are kept; owners only when pvr runs as root, otherwise they are recorded in the index only.\n" +
				"Special files (device nodes, sockets, pipes) and names that leave the folder are skipped and reported.\n" +
				"A device that cannot send tar files is pulled file by file ('pvcontrol devmode files ls' and 'get'), without an index.",
			flags: func(fs *flag.FlagSet, o *options) {
				transportFlags(fs, o)
				dirFlag(fs, o)
				fs.BoolVar(&o.force, "force", false, "overwrite and remove files changed locally")
				fs.BoolVar(&o.all, "all", false, "check out the whole container, not only what was pulled before")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				r, err := env.PullWith(ctx, pos, o.rest, o.force, o.all)
				if err != nil {
					return nil, nil, err
				}
				return map[string]interface{}{"containers": r}, func(w io.Writer) { printPull(w, r) }, nil
			},
		}),
		dev(spec{
			name: "apply", args: "<device> [container...] [--yes] [--no-wait] [--timeout D]", maxPos: -1,
			usage: "Restart containers so they see the pushed files",
			desc: "Runs 'pvcontrol devmode apply <container>'. A system container needs a device reboot:\n" +
				"'pvcontrol devmode apply <container> --reboot' runs after --yes or an interactive yes; otherwise exit code 13.\n" +
				"A restart is waited for: before the request apply waits until the container is STARTED, after it until the container is STARTED\n" +
				"again with a different pid or start time (poll 'pvcontrol containers ls' every second, --timeout default 90s, exit code 15 when it\n" +
				"does not come back). A reboot does not wait. --no-wait returns right after the request.\n" +
				"Without container names the containers found in ./devmode are applied.",
			flags: func(fs *flag.FlagSet, o *options) {
				transportFlags(fs, o)
				waitFlags(fs, o)
				dirFlag(fs, o)
				fs.BoolVar(&o.yes, "yes", false, "allow a device reboot")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				r, err := env.Apply(ctx, pos, false)
				if err != nil {
					return nil, nil, err
				}
				return r, func(w io.Writer) {
					if len(r.Restarted) > 0 {
						printRestarts(w, &r)
					}
					if r.Reboot {
						fmt.Fprintf(w, "the device reboots now (needed for %s)\n", r.Rebooted)
					}
				}, nil
			},
		}),
		dev(spec{
			name: "reset", args: "<device> <container...> [--no-restart] [--no-wait] [--timeout D]", minPos: 1, maxPos: -1,
			usage: "Empty the devmode files of containers on the device (back to the image)",
			desc: "Runs 'pvcontrol devmode reset <container>' for each container. The local tree and the deleted list are not touched.\n" +
				"Each app container is restarted afterwards so it runs the image again; a system container is not (run\n" +
				"'pvr devmode apply <device> <container> --yes'). --no-restart restarts nothing.",
			flags: func(fs *flag.FlagSet, o *options) { transportFlags(fs, o); waitFlags(fs, o); restartFlag(fs, o) },
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				if _, err := RequireActive(ctx, env.C); err != nil {
					return nil, nil, err
				}
				done := []string{}
				rs := []RestartResult{}
				for _, n := range pos {
					if err := env.C.Reset(env.opCtx(ctx, true), n); err != nil {
						return nil, nil, err
					}
					done = append(done, n)
					rr, err := env.afterChange(ctx, n, true)
					if err != nil {
						return nil, nil, err
					}
					rs = append(rs, rr)
				}
				return map[string]interface{}{"containers": done, "restarts": rs}, func(w io.Writer) {
					fmt.Fprintf(w, "%s: devmode files removed\n", joinNames(done))
					for _, rr := range rs {
						rr.print(w, env.NoRestart)
					}
				}, nil
			},
		}),
		dev(spec{
			name: "rm", args: "<device> <container> <path...> [--recursive] [--no-restart] [--no-wait] [--timeout D]", minPos: 2, maxPos: -1,
			usage: "Delete image paths in a container and remember them in the deleted list",
			desc: "Runs 'pvcontrol devmode files rm <container> <path> [--recursive]' for each path (container paths without a leading slash)\n" +
				"and adds each path to ./devmode/<container>/.devmode-deleted, creating the directory if needed.\n" +
				"push and sync delete the listed paths again. --recursive is needed for a directory with content.\n" +
				"The local copy under ./devmode/<container>/ (and its checkout index entry) is removed too, so the tree and the list agree.\n" +
				"Afterwards an app container is restarted; a system container is not (run 'pvr devmode apply <device> <container> --yes').\n" +
				"--no-restart restarts nothing.",
			flags: func(fs *flag.FlagSet, o *options) {
				transportFlags(fs, o)
				waitFlags(fs, o)
				restartFlag(fs, o)
				dirFlag(fs, o)
				fs.BoolVar(&o.recursive, "recursive", false, "delete a directory with everything in it")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				r, err := env.Remove(ctx, pos[0], pos[1:], o.recursive)
				if err != nil {
					return nil, nil, err
				}
				return r, func(w io.Writer) {
					fmt.Fprintf(w, "%s: deleted %s\n", r.Container, joinNames(r.Removed))
					RestartResult{r.Container, r.Restarted, r.RestartNote}.print(w, env.NoRestart)
				}, nil
			},
		}),
		dev(spec{
			name: "leave", args: "<device> [--rev REV]", maxPos: 0,
			usage: "Run the committed revision again; the device reboots",
			desc: "Runs 'pvcontrol devmode status' to read the base, then 'pvcontrol cmd run <base>'.\n" +
				"Use --rev to run another revision.",
			flags: func(fs *flag.FlagSet, o *options) {
				transportFlags(fs, o)
				fs.StringVar(&o.rev, "rev", "", "revision to run (default: the base of the devmode revision)")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				st, err := RequireActive(ctx, env.C)
				if err != nil {
					return nil, nil, err
				}
				rev := o.rev
				if rev == "" {
					rev = st.Base
				}
				if rev == "" {
					return nil, nil, errf(KindGeneric, "the device did not report a base revision; use --rev")
				}
				if err := env.C.Leave(ctx, rev); err != nil {
					return nil, nil, err
				}
				return map[string]interface{}{"revision": rev, "reboot": true}, func(w io.Writer) {
					fmt.Fprintf(w, "running %s; the device reboots now\n", rev)
				}, nil
			},
		}),
		dev(spec{
			name: "exec", args: "<device> <container> [--timeout D] [--cwd DIR] [--env K=V] [--stdin] [--max-output N] -- <command> [args...]", minPos: 1, maxPos: 1, rest: true,
			usage: "Run a command in a running container",
			desc: "Runs the command without a shell; to use one write 'sh -c \"...\"'. The command needs a devmode revision.\n" +
				"Its standard output and error go to this terminal and pvr exits with its exit code: 124 after a timeout,\n" +
				"128 plus the signal number when a signal ended it. --stdin sends this standard input to the command.\n" +
				"--timeout is 30s by default. With --json the output is part of the result.\n" +
				"pvcontrol transport: 'pvcontrol containers exec'. http transport: POST /containers/<container>/exec. Hub transport: the exec tool.",
			flags: func(fs *flag.FlagSet, o *options) {
				transportFlags(fs, o)
				fs.DurationVar(&o.timeout, "timeout", DefaultExecTimeout, "stop the command after this time")
				fs.StringVar(&o.cwd, "cwd", "", "working directory in the container")
				fs.Var(&o.envVars, "env", "extra environment variable K=V, repeatable")
				fs.BoolVar(&o.stdin, "stdin", false, "send this standard input to the command")
				fs.IntVar(&o.maxOutput, "max-output", 0, "bytes kept of each of stdout and stderr (the device default when 0)")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				if _, err := RequireActive(ctx, env.C); err != nil {
					return nil, nil, err
				}
				eo := ExecOpts{Timeout: o.timeout, MaxOutput: o.maxOutput, Cwd: o.cwd}
				if len(o.envVars) > 0 {
					eo.Env = map[string]string{}
					for _, kv := range o.envVars {
						k, v, ok := strings.Cut(kv, "=")
						if !ok || k == "" {
							return nil, nil, errf(KindUsage, "--env wants K=V, got "+kv)
						}
						eo.Env[k] = v
					}
				}
				if o.stdin {
					in, err := io.ReadAll(env.In)
					if err != nil {
						return nil, nil, errf(KindGeneric, "cannot read standard input: "+err.Error())
					}
					eo.Stdin = in
				}
				r, err := env.C.Exec(ctx, pos[0], o.rest, eo)
				if err != nil {
					return nil, nil, err
				}
				env.ExitCode = r.Code()
				return r, func(w io.Writer) {
					io.WriteString(w, r.Stdout)
					io.WriteString(env.Err, r.Stderr)
					if r.TimedOut {
						fmt.Fprintf(env.Err, "pvr devmode exec: stopped after %s\n", o.timeout)
					}
					if r.TruncStdout || r.TruncStderr {
						fmt.Fprintln(env.Err, "pvr devmode exec: the output was cut; raise --max-output")
					}
				}, nil
			},
		}),
		dev(spec{
			name: "logs", args: "<device> <container> [--tail N]", minPos: 1, maxPos: 1,
			usage: "Show the last console log lines of a container",
			desc: "Prints the last --tail lines (200 by default) of the console log of the container in the running devmode revision.\n" +
				"Over pvcontrol it reads /pantavisor/logs/<revision>/<container>/lxc/console.log, over http the logs CGI of the pvr-sdk container, over the Hub the logs tool.",
			flags: func(fs *flag.FlagSet, o *options) {
				transportFlags(fs, o)
				fs.IntVar(&o.tail, "tail", 200, "number of lines")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				lines, err := env.C.Logs(ctx, pos[0], o.tail)
				if err != nil {
					return nil, nil, err
				}
				return map[string]interface{}{"container": pos[0], "lines": lines}, func(w io.Writer) {
					for _, l := range lines {
						fmt.Fprintln(w, l)
					}
				}, nil
			},
		}),
		{
			name: "devices", args: "[--no-scan]", maxPos: 0, noDevice: true,
			usage: "List saved devices and devices found on the local network",
			desc:  "Reads the saved aliases from <config dir>/devmode-devices.json and runs the same mDNS scan as 'pvr device scan'.",
			flags: func(fs *flag.FlagSet, o *options) {
				fs.BoolVar(&o.noScan, "no-scan", false, "only list saved devices")
				fs.DurationVar(&o.scanTimeout, "scan-timeout", 5*time.Second, "how long to scan")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				store := NewAliasStore(configDir(c))
				names, err := store.Names()
				if err != nil {
					return nil, nil, err
				}
				all, _ := store.List()
				type aliasOut struct {
					Name   string `json:"name"`
					Target string `json:"target,omitempty"`
					Exec   string `json:"exec,omitempty"`
				}
				type scanOut struct {
					DeviceID string   `json:"device_id"`
					Host     string   `json:"host"`
					Port     int      `json:"port"`
					Addrs    []string `json:"addrs"`
					Target   string   `json:"target"`
				}
				res := struct {
					Saved   []aliasOut `json:"saved"`
					Scanned []scanOut  `json:"scanned"`
				}{[]aliasOut{}, []scanOut{}}
				for _, n := range names {
					res.Saved = append(res.Saved, aliasOut{n, all[n].Target, all[n].Exec})
				}
				if !o.noScan {
					found, err := libpvr.ScanDevices(o.scanTimeout)
					if err != nil {
						env.progress("scan failed: %v", err)
					}
					for _, f := range found {
						host := strings.TrimSuffix(f.Hostname, ".")
						so := scanOut{DeviceID: f.DeviceId, Host: host, Port: f.Port, Addrs: []string{}}
						for _, a := range f.AddrIPv4 {
							so.Addrs = append(so.Addrs, a.String())
						}
						so.Target = fmt.Sprintf("%s:%d", host, f.Port)
						res.Scanned = append(res.Scanned, so)
					}
					sort.Slice(res.Scanned, func(i, j int) bool { return res.Scanned[i].DeviceID < res.Scanned[j].DeviceID })
				}
				return res, func(w io.Writer) {
					fmt.Fprintln(w, "saved:")
					for _, a := range res.Saved {
						fmt.Fprintf(w, "  %s  %s%s\n", a.Name, a.Target, a.Exec)
					}
					if !o.noScan {
						fmt.Fprintln(w, "found on the network:")
						for _, s := range res.Scanned {
							fmt.Fprintf(w, "  %s  %s  %v\n", s.DeviceID, s.Target, s.Addrs)
						}
					}
				}, nil
			},
		},
		{
			name: "add-device", args: "<alias> <target> | <alias> --exec CMD", minPos: 1, maxPos: 2, noDevice: true,
			usage: "Save a device under a short name",
			desc: "Saves to <config dir>/devmode-devices.json (--config-dir or PVR_CONFIG_DIR).\n" +
				"target is user@host[:port] or host[:port] for ssh (the default is _pv_@host:8222),\n" +
				"http://host[:port] for the web server of the pvr-sdk container (the default port is 12368),\n" +
				"or hub:<device id or nick> for the Hub of 'pvr login' (see --baseurl).",
			flags: func(fs *flag.FlagSet, o *options) {
				fs.StringVar(&o.exec, "exec", "", "save an exec prefix instead of an ssh target")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				a := Alias{}
				if o.exec != "" {
					if len(pos) != 1 {
						return nil, nil, errf(KindUsage, "give only the alias together with --exec")
					}
					if _, err := NewExecTransport(o.exec); err != nil {
						return nil, nil, errf(KindUsage, err.Error())
					}
					a.Exec = o.exec
				} else {
					if len(pos) != 2 {
						return nil, nil, errf(KindUsage, "usage: pvr devmode add-device <alias> <target>")
					}
					if strings.HasPrefix(pos[1], "hub:") {
						if _, err := ParseHubTarget(pos[1]); err != nil {
							return nil, nil, errf(KindUsage, err.Error())
						}
						a.Target = pos[1]
					} else if strings.HasPrefix(pos[1], "http://") || strings.HasPrefix(pos[1], "https://") {
						base, err := ParseHTTPTarget(pos[1])
						if err != nil {
							return nil, nil, errf(KindUsage, err.Error())
						}
						a.Target = base
					} else {
						t, err := ParseTarget(pos[1])
						if err != nil {
							return nil, nil, errf(KindUsage, err.Error())
						}
						a.Target = t.String()
					}
				}
				store := NewAliasStore(configDir(c))
				if err := store.Set(pos[0], a); err != nil {
					return nil, nil, err
				}
				return map[string]interface{}{"alias": pos[0], "target": a.Target, "exec": a.Exec, "file": store.Path},
					func(w io.Writer) { fmt.Fprintf(w, "saved %s in %s\n", pos[0], store.Path) }, nil
			},
		},
	}
	var subs []cli.Command
	for _, s := range specs {
		subs = append(subs, s.command())
	}
	return cli.Command{
		Name:        "devmode",
		Usage:       "Sync files to a device running a devmode revision",
		ArgsUsage:   "<enter|status|push|sync|pull|apply|reset|rm|exec|logs|leave|devices|add-device>",
		Description: "See docs/devmode.md. Every command takes --json. Exit codes: 1 error, 2 usage, 10 device unreachable, 11 devmode not allowed, 12 devmode not active, 13 reboot needed, 14 apply failed, 15 container did not come back after a restart, 16 not available over this transport.",
		Subcommands: subs,
	}
}

func printRestarts(w io.Writer, r *ApplyResult) {
	for _, c := range r.Containers {
		if c.State == "rebooting" {
			continue
		}
		fmt.Fprintf(w, "%s: %s after %.1fs\n", c.Name, c.State, c.Seconds)
	}
}

func printPull(w io.Writer, res []PullResult) {
	for _, x := range res {
		if x.Method == "delta" {
			fmt.Fprintf(w, "%s: %d written, %d removed, %d skipped (pulled file by file, no checkout index)\n", x.Container, len(x.Written), len(x.Removed), len(x.Skipped))
		} else {
			fmt.Fprintf(w, "%s: %d written, %d unchanged, %d removed, %d kept (changed here), %d conflicts, %d skipped\n",
				x.Container, len(x.Written), x.Unchanged, len(x.Removed), len(x.Kept), len(x.Conflicts), len(x.Skipped))
		}
		for _, c := range x.Conflicts {
			fmt.Fprintf(w, "  conflict %s: %s\n", c.Path, c.Reason)
		}
		for _, s := range x.Skipped {
			fmt.Fprintf(w, "  skipped %s: %s\n", s.Path, s.Reason)
		}
		if len(x.Conflicts) > 0 {
			fmt.Fprintln(w, "  left alone; pull again with --force to take the device version")
		}
	}
}
