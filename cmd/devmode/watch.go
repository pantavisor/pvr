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
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Debounce is how long the tree must be quiet before a watch cycle runs.
const Debounce = 400 * time.Millisecond

func addTree(w *fsnotify.Watcher, root string) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && Ignored(d.Name()) {
				return filepath.SkipDir
			}
			_ = w.Add(p)
		}
		return nil
	})
}

// Watch pushes and applies every time the local tree changes, until ctx
// ends. System containers only get a reboot when e.Yes is set.
func (e *Env) Watch(ctx context.Context, names []string) error {
	if _, err := RequireActive(ctx, e.C); err != nil {
		return err
	}
	root, err := filepath.Abs(e.Dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	addTree(w, root)

	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	dirty := map[string]bool{}
	for _, n := range names {
		dirty[n] = true // first cycle syncs what is already different
	}
	if len(names) == 0 {
		l, _ := LocalContainers(root)
		for _, n := range l {
			dirty[n] = true
		}
	}
	e.progress("watching ./%s (Ctrl-C to stop)", e.Dir)

	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if b := filepath.Base(ev.Name); Ignored(b) && b != DeletedListName {
				continue
			}
			if ev.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					addTree(w, ev.Name)
				}
			}
			c, _, err := LocalToRemote(root, ev.Name)
			if err != nil || (len(want) > 0 && !want[c]) {
				// a change straight in the root or of a container directory
				continue
			}
			dirty[c] = true
			timer.Reset(Debounce)
		case err := <-w.Errors:
			e.progress("watch error: %v", err)
		case <-timer.C:
			if len(dirty) == 0 {
				continue
			}
			var batch []string
			for c := range dirty {
				batch = append(batch, c)
			}
			sort.Strings(batch)
			dirty = map[string]bool{}
			e.cycle(ctx, batch)
		}
	}
}

// cycle pushes and applies one batch; failures are reported and the watch
// goes on.
func (e *Env) cycle(ctx context.Context, batch []string) {
	// apply below restarts, or reboots a system container; push must not restart too
	keep := e.NoRestart
	e.NoRestart = true
	res, err := e.Push(ctx, batch)
	e.NoRestart = keep
	if err != nil {
		e.cycleError("push", err)
		return
	}
	e.emit("push", map[string]interface{}{"watch": true, "containers": res}, func(w2 io.Writer) { e.printPush(w2, res) })
	var changed []string
	for _, r := range res {
		if len(r.Pushed)+len(r.Deleted) > 0 {
			changed = append(changed, r.Container)
		}
	}
	if len(changed) == 0 {
		return
	}
	ar, err := e.Apply(ctx, changed, e.Yes)
	if err != nil {
		e.cycleError("apply", err)
		return
	}
	e.emit("apply", map[string]interface{}{"watch": true, "apply": ar}, func(w2 io.Writer) {
		for _, n := range ar.Restarted {
			fmt.Fprintf(w2, "restarted %s\n", n)
		}
		if ar.Reboot {
			fmt.Fprintf(w2, "device is rebooting; the watch stops until it is back\n")
		}
	})
}

func (e *Env) cycleError(cmd string, err error) {
	EmitError(e.Out, cmd, err, e.JSON)
}
