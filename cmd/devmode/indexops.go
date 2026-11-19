/*
 * Copyright (c) 2025 Pantacor Ltd.
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */
package devmode

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
)

// collapse keeps only the paths whose parent folder is not in the set too.
func collapse(paths []string) []string {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	var out []string
	for _, p := range paths {
		covered := false
		for q := p; ; {
			i := strings.LastIndex(q, "/")
			if i < 0 {
				break
			}
			q = q[:i]
			if set[q] {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// computeIndexDiff compares a container folder with its index. It does not
// talk to the device.
func computeIndexDiff(root, name string, ix *Index, local []LocalFile, list []string) (Diff, error) {
	d := Diff{Added: []Change{}, Changed: []Change{}, OnlyOnDevice: []Change{}, ToDelete: []Change{}, Deleted: []Change{}}
	base := filepath.Join(root, name)
	var gone []string
	for _, p := range ix.paths() {
		old := ix.m[p]
		l, ok, err := statLocal(base, p)
		if err != nil {
			return d, err
		}
		switch {
		case !ok:
			gone = append(gone, p)
		case l.Type != old.Type:
			d.Changed = append(d.Changed, Change{p, "type"})
		case l.Type == "symlink" && l.Target != old.Target:
			d.Changed = append(d.Changed, Change{p, "content"})
		case l.Type == "file" && l.SHA256 != old.SHA256:
			d.Changed = append(d.Changed, Change{p, "content"})
		case l.Type == "file" && l.Mode&0o7777 != old.mode():
			d.Changed = append(d.Changed, Change{p, "mode"})
		default:
			d.Unchanged++
		}
	}
	for _, p := range collapse(gone) {
		d.Deleted = append(d.Deleted, Change{Path: p})
	}
	for _, l := range local {
		if _, ok := ix.get(l.Path); !ok {
			d.Added = append(d.Added, Change{Path: l.Path})
		}
	}
	for _, p := range list {
		if !ix.wasDeleted(p) {
			d.ToDelete = append(d.ToDelete, Change{Path: p})
		}
	}
	for _, s := range [][]Change{d.Added, d.Changed, d.ToDelete} {
		sort.Slice(s, func(i, j int) bool { return s[i].Path < s[j].Path })
	}
	return d, nil
}

// ownerFor is the owner a pushed file gets: an edited file keeps the one the
// index recorded, a new file is root unless --keep-owner.
func (e *Env) ownerFor(name string, l LocalFile, ix *Index) (int, int) {
	if ix != nil {
		if old, ok := ix.get(l.Path); ok && old.Type == l.Type {
			return old.UID, old.GID
		}
	}
	if e.KeepOwner {
		if u, g, ok := localOwner(filepath.Join(e.Dir, name, filepath.FromSlash(l.Path))); ok {
			return u, g
		}
	}
	return 0, 0
}

// pushIndexed sends what differs from the index: new and changed files, and
// deletes for checked-out paths that are gone here.
func (e *Env) pushIndexed(ctx context.Context, name string, ix *Index) (r PushResult, err error) {
	r = PushResult{Container: name, Pushed: []string{}, Deleted: []string{}, Skipped: []Skip{}}
	defer func() {
		if serr := ix.save(e.Dir, name); err == nil && serr != nil {
			err = serr
		}
	}()
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
	d, err := computeIndexDiff(e.Dir, name, ix, local, list)
	if err != nil {
		return r, err
	}
	r.Unchanged = d.Unchanged
	base := filepath.Join(e.Dir, name)
	for _, ch := range append(append([]Change{}, d.Added...), d.Changed...) {
		l, ok, err := statLocal(base, ch.Path)
		if err != nil || !ok {
			return r, errf(KindGeneric, ch.Path+" changed while pushing")
		}
		uid, gid := e.ownerFor(name, l, ix)
		if err := e.putLocal(ctx, name, l, uid, gid); err != nil {
			return r, err
		}
		ix.set(entryOf(l, uid, gid))
		r.Pushed = append(r.Pushed, ch.Path)
	}
	for _, ch := range d.Deleted {
		if _, err := e.deleteListed(ctx, name, ch.Path); err != nil {
			return r, err
		}
		ix.drop(ch.Path)
		r.Deleted = append(r.Deleted, ch.Path)
	}
	for _, ch := range d.ToDelete {
		ok, err := e.deleteListed(ctx, name, ch.Path)
		if err != nil {
			return r, err
		}
		ix.markDeleted(ch.Path)
		if ok {
			r.Deleted = append(r.Deleted, ch.Path)
		}
	}
	return r, nil
}

// goneFromIndex lists the checked-out paths that are missing in the folder.
func goneFromIndex(root, name string, ix *Index) ([]string, error) {
	var gone []string
	for _, p := range ix.paths() {
		_, ok, err := statLocal(filepath.Join(root, name), p)
		if err != nil {
			return nil, err
		}
		if !ok {
			gone = append(gone, p)
		}
	}
	return collapse(gone), nil
}
