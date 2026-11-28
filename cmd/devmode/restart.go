// Copyright 2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package devmode

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// RestartResult tells if a container was restarted after a change.
type RestartResult struct {
	Container string `json:"container"`
	Restarted bool   `json:"restarted"`
	// Note says why a container was not restarted (a system container).
	Note string `json:"restart_note,omitempty"`
}

// RestartAnswer is what a device agent says about the restart that went with
// a change.
type RestartAnswer struct {
	Restarted bool
	Note      string
}

// selfRestarter is a backend whose change calls restart the container
// themselves (the Hub transport).
type selfRestarter interface {
	// TakeRestart gives the answer kept from the last change of the
	// container; false when the device did not say.
	TakeRestart(container string) (RestartAnswer, bool)
}

type restartCtxKey struct{}

// restartMode tells a backend what to do with the restart of one change
// call: restart is false when the user asked for --no-restart; final is true
// for the last call of a batch, the only one that may restart.
type restartMode struct{ restart, final bool }

func (e *Env) opCtx(ctx context.Context, final bool) context.Context {
	return context.WithValue(ctx, restartCtxKey{}, restartMode{restart: !e.NoRestart, final: final})
}

func modeOf(ctx context.Context) (restartMode, bool) {
	m, ok := ctx.Value(restartCtxKey{}).(restartMode)
	return m, ok
}

// batch hands out the context of each of n change calls.
type batch struct {
	e     *Env
	total int
	done  int
}

func (e *Env) newBatch(total int) *batch { return &batch{e: e, total: total} }

func (b *batch) next(ctx context.Context) context.Context {
	b.done++
	return b.e.opCtx(ctx, b.done >= b.total)
}

func systemNote(container string) string {
	return container + " is a system container: the change shows after it restarts, which reboots the device; restart it with pvr devmode apply <device> " + container + " --yes"
}

// restartApp restarts an app container the way apply does and waits for it.
// reboot is true when the device wants a reboot for it instead.
func (e *Env) restartApp(ctx context.Context, n string) (st ApplyState, reboot bool, err error) {
	var before ContainerState
	if !e.NoWait {
		// a container still restarting from an earlier apply cannot restart again
		e.progress("waiting for %s to be STARTED", n)
		before, err = e.waitContainer(ctx, n, "start", func(s ContainerState) bool { return s.Status == "STARTED" })
		if err != nil {
			return st, false, err
		}
	}
	e.progress("restart %s", n)
	t0 := time.Now()
	err = e.C.Apply(ctx, n, false)
	if ae, ok := err.(*Error); ok && ae.Kind == KindRebootNeeded {
		return st, true, nil
	}
	if err != nil {
		return st, false, err
	}
	state := "restart_requested"
	if !e.NoWait {
		if _, err := e.waitContainer(ctx, n, "restart", func(s ContainerState) bool {
			return s.Status == "STARTED" && !s.Same(before)
		}); err != nil {
			return st, false, err
		}
		state = "STARTED"
	}
	secs := time.Since(t0).Seconds()
	e.progress("%s is %s after %.1fs", n, state, secs)
	return ApplyState{n, state, secs}, false, nil
}

// afterChange restarts a container whose files were changed: an app
// container is restarted, a system container is not (that is a device
// reboot). A backend that restarts on its own is only asked what it did.
func (e *Env) afterChange(ctx context.Context, n string, changed bool) (RestartResult, error) {
	r := RestartResult{Container: n}
	if !changed || e.NoRestart {
		return r, nil
	}
	if sr, ok := e.C.(selfRestarter); ok {
		if a, ok := sr.TakeRestart(n); ok {
			r.Restarted, r.Note = a.Restarted, a.Note
			return r, nil
		}
	}
	policies, _ := e.C.RestartPolicies(ctx)
	if policies[n] == "system" {
		r.Note = systemNote(n)
		return r, nil
	}
	_, reboot, err := e.restartApp(ctx, n)
	if err != nil {
		return r, err
	}
	if reboot {
		r.Note = systemNote(n)
		return r, nil
	}
	r.Restarted = true
	return r, nil
}

func (r RestartResult) print(w io.Writer, noRestart bool) {
	switch {
	case r.Restarted:
		fmt.Fprintf(w, "%s: restarted\n", r.Container)
	case r.Note != "":
		// The device's note may name MCP options, so pvr says it in its own words.
		fmt.Fprintf(w, "%s: not restarted, it is a system container and its restart reboots the device; run pvr devmode apply <device> %s --yes\n", r.Container, r.Container)
	case noRestart:
		fmt.Fprintf(w, "%s: not restarted (--no-restart); run pvr devmode apply <device> %s to restart it\n", r.Container, r.Container)
	}
}

// dropLocal removes the local copy of a path the device deleted, and its
// checkout index entries, so the tree, the deleted list and the device agree.
func (e *Env) dropLocal(container string, paths []string) error {
	ix, err := loadIndex(e.Dir, container)
	if err != nil {
		return errf(KindGeneric, err.Error())
	}
	for _, p := range paths {
		if err := os.RemoveAll(filepath.Join(e.Dir, container, filepath.FromSlash(p))); err != nil {
			return err
		}
		if ix != nil {
			ix.drop(p)
			ix.markDeleted(p)
		}
	}
	if ix != nil {
		return ix.save(e.Dir, container)
	}
	return nil
}
