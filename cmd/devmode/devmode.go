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
	json, yes, del, watch, keepOwner, resume, noScan bool
	exec, dir, base, rev                             string
	sshOpts                                          multi
	scanTimeout                                      time.Duration
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
}

func commonFlags(fs *flag.FlagSet, o *options) {
	fs.BoolVar(&o.json, "json", false, "print one JSON document (schema 1) instead of text")
}

func transportFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.exec, "exec", "", "run commands through this prefix instead of ssh, e.g. 'docker exec -i ae' (then no <device> argument)")
	fs.Var(&o.sshOpts, "ssh-opt", "extra ssh argument, repeatable, e.g. --ssh-opt=-oIdentityFile=key")
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

// resolve turns the device argument into a transport.
func resolve(c *cli.Context, o *options, device string) (Transport, error) {
	if o.exec != "" {
		return NewExecTransport(o.exec)
	}
	target := device
	if a, ok, err := NewAliasStore(configDir(c)).Get(device); err != nil {
		return nil, err
	} else if ok {
		if a.Exec != "" {
			return NewExecTransport(a.Exec)
		}
		target = a.Target
	}
	t, err := ParseTarget(target)
	if err != nil {
		return nil, errf(KindUsage, err.Error())
	}
	return NewSSHTransport(t, o.sshOpts), nil
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
			pos, err := parseArgs(fs, c.Args())
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
			env := &Env{Dir: o.dir, JSON: o.json, Yes: o.yes, KeepOwner: o.keepOwner,
				Out: os.Stdout, Err: os.Stderr, In: os.Stdin}
			if s.device {
				t, err := resolve(c, o, device)
				if err != nil {
					return s.fail(o, err)
				}
				defer t.Close()
				env.C = &Client{T: t}
			}
			res, human, err := s.run(ctx, env, o, c, pos)
			if err != nil {
				return s.fail(o, err)
			}
			if res != nil {
				env.emit(s.name, res, human)
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
			desc: "Runs 'pvcontrol devmode status', 'pvcontrol devmode files ls <container>' and 'pvcontrol containers ls'.\n" +
				"added: only local. changed: content or mode differs. deleted: only on the device (removed by 'push --delete').\n" +
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
			name: "push", args: "<device> [container...] [--delete] [--watch] [--yes]", maxPos: -1,
			usage: "Upload changed files from ./devmode/<container>/ to the device",
			desc: "Runs 'pvcontrol devmode files put <container> <path> - --mode M --uid U --gid G' per changed file,\n" +
				"and with --delete 'pvcontrol devmode files rm <container> <path>' per file missing locally.\n" +
				"Files keep their permission bits. The owner is root (0/0) unless --keep-owner uses the local numeric uid/gid.\n" +
				"--watch pushes and applies again whenever the tree changes (400ms quiet time); --yes lets it reboot for system containers.",
			flags: func(fs *flag.FlagSet, o *options) {
				transportFlags(fs, o)
				dirFlag(fs, o)
				fs.BoolVar(&o.del, "delete", false, "remove device files that are missing locally")
				fs.BoolVar(&o.watch, "watch", false, "keep running: push and apply on every change")
				fs.BoolVar(&o.yes, "yes", false, "allow a device reboot in watch mode")
				fs.BoolVar(&o.keepOwner, "keep-owner", false, "use the local uid/gid instead of 0/0")
			},
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				if o.watch {
					return nil, nil, env.Watch(ctx, pos, o.del)
				}
				r, err := env.Push(ctx, pos, o.del)
				if err != nil {
					return nil, nil, err
				}
				return map[string]interface{}{"containers": r}, func(w io.Writer) {
					env.printPush(w, r)
					for _, x := range r {
						if x.Apply != "" {
							fmt.Fprintln(w, "run pvr devmode apply to make the container see the files")
							break
						}
					}
				}, nil
			},
		}),
		dev(spec{
			name: "pull", args: "<device> [container...]", maxPos: -1,
			usage: "Fetch the devmode files of the device into ./devmode/<container>/",
			desc: "Runs 'pvcontrol devmode files ls <container>' and 'pvcontrol devmode files get <container> <path>'.\n" +
				"Files deleted in devmode are removed from the local tree.",
			flags: func(fs *flag.FlagSet, o *options) { transportFlags(fs, o); dirFlag(fs, o) },
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				r, err := env.Pull(ctx, pos)
				if err != nil {
					return nil, nil, err
				}
				return map[string]interface{}{"containers": r}, func(w io.Writer) {
					for _, x := range r {
						fmt.Fprintf(w, "%s: %d written, %d removed, %d skipped\n", x.Container, len(x.Written), len(x.Removed), len(x.Skipped))
					}
				}, nil
			},
		}),
		dev(spec{
			name: "apply", args: "<device> [container...] [--yes]", maxPos: -1,
			usage: "Restart containers so they see the pushed files",
			desc: "Runs 'pvcontrol devmode apply <container>'. A system container needs a device reboot:\n" +
				"'pvcontrol devmode apply <container> --reboot' runs after --yes or an interactive yes; otherwise exit code 13.\n" +
				"Without container names the containers found in ./devmode are applied.",
			flags: func(fs *flag.FlagSet, o *options) {
				transportFlags(fs, o)
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
						fmt.Fprintf(w, "restarted: %s\n", joinNames(r.Restarted))
					}
					if r.Reboot {
						fmt.Fprintf(w, "the device reboots now (needed for %s)\n", r.Rebooted)
					}
				}, nil
			},
		}),
		dev(spec{
			name: "reset", args: "<device> <container>", minPos: 1, maxPos: 1,
			usage: "Empty the devmode files of a container on the device",
			desc:  "Runs 'pvcontrol devmode reset <container>'. The local tree is not touched.",
			flags: func(fs *flag.FlagSet, o *options) { transportFlags(fs, o) },
			run: func(ctx context.Context, env *Env, o *options, c *cli.Context, pos []string) (interface{}, func(io.Writer), error) {
				if _, err := env.C.RequireActive(ctx); err != nil {
					return nil, nil, err
				}
				if err := env.C.Reset(ctx, pos[0]); err != nil {
					return nil, nil, err
				}
				return map[string]string{"container": pos[0]}, func(w io.Writer) {
					fmt.Fprintf(w, "%s: devmode files removed; run pvr devmode apply to restart it\n", pos[0])
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
				st, err := env.C.RequireActive(ctx)
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
				"target is user@host[:port] or host[:port]; the default is _pv_@host:8222.",
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
					t, err := ParseTarget(pos[1])
					if err != nil {
						return nil, nil, errf(KindUsage, err.Error())
					}
					a.Target = t.String()
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
		ArgsUsage:   "<enter|status|push|pull|apply|reset|leave|devices|add-device>",
		Description: "See docs/devmode.md. Every command takes --json. Exit codes: 1 error, 2 usage, 10 device unreachable, 11 devmode not allowed, 12 devmode not active, 13 reboot needed, 14 apply failed.",
		Subcommands: subs,
	}
}
