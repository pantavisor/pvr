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

package devmode

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PutItem is one file, directory or symlink to upload.
type PutItem struct {
	Path string
	Opts PutOpts
	Size int64
	// Open gives the content of a file; nil for a directory or symlink.
	Open func() (io.ReadCloser, error)
}

// BatchOp is one change of a batch: an upload or the removal of a path.
type BatchOp struct {
	Put    *PutItem
	Remove string
}

// Batcher is a backend that sends many changes in few calls. It returns how
// many leading ops were applied. wrap runs one call and retries it while the
// device is busy.
type Batcher interface {
	ApplyBatch(ctx context.Context, container string, ops []BatchOp, wrap func(func() error) error) (int, error)
}

// isBusy tells if the device refused a command for now: it has too many, or
// it is still switching revisions (right after enter or leave).
func isBusy(err error) bool {
	e, ok := err.(*Error)
	return ok && (e.Code == "BUSY" || strings.HasPrefix(e.Msg, "BUSY") ||
		strings.Contains(e.Msg, "too many devmode commands") ||
		strings.Contains(e.Msg, "while update is ongoing"))
}

const (
	busyBaseDefault  = time.Second
	busyTotalDefault = 60 * time.Second
)

// retryBusy runs fn again, with a growing pause, while the device says it is
// busy; it gives up after about a minute.
func (e *Env) retryBusy(ctx context.Context, fn func() error) error {
	base, total := e.BusyBase, e.BusyTotal
	if base <= 0 {
		base = busyBaseDefault
	}
	if total <= 0 {
		total = busyTotalDefault
	}
	delay, waited := base, time.Duration(0)
	for {
		err := fn()
		if err == nil || !isBusy(err) {
			return err
		}
		if waited >= total {
			return errf(KindGeneric, fmt.Sprintf("the device stayed busy for %s: %s", waited.Round(time.Millisecond), err.Error()))
		}
		if waited == 0 {
			e.progress("the device is busy, waiting and trying again")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		waited += delay
		if delay < 8*base {
			delay *= 2
		}
	}
}

// putItem makes the upload of one local file, symlink or directory.
func (e *Env) putItem(name string, l LocalFile, uid, gid int) *PutItem {
	it := &PutItem{Path: l.Path, Size: l.Size, Opts: PutOpts{Mode: l.Mode, Type: l.Type, Target: l.Target, UID: uid, GID: gid}}
	if l.Type == "file" {
		local := filepath.Join(e.Dir, name, filepath.FromSlash(l.Path))
		it.Open = func() (io.ReadCloser, error) { return os.Open(local) }
	}
	return it
}

// sendOps applies the ops of one container, the last one with the restart of
// the command. It returns how many ops were applied.
func (e *Env) sendOps(ctx context.Context, name string, ops []BatchOp) (int, error) {
	if len(ops) == 0 {
		return 0, nil
	}
	if b, ok := e.C.(Batcher); ok {
		e.progress("send %d changes to %s", len(ops), name)
		return b.ApplyBatch(e.opCtx(ctx, true), name, ops, func(fn func() error) error { return e.retryBusy(ctx, fn) })
	}
	for i, op := range ops {
		octx := e.opCtx(ctx, i == len(ops)-1)
		var err error
		if op.Put != nil {
			err = e.putItemNow(octx, name, op.Put)
		} else {
			_, err = e.deleteListed(octx, name, op.Remove)
		}
		if err != nil {
			return i, err
		}
	}
	return len(ops), nil
}

// pushShort turns a failed batch into an error that says how many changes
// reached the device and which one is first among those still pending.
func (e *Env) pushShort(name string, total, sent int, paths []string, err error) error {
	if err == nil {
		return nil
	}
	first := ""
	if sent < len(paths) {
		first = ", first " + paths[sent]
	}
	kind := KindGeneric
	if ee, ok := err.(*Error); ok {
		kind = ee.Kind
	}
	return errf(kind, fmt.Sprintf("only %d of %d changes reached %s (%d pending%s): %s; run pvr devmode push again to send the rest",
		sent, total, name, total-sent, first, err.Error()))
}

func (e *Env) putItemNow(ctx context.Context, name string, it *PutItem) error {
	return e.retryBusy(ctx, func() error {
		var body io.Reader
		if it.Open != nil {
			f, err := it.Open()
			if err != nil {
				return err
			}
			defer f.Close()
			body = f
		}
		if it.Opts.Type == "file" || it.Opts.Type == "" {
			e.progress("push %s/%s", name, it.Path)
		} else {
			e.progress("push %s/%s (%s)", name, it.Path, it.Opts.Type)
		}
		return e.C.Put(ctx, name, it.Path, body, it.Opts)
	})
}

// uploadSet picks the local paths a sync has to send. The device's own image
// has everything pull wrote unchanged, so only these are sent: paths the
// index does not know, paths that differ from it, paths the device holds in
// its changes now (the reset empties them) and paths an earlier sync did not
// finish.
func uploadSet(local []LocalFile, ix *Index, inlay map[string]bool) []LocalFile {
	pending := map[string]bool{}
	for _, p := range ix.Pending {
		pending[p] = true
	}
	var out []LocalFile
	for _, l := range local {
		old, ok := ix.get(l.Path)
		if !ok || !sameAsEntry(l, old) || inlay[l.Path] || pending[l.Path] {
			out = append(out, l)
		}
	}
	return out
}
