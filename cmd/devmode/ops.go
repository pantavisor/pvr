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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/term"
)

// Env is what the commands run with.
type Env struct {
	C         *Client
	Dir       string
	JSON      bool
	Yes       bool
	KeepOwner bool
	Out, Err  io.Writer
	In        *os.File
}

func (e *Env) progress(format string, a ...interface{}) {
	if !e.JSON {
		fmt.Fprintf(e.Err, format+"\n", a...)
	}
}

// emit prints the result as the versioned JSON envelope or as text.
func (e *Env) emit(command string, result interface{}, human func(w io.Writer)) {
	if e.JSON {
		b, _ := json.Marshal(map[string]interface{}{
			"schema": 1, "command": command, "ok": true, "result": result,
		})
		fmt.Fprintln(e.Out, string(b))
		return
	}
	if human != nil {
		human(e.Out)
	}
}

// EmitError prints a failure in the same envelope.
func EmitError(w io.Writer, command string, err error, asJSON bool) {
	k := KindGeneric
	if e, ok := err.(*Error); ok {
		k = e.Kind
	}
	if asJSON {
		b, _ := json.Marshal(map[string]interface{}{
			"schema": 1, "command": command, "ok": false,
			"error":     map[string]string{"kind": string(k), "message": err.Error()},
			"exit_code": k.ExitCode(),
		})
		fmt.Fprintln(w, string(b))
		return
	}
	fmt.Fprintf(w, "pvr devmode %s: %s\n", command, err)
}

// ---- push ----

type Skip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type PushResult struct {
	Container string   `json:"container"`
	Pushed    []string `json:"pushed"`
	Deleted   []string `json:"deleted"`
	Skipped   []Skip   `json:"skipped"`
	Unchanged int      `json:"unchanged"`
	// Apply is what pvr devmode apply will do for this container:
	// restart, reboot or unknown. Empty when nothing was pushed.
	Apply string `json:"apply,omitempty"`
}

func (e *Env) containersOrLocal(names []string) ([]string, error) {
	if len(names) > 0 {
		return names, nil
	}
	l, err := LocalContainers(e.Dir)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, errf(KindUsage, fmt.Sprintf("no container directories in ./%s; name a container or run pvr devmode pull first", e.Dir))
	}
	return l, nil
}

func (e *Env) PushContainer(ctx context.Context, name string, del bool) (PushResult, error) {
	r := PushResult{Container: name, Pushed: []string{}, Deleted: []string{}, Skipped: []Skip{}}
	m, err := e.C.Manifest(ctx, name)
	if err != nil {
		return r, err
	}
	local, err := ScanLocal(e.Dir, name)
	if err != nil {
		return r, err
	}
	d := ComputeDiff(local, m)
	r.Unchanged = d.Unchanged
	devDirs := map[string]bool{}
	for _, f := range m.Files {
		if f.Type == "dir" {
			devDirs[f.Path] = true
		}
	}
	byPath := map[string]LocalFile{}
	for _, l := range local {
		byPath[l.Path] = l
	}
	for _, ch := range append(append([]Change{}, d.Added...), d.Changed...) {
		l := byPath[ch.Path]
		local := filepath.Join(e.Dir, name, filepath.FromSlash(ch.Path))
		o := PutOpts{Mode: l.Mode, Type: l.Type, Target: l.Target}
		if e.KeepOwner {
			if u, g, ok := localOwner(local); ok {
				o.UID, o.GID = u, g
			}
		}
		if l.Type != "file" {
			e.progress("push %s/%s (%s)", name, ch.Path, l.Type)
			if err := e.C.Put(ctx, name, ch.Path, nil, o); err != nil {
				return r, err
			}
			r.Pushed = append(r.Pushed, ch.Path)
			continue
		}
		f, err := os.Open(local)
		if err != nil {
			return r, err
		}
		e.progress("push %s/%s", name, ch.Path)
		err = e.C.Put(ctx, name, ch.Path, f, o)
		f.Close()
		if err != nil {
			return r, err
		}
		r.Pushed = append(r.Pushed, ch.Path)
	}
	if del {
		for _, ch := range d.Deleted {
			if under(r.Deleted, ch.Path) {
				continue // went with its directory
			}
			e.progress("delete %s/%s", name, ch.Path)
			if err := e.C.Remove(ctx, name, ch.Path, devDirs[ch.Path]); err != nil {
				return r, err
			}
			r.Deleted = append(r.Deleted, ch.Path)
		}
	}
	return r, nil
}

