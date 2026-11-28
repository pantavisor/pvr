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
	"time"

	"golang.org/x/term"
)

// Env is what the commands run with.
type Env struct {
	C         Backend
	Dir       string
	JSON      bool
	Yes       bool
	KeepOwner bool
	Out, Err  io.Writer
	In        *os.File
	// NoWait makes apply return right after the request.
	NoWait bool
	// NoRestart keeps push, rm, sync and reset from restarting the containers.
	NoRestart bool
	// WaitTimeout is how long apply waits for a container (default 90s).
	WaitTimeout time.Duration
	// Poll is the pause between two container checks (default 1s).
	Poll time.Duration
	// BusyBase is the first pause when the device is busy (default 1s, doubling
	// to 8x); BusyTotal is how long to keep trying (default 60s).
	BusyBase, BusyTotal time.Duration
	// ExitCode is the exit code the process ends with after the command ran.
	ExitCode int
}

func (e *Env) timeout() time.Duration {
	if e.WaitTimeout > 0 {
		return e.WaitTimeout
	}
	return 90 * time.Second
}

func (e *Env) poll() time.Duration {
	if e.Poll > 0 {
		return e.Poll
	}
	return time.Second
}

// waitContainer polls `pvcontrol containers ls` until ok accepts the state
// of the container.
func (e *Env) waitContainer(ctx context.Context, name, what string, ok func(ContainerState) bool) (ContainerState, error) {
	deadline := time.Now().Add(e.timeout())
	var last ContainerState
	for {
		m, err := e.C.Containers(ctx)
		if err != nil {
			return last, err
		}
		st, found := m[name]
		if found {
			last = st
			if ok(st) {
				return st, nil
			}
		}
		if time.Now().After(deadline) {
			status := "not listed"
			if found {
				status = st.Status
			}
			return last, errf(KindWaitTimeout, fmt.Sprintf("%s: %s did not happen within %s (status %s); see pvcontrol containers ls", name, what, e.timeout(), status))
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(e.poll()):
		}
	}
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
	// Deleted lists the image paths removed because they are on the deleted list.
	Deleted   []string `json:"deleted"`
	Skipped   []Skip   `json:"skipped"`
	Unchanged int      `json:"unchanged"`
	// Apply is what pvr devmode apply will do for this container:
	// restart, reboot or unknown. Empty when nothing changed.
	Apply string `json:"apply,omitempty"`
	// Restarted is true when the container was restarted after the push;
	// RestartNote says why not (a system container).
	Restarted   bool   `json:"restarted"`
	RestartNote string `json:"restart_note,omitempty"`
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

func isNotFound(err error) bool {
	e, ok := err.(*Error)
	return ok && strings.Contains(e.Msg, "Not found")
}

// putLocal uploads one local file, symlink or directory.
func (e *Env) putLocal(ctx context.Context, name string, l LocalFile, uid, gid int) error {
	return e.putItemNow(ctx, name, e.putItem(name, l, uid, gid))
}

// deleteListed removes one deleted-list path from the container's view;
// true when it was there.
func (e *Env) deleteListed(ctx context.Context, name, p string) (bool, error) {
	e.progress("delete %s/%s", name, p)
	err := e.retryBusy(ctx, func() error { return e.C.Remove(ctx, name, p, true) })
	if isNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// PushContainer adds and updates files, and removes the paths of the
// deleted list that are not deleted yet. It removes nothing else.
func (e *Env) PushContainer(ctx context.Context, name string) (PushResult, error) {
	ix, err := loadIndex(e.Dir, name)
	if err != nil {
		return PushResult{}, errf(KindGeneric, err.Error())
	}
	if ix != nil {
		return e.pushIndexed(ctx, name, ix)
	}
	r := PushResult{Container: name, Pushed: []string{}, Deleted: []string{}, Skipped: []Skip{}}
	m, err := e.C.Manifest(ctx, name)
	if err != nil {
		return r, err
	}
	local, err := ScanLocal(e.Dir, name)
	if err != nil {
		return r, err
	}
	list, err := ReadDeletedList(e.Dir, name)
	if err != nil {
		return r, err
	}
	if err := ValidateTree(local, list); err != nil {
		return r, err
	}
	d := ComputeDiff(local, m, list)
	r.Unchanged = d.Unchanged
	byPath := map[string]LocalFile{}
	for _, l := range local {
		byPath[l.Path] = l
	}
	if _, ok := e.C.(Batcher); ok {
		var ops []BatchOp
		var paths []string
		for _, ch := range append(append([]Change{}, d.Added...), d.Changed...) {
			l := byPath[ch.Path]
			uid, gid := e.ownerFor(name, l, nil)
			ops = append(ops, BatchOp{Put: e.putItem(name, l, uid, gid)})
			paths = append(paths, ch.Path)
		}
		np := len(ops)
		for _, ch := range d.ToDelete {
			ops = append(ops, BatchOp{Remove: ch.Path})
			paths = append(paths, ch.Path)
		}
		sent, err := e.sendOps(ctx, name, ops)
		for i := 0; i < sent; i++ {
			if i < np {
				r.Pushed = append(r.Pushed, paths[i])
			} else {
				r.Deleted = append(r.Deleted, paths[i])
			}
		}
		return r, e.pushShort(name, len(ops), sent, paths, err)
	}
	b := e.newBatch(len(d.Added) + len(d.Changed) + len(d.ToDelete))
	for _, ch := range append(append([]Change{}, d.Added...), d.Changed...) {
		l := byPath[ch.Path]
		uid, gid := e.ownerFor(name, l, nil)
		if err := e.putLocal(b.next(ctx), name, l, uid, gid); err != nil {
			return r, err
		}
		r.Pushed = append(r.Pushed, ch.Path)
	}
	for _, ch := range d.ToDelete {
		ok, err := e.deleteListed(b.next(ctx), name, ch.Path)
		if err != nil {
			return r, err
		}
		if ok {
			r.Deleted = append(r.Deleted, ch.Path)
		}
	}
	return r, nil
}

func (e *Env) Push(ctx context.Context, names []string) ([]PushResult, error) {
	if _, err := RequireActive(ctx, e.C); err != nil {
		return nil, err
	}
	names, err := e.containersOrLocal(names)
	if err != nil {
		return nil, err
	}
	policies, perr := e.C.RestartPolicies(ctx)
	var out []PushResult
	for _, n := range names {
		r, err := e.PushContainer(ctx, n)
		changed := len(r.Pushed)+len(r.Deleted) > 0
		if changed {
			r.Apply = "unknown"
			if perr == nil {
				r.Apply = ApplyAction(policies[n])
			}
		}
		if err == nil {
			var rr RestartResult
			rr, err = e.afterChange(ctx, n, changed)
			r.Restarted, r.RestartNote = rr.Restarted, rr.Note
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
		fmt.Fprintf(w, "%s: %d pushed", r.Container, len(r.Pushed))
		if len(r.Deleted) > 0 {
			fmt.Fprintf(w, ", %s deleted", plural(len(r.Deleted), "image path", "image paths"))
		}
		fmt.Fprintf(w, ", %d unchanged", r.Unchanged)
		if len(r.Skipped) > 0 {
			fmt.Fprintf(w, ", %d skipped", len(r.Skipped))
		}
		fmt.Fprintln(w)
		for _, s := range r.Skipped {
			fmt.Fprintf(w, "  skipped %s: %s\n", s.Path, s.Reason)
		}
		if r.Apply != "" {
			RestartResult{r.Container, r.Restarted, r.RestartNote}.print(w, e.NoRestart)
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
	// Conflicts are paths changed here and on the device; pull left them alone.
	Conflicts []Skip `json:"conflicts"`
	// Kept are paths with a local change that the device did not touch.
	Kept []string `json:"kept"`
	// Unchanged counts paths that were already as the device has them.
	Unchanged int `json:"unchanged"`
	// Method is tar, or delta for a device that cannot send tar files.
	Method string `json:"method"`
	// DeletedList is what was written to the container's .devmode-deleted (delta pull only).
	DeletedList []string `json:"deleted_list"`
}

// Pull checks out the files of containers into ./devmode/<container>/: all
// of them, or only the given paths.
func (e *Env) Pull(ctx context.Context, names, paths []string, force bool) ([]PullResult, error) {
	return e.PullWith(ctx, names, paths, force, false)
}

// PullWith is Pull with all: check out the whole container even when an
// earlier pull took only some paths.
func (e *Env) PullWith(ctx context.Context, names, paths []string, force, all bool) ([]PullResult, error) {
	if all && len(paths) > 0 {
		return nil, errf(KindUsage, "--all and paths after -- cannot be used together")
	}
	st, err := RequireActive(ctx, e.C)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		for _, c := range st.Containers {
			names = append(names, c.Name)
		}
	}
	var scopes []string
	for _, p := range paths {
		c, err := treePath(p)
		if err != nil {
			return nil, errf(KindUsage, err.Error())
		}
		scopes = append(scopes, c)
	}
	var out []PullResult
	for _, n := range names {
		if strings.ContainsAny(n, "/\\") || n == "" || n == "." || n == ".." {
			return out, errf(KindUsage, fmt.Sprintf("bad container name %q", n))
		}
		r := PullResult{Container: n, Written: []string{}, Removed: []string{}, Skipped: []Skip{}, Conflicts: []Skip{}, Kept: []string{},
			DeletedList: []string{}, Method: "tar"}
		err := e.pullTree(ctx, n, scopes, force, all, &r)
		if isNoTar(err) {
			e.progress("%s: the device cannot send a folder as a tar; pulling file by file", n)
			r = PullResult{Container: n, Written: []string{}, Removed: []string{}, Skipped: []Skip{}, Conflicts: []Skip{}, Kept: []string{},
				DeletedList: []string{}, Method: "delta"}
			err = e.pullDelta(ctx, n, scopes, &r)
		}
		out = append(out, r)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// pullDelta is the pull of a device without tar: the files devmode changed,
// one by one. It leaves no index, so push and status compare with the device.
func (e *Env) pullDelta(ctx context.Context, n string, scopes []string, r *PullResult) error {
	m, err := e.C.Manifest(ctx, n)
	if err != nil {
		return err
	}
	// the folder is where edits start, even before devmode changed anything
	if err := os.MkdirAll(filepath.Join(e.Dir, n), 0o755); err != nil {
		return err
	}
	if err := os.Remove(indexPath(e.Dir, n)); err != nil && !os.IsNotExist(err) {
		return err
	}
	inAny := func(p string) bool {
		if len(scopes) == 0 {
			return true
		}
		for _, sc := range scopes {
			if inScope(sc, p) {
				return true
			}
		}
		return false
	}
	for _, f := range m.Files {
		if !inAny(f.Path) {
			continue
		}
		dst, err := RemoteToLocal(e.Dir, n, f.Path)
		if err != nil {
			r.Skipped = append(r.Skipped, Skip{f.Path, err.Error()})
			continue
		}
		switch f.Type {
		case "dir":
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			if m, ok := parseMode(f.Mode); ok {
				_ = os.Chmod(dst, os.FileMode(m&0o777)|0o700)
			}
		case "deleted":
			r.DeletedList = append(r.DeletedList, f.Path)
			if err := os.Remove(dst); err == nil {
				r.Removed = append(r.Removed, f.Path)
			}
		case "symlink":
			_ = os.Remove(dst)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(f.Target, dst); err != nil {
				r.Skipped = append(r.Skipped, Skip{f.Path, err.Error()})
				continue
			}
			r.Written = append(r.Written, f.Path)
		case "file":
			e.progress("pull %s/%s", n, f.Path)
			if err := e.pullFile(ctx, n, f, dst); err != nil {
				return err
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
	if len(scopes) > 0 {
		// a partial pull keeps the deleted paths outside it
		cur, err := ReadDeletedList(e.Dir, n)
		if err != nil {
			return err
		}
		for _, p := range cur {
			if !inAny(p) {
				r.DeletedList = append(r.DeletedList, p)
			}
		}
	}
	return WriteDeletedList(e.Dir, n, append([]string{}, r.DeletedList...))
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

// ApplyState is what happened to one container.
type ApplyState struct {
	Name string `json:"name"`
	// State is STARTED when the container runs again, or restart_requested
	// (no wait) or rebooting.
	State   string  `json:"state"`
	Seconds float64 `json:"seconds"`
}

type ApplyResult struct {
	Containers []ApplyState `json:"containers"`
	Restarted  []string     `json:"restarted"`
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
	res := ApplyResult{Restarted: []string{}, Containers: []ApplyState{}}
	if _, err := RequireActive(ctx, e.C); err != nil {
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
		st, reboot, err := e.restartApp(ctx, n)
		if err != nil {
			return res, err
		}
		if reboot {
			system = append(system, n)
			continue
		}
		res.Restarted = append(res.Restarted, n)
		res.Containers = append(res.Containers, st)
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
		res.Containers = append(res.Containers, ApplyState{system[0], "rebooting", 0})
	}
	return res, nil
}

// ---- status ----

type ContainerStatus struct {
	Name         string `json:"name"`
	LocalPresent bool   `json:"local_present"`
	// Checkout is true when the folder has an index; the diff is then against it.
	Checkout bool   `json:"checkout"`
	Diff     *Diff  `json:"diff,omitempty"`
	Apply    string `json:"apply"`
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
			ix, err := loadIndex(e.Dir, n)
			if err != nil {
				return res, errf(KindGeneric, err.Error())
			}
			local, err := ScanLocal(e.Dir, n)
			if err != nil {
				return res, err
			}
			list, err := ReadDeletedList(e.Dir, n)
			if err != nil {
				return res, err
			}
			var d Diff
			if ix != nil {
				cs.Checkout = true
				if d, err = computeIndexDiff(e.Dir, n, ix, local, list); err != nil {
					return res, err
				}
			} else {
				m, err := e.C.Manifest(ctx, n)
				if err != nil {
					return res, err
				}
				d = ComputeDiff(local, m, list)
			}
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
			fmt.Fprintf(w, "  deleted  %s (push deletes it on the device)\n", x.Path)
		}
		for _, x := range c.Diff.OnlyOnDevice {
			fmt.Fprintf(w, "  only on device  %s (sync removes it)\n", x.Path)
		}
		for _, x := range c.Diff.ToDelete {
			fmt.Fprintf(w, "  to delete       %s (image path on the deleted list)\n", x.Path)
		}
	}
}

// ---- sync ----

type SyncPlan struct {
	Container string   `json:"container"`
	Files     int      `json:"files"`
	Symlinks  int      `json:"symlinks"`
	Dirs      int      `json:"directories"`
	Delete    []string `json:"delete"`
	Summary   string   `json:"summary"`
	// Restarted is true when the container was restarted after the sync;
	// RestartNote says why not (a system container).
	Restarted   bool   `json:"restarted"`
	RestartNote string `json:"restart_note,omitempty"`
	local       []LocalFile
	index       *Index // the checkout index, nil for a folder from the old pull
	listed      []string
	// Skipped counts image files left out of the upload because they are
	// unchanged from what pull checked out.
	Skipped int `json:"skipped_unchanged,omitempty"`
	upload  []LocalFile
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (p *SyncPlan) count() {
	p.Files, p.Symlinks, p.Dirs = 0, 0, 0
	for _, l := range p.upload {
		switch l.Type {
		case "symlink":
			p.Symlinks++
		case "dir":
			p.Dirs++
		default:
			p.Files++
		}
	}
	p.summarize()
}

func (p *SyncPlan) summarize() {
	parts := []string{"emptying devmode files"}
	var up []string
	if p.Files > 0 {
		up = append(up, plural(p.Files, "file", "files"))
	}
	if p.Symlinks > 0 {
		up = append(up, plural(p.Symlinks, "symlink", "symlinks"))
	}
	if p.Dirs > 0 {
		up = append(up, plural(p.Dirs, "directory", "directories"))
	}
	if len(up) > 0 {
		parts = append(parts, "uploading "+strings.Join(up, ", "))
	}
	if len(p.Delete) > 0 {
		parts = append(parts, "deleting "+plural(len(p.Delete), "image file", "image files"))
	}
	if p.Skipped > 0 {
		parts = append(parts, plural(p.Skipped, "unchanged image file", "unchanged image files")+" not uploaded")
	}
	p.Summary = p.Container + ": " + strings.Join(parts, ", ")
}

// PlanSync checks the whole local tree of every selected container. It
// does not talk to the device.
func (e *Env) PlanSync(names []string) ([]SyncPlan, error) {
	names, err := e.containersOrLocal(names)
	if err != nil {
		return nil, err
	}
	var plans []SyncPlan
	for _, n := range names {
		if n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\\ \t\n") {
			return nil, errf(KindUsage, fmt.Sprintf("bad container name %q", n))
		}
		fi, err := os.Stat(filepath.Join(e.Dir, n))
		if err != nil || !fi.IsDir() {
			return nil, errf(KindUsage, fmt.Sprintf("no local directory ./%s/%s", e.Dir, n))
		}
		local, err := ScanLocal(e.Dir, n)
		if err != nil {
			return nil, errf(KindGeneric, fmt.Sprintf("%s: %v", n, err))
		}
		list, err := ReadDeletedList(e.Dir, n)
		if err != nil {
			return nil, errf(KindGeneric, fmt.Sprintf("%s: %v", n, err))
		}
		if err := ValidateTree(local, list); err != nil {
			return nil, errf(KindGeneric, fmt.Sprintf("%s: %v", n, err))
		}
		p := SyncPlan{Container: n, Delete: list, local: local, listed: list}
		if p.Delete == nil {
			p.Delete = []string{}
		}
		ix, err := loadIndex(e.Dir, n)
		if err != nil {
			return nil, errf(KindGeneric, fmt.Sprintf("%s: %v", n, err))
		}
		if ix != nil {
			// checked-out paths that are gone here are deleted too
			gone, err := goneFromIndex(e.Dir, n, ix)
			if err != nil {
				return nil, errf(KindGeneric, fmt.Sprintf("%s: %v", n, err))
			}
			p.index = ix
			p.Delete = append(p.Delete, gone...)
		}
		p.upload = local
		p.count()
		plans = append(plans, p)
	}
	return plans, nil
}

type SyncResult struct {
	Containers []SyncPlan   `json:"containers"`
	Apply      *ApplyResult `json:"apply,omitempty"`
}

// Sync makes each container the image plus its local tree minus its
// deleted list: reset, upload everything, delete the listed paths, then
// restart the app containers (not the system ones, unless --yes).
func (e *Env) Sync(ctx context.Context, names []string, noApply bool) (SyncResult, error) {
	var res SyncResult
	if noApply {
		e.NoRestart = true
	}
	plans, err := e.PlanSync(names)
	if err != nil {
		return res, err
	}
	res.Containers = plans
	if _, err := RequireActive(ctx, e.C); err != nil {
		return res, err
	}
	for i := range plans {
		p := &plans[i]
		if p.index == nil {
			continue
		}
		// what the device holds in its changes now is lost by the reset, so it is sent again
		m, err := e.C.Manifest(ctx, p.Container)
		if err != nil {
			return res, err
		}
		inlay := map[string]bool{}
		for _, f := range m.Files {
			if f.Type != "deleted" {
				inlay[f.Path] = true
			}
		}
		p.upload = uploadSet(p.local, p.index, inlay)
		p.Skipped = len(p.local) - len(p.upload)
		p.count()
	}
	for _, p := range plans {
		e.progress("%s", p.Summary)
	}
	var rest []string
	for i := range plans {
		p := &plans[i]
		var ops []BatchOp
		owners := map[string][2]int{}
		for _, l := range p.upload {
			uid, gid := e.ownerFor(p.Container, l, p.index)
			owners[l.Path] = [2]int{uid, gid}
			ops = append(ops, BatchOp{Put: e.putItem(p.Container, l, uid, gid)})
		}
		for _, d := range p.Delete {
			ops = append(ops, BatchOp{Remove: d})
		}
		e.progress("reset %s", p.Container)
		rctx := e.opCtx(ctx, len(ops) == 0)
		if err := e.retryBusy(ctx, func() error { return e.C.Reset(rctx, p.Container) }); err != nil {
			return res, err
		}
		sent, err := e.sendOps(ctx, p.Container, ops)
		if err != nil {
			missing := len(ops) - sent
			if p.index != nil {
				for j := sent; j < len(p.upload); j++ {
					p.index.Pending = append(p.index.Pending, p.upload[j].Path)
				}
				p.index.save(e.Dir, p.Container)
			}
			first := ""
			if sent < len(p.upload) {
				first = ", first " + p.upload[sent].Path
			} else if sent < len(ops) {
				first = ", first " + ops[sent].Remove
			}
			kind := KindGeneric
			if ee, ok := err.(*Error); ok {
				kind = ee.Kind
			}
			return res, errf(kind, fmt.Sprintf("%s was reset and only %d of %d changes reached it (%d missing%s): %s; run pvr devmode sync again to finish",
				p.Container, sent, len(ops), missing, first, err.Error()))
		}
		if p.index != nil {
			// the device now has the image plus the local tree
			for _, l := range p.upload {
				o := owners[l.Path]
				p.index.set(entryOf(l, o[0], o[1]))
			}
			for _, d := range p.Delete {
				if !ix2contains(p.listed, d) {
					p.index.drop(d)
				}
			}
			p.index.Deleted = append([]string(nil), p.listed...)
			p.index.Pending = nil
			if err := p.index.save(e.Dir, p.Container); err != nil {
				return res, err
			}
		}
		rr, err := e.afterChange(ctx, p.Container, true)
		if err != nil {
			return res, err
		}
		p.Restarted, p.RestartNote = rr.Restarted, rr.Note
		if rr.Note != "" {
			rest = append(rest, p.Container)
		}
	}
	// --yes allows the reboot a system container needs
	if e.Yes && !noApply && !e.NoRestart && len(rest) > 0 {
		ar, err := e.Apply(ctx, rest, false)
		if err != nil {
			return res, err
		}
		res.Apply = &ar
	}
	return res, nil
}

// ---- rm ----

type RmResult struct {
	Container   string   `json:"container"`
	Removed     []string `json:"removed"`
	Restarted   bool     `json:"restarted"`
	RestartNote string   `json:"restart_note,omitempty"`
}

// Remove deletes paths from the container's view and records them in the
// deleted list, so sync keeps them deleted. The local copy and its checkout
// index entry go too, so the tree, the list and the device agree.
func (e *Env) Remove(ctx context.Context, container string, paths []string, recursive bool) (RmResult, error) {
	r := RmResult{Container: container, Removed: []string{}}
	if _, err := RequireActive(ctx, e.C); err != nil {
		return r, err
	}
	clean := make([]string, 0, len(paths))
	for _, p := range paths {
		c, err := CleanRemotePath(p)
		if err != nil {
			return r, errf(KindUsage, err.Error())
		}
		clean = append(clean, c)
	}
	b := e.newBatch(len(clean))
	for _, p := range clean {
		e.progress("rm %s/%s", container, p)
		if err := e.C.Remove(b.next(ctx), container, p, recursive); err != nil {
			return r, err
		}
		if err := AddDeleted(e.Dir, container, p); err != nil {
			return r, err
		}
		if err := e.dropLocal(container, []string{p}); err != nil {
			return r, err
		}
		r.Removed = append(r.Removed, p)
	}
	rr, err := e.afterChange(ctx, container, len(r.Removed) > 0)
	r.Restarted, r.RestartNote = rr.Restarted, rr.Note
	return r, err
}

func ix2contains(list []string, p string) bool {
	for _, q := range list {
		if q == p {
			return true
		}
	}
	return false
}