func (e *Env) Push(ctx context.Context, names []string, del bool) ([]PushResult, error) {
	if _, err := e.C.RequireActive(ctx); err != nil {
		return nil, err
	}
	names, err := e.containersOrLocal(names)
	if err != nil {
		return nil, err
	}
	policies, perr := e.C.RestartPolicies(ctx)
	var out []PushResult
	for _, n := range names {
		r, err := e.PushContainer(ctx, n, del)
		if len(r.Pushed)+len(r.Deleted) > 0 {
			r.Apply = "unknown"
			if perr == nil {
				r.Apply = ApplyAction(policies[n])
			}
		}
		out = append(out, r)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func (e *Env) printPush(w io.Writer, res []PushResult) {
	for _, r := range res {
		fmt.Fprintf(w, "%s: %d pushed, %d deleted, %d unchanged", r.Container, len(r.Pushed), len(r.Deleted), r.Unchanged)
		if len(r.Skipped) > 0 {
			fmt.Fprintf(w, ", %d skipped", len(r.Skipped))
		}
		fmt.Fprintln(w)
		for _, s := range r.Skipped {
			fmt.Fprintf(w, "  skipped %s: %s\n", s.Path, s.Reason)
		}
		if r.Apply != "" {
			fmt.Fprintf(w, "  pvr devmode apply %s will %s\n", r.Container, r.Apply)
		}
	}
}

// under tells if p is inside one of the deleted directories.
func under(deleted []string, p string) bool {
	for _, d := range deleted {
		if strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// ---- pull ----

type PullResult struct {
	Container string   `json:"container"`
	Written   []string `json:"written"`
	Removed   []string `json:"removed"`
	Skipped   []Skip   `json:"skipped"`
}

func (e *Env) Pull(ctx context.Context, names []string) ([]PullResult, error) {
	st, err := e.C.RequireActive(ctx)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		for _, c := range st.Containers {
			names = append(names, c.Name)
		}
	}
	var out []PullResult
	for _, n := range names {
		r := PullResult{Container: n, Written: []string{}, Removed: []string{}, Skipped: []Skip{}}
		m, err := e.C.Manifest(ctx, n)
		if err != nil {
			return out, err
		}
		for _, f := range m.Files {
			dst, err := RemoteToLocal(e.Dir, n, f.Path)
			if err != nil {
				r.Skipped = append(r.Skipped, Skip{f.Path, err.Error()})
				continue
			}
			switch f.Type {
			case "dir":
				if err := os.MkdirAll(dst, 0o755); err != nil {
					return out, err
				}
				if m, ok := parseMode(f.Mode); ok {
					_ = os.Chmod(dst, os.FileMode(m&0o777)|0o700)
				}
			case "deleted":
				if err := os.Remove(dst); err == nil {
					r.Removed = append(r.Removed, f.Path)
				}
			case "symlink":
				_ = os.Remove(dst)
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					return out, err
				}
				if err := os.Symlink(f.Target, dst); err != nil {
					r.Skipped = append(r.Skipped, Skip{f.Path, err.Error()})
					continue
				}
				r.Written = append(r.Written, f.Path)
			case "file":
				e.progress("pull %s/%s", n, f.Path)
				if err := e.pullFile(ctx, n, f, dst); err != nil {
					return out, err
				}
				r.Written = append(r.Written, f.Path)
			default:
				reason := "unknown type " + f.Type
				if f.Type == "special" {
					reason = "special file (device node, socket or pipe)"
				}
				r.Skipped = append(r.Skipped, Skip{f.Path, reason})
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func (e *Env) pullFile(ctx context.Context, container string, f Entry, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".pvr-pull-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := e.C.Get(ctx, container, f.Path, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	mode := uint32(0o644)
	if m, ok := parseMode(f.Mode); ok {
		mode = m & 0o777
	}
	if err := os.Chmod(tmp.Name(), os.FileMode(mode)); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// ---- apply ----

type ApplyResult struct {
	Restarted []string `json:"restarted"`
	// Rebooted is the container whose apply rebooted the device; the
	// reboot restarts the others too.
	Rebooted string `json:"rebooted,omitempty"`
	Reboot   bool   `json:"reboot"`
}

func (e *Env) confirm(prompt string) bool {
	if e.Yes {
		return true
	}
	if e.JSON || e.In == nil {
		return false
	}
	if !term.IsTerminal(int(e.In.Fd())) {
		return false
	}
	fmt.Fprintf(e.Err, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(e.In).ReadString('\n')
	l := strings.ToLower(strings.TrimSpace(line))
	return l == "y" || l == "yes"
}

// Apply restarts the containers; system containers need a reboot, which
// asks first. allowReboot overrides the question (watch mode).
func (e *Env) Apply(ctx context.Context, names []string, allowReboot bool) (ApplyResult, error) {
	res := ApplyResult{Restarted: []string{}}
	if _, err := e.C.RequireActive(ctx); err != nil {
		return res, err
	}
	names, err := e.containersOrLocal(names)
	if err != nil {
		return res, err
	}
	policies, _ := e.C.RestartPolicies(ctx)
	var restart, system []string
	for _, n := range names {
		if policies[n] == "system" {
			system = append(system, n)
		} else {
			restart = append(restart, n)
		}
	}
	if len(system) > 0 && !allowReboot && !e.Yes {
		if !e.confirm(fmt.Sprintf("%s needs a device reboot. Reboot now?", strings.Join(system, ", "))) {
			return res, errf(KindRebootNeeded, fmt.Sprintf("%s needs a device reboot; run again with --yes to allow it", strings.Join(system, ", ")))
		}
	}
	for _, n := range restart {
		e.progress("restart %s", n)
		err := e.C.Apply(ctx, n, false)
		if ae, ok := err.(*Error); ok && ae.Kind == KindRebootNeeded {
			system = append(system, n)
			continue
		}
		if err != nil {
			return res, err
		}
		res.Restarted = append(res.Restarted, n)
	}
	if len(system) > 0 {
		if !allowReboot && !e.Yes && !e.confirm(fmt.Sprintf("%s needs a device reboot. Reboot now?", strings.Join(system, ", "))) {
			return res, errf(KindRebootNeeded, fmt.Sprintf("%s needs a device reboot; run again with --yes to allow it", strings.Join(system, ", ")))
		}
		e.progress("reboot the device for %s", strings.Join(system, ", "))
		if err := e.C.Apply(ctx, system[0], true); err != nil {
			return res, err
		}
		res.Reboot, res.Rebooted = true, system[0]
	}
	return res, nil
}

// ---- status ----

type ContainerStatus struct {
	Name         string `json:"name"`
	LocalPresent bool   `json:"local_present"`
	Diff         *Diff  `json:"diff,omitempty"`
	Apply        string `json:"apply"`
}

type StatusResult struct {
	Devmode    Status            `json:"devmode"`
	Containers []ContainerStatus `json:"containers"`
}

func (e *Env) Status(ctx context.Context, names []string) (StatusResult, error) {
	var res StatusResult
	st, err := e.C.Status(ctx)
	if err != nil {
		return res, err
	}
	res.Devmode = st
	res.Containers = []ContainerStatus{}
	if !st.Active {
		return res, nil
	}
	policies, perr := e.C.RestartPolicies(ctx)
	if len(names) == 0 {
		for _, c := range st.Containers {
			names = append(names, c.Name)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		cs := ContainerStatus{Name: n, Apply: "unknown"}
		if perr == nil {
			cs.Apply = ApplyAction(policies[n])
		}
		if fi, err := os.Stat(filepath.Join(e.Dir, n)); err == nil && fi.IsDir() {
			cs.LocalPresent = true
			m, err := e.C.Manifest(ctx, n)
			if err != nil {
				return res, err
			}
			local, err := ScanLocal(e.Dir, n)
			if err != nil {
				return res, err
			}
			d := ComputeDiff(local, m)
			cs.Diff = &d
		}
		res.Containers = append(res.Containers, cs)
	}
	return res, nil
}

func (e *Env) printStatus(w io.Writer, r StatusResult) {
	s := r.Devmode
	switch {
	case !s.Enabled:
		fmt.Fprintln(w, "devmode: not allowed on this device (PV_DEVMODE_ALLOWED is off)")
		return
	case !s.Active:
		fmt.Fprintln(w, "devmode: not active; run pvr devmode enter")
		return
	}
	fmt.Fprintf(w, "devmode: active, revision %s, base %s\n", s.Revision, s.Base)
	for _, c := range r.Containers {
		fmt.Fprintf(w, "%s (apply: %s)\n", c.Name, c.Apply)
		if c.Diff == nil {
			fmt.Fprintf(w, "  no local directory ./%s/%s\n", e.Dir, c.Name)
			continue
		}
		if c.Diff.Empty() {
			fmt.Fprintf(w, "  in sync (%d files)\n", c.Diff.Unchanged)
		}
		for _, x := range c.Diff.Added {
			fmt.Fprintf(w, "  added    %s\n", x.Path)
		}
		for _, x := range c.Diff.Changed {
			fmt.Fprintf(w, "  changed  %s (%s)\n", x.Path, x.Reason)
		}
		for _, x := range c.Diff.Deleted {
			fmt.Fprintf(w, "  deleted  %s (on device only; push --delete removes it)\n", x.Path)
		}
	}
}
