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
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// cleanTarName checks the name of a tar entry. An empty result is the root
// folder itself.
func cleanTarName(n string) (string, string) {
	n = strings.TrimSuffix(strings.TrimPrefix(n, "./"), "/")
	if n == "" || n == "." {
		return "", ""
	}
	switch {
	case strings.HasPrefix(n, "/"):
		return "", "absolute name"
	case strings.ContainsAny(n, "\\\x00\n\r"):
		return "", "unusable character in the name"
	case path.Clean(n) != n || n == ".." || strings.HasPrefix(n, "../"):
		return "", "name goes outside the folder"
	}
	return n, ""
}

// pullState is one pull of one container.
type pullState struct {
	base  string
	ix    *Index
	force bool
	res   *PullResult
	seen  map[string]bool
	dirs  map[string]bool // parents already checked
	root  bool
}

// prepareParent checks that no parent of rel is a symlink or a file and
// creates the missing ones.
func (st *pullState) prepareParent(rel string) string {
	dir := path.Dir(rel)
	if dir == "." {
		return ""
	}
	if !st.dirs[dir] {
		cur := st.base
		for _, c := range strings.Split(dir, "/") {
			cur = filepath.Join(cur, c)
			fi, err := os.Lstat(cur)
			if os.IsNotExist(err) {
				break
			}
			if err != nil {
				return err.Error()
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				return "the path goes through a symlink"
			}
			if !fi.IsDir() {
				return "a file is in the way of the folder " + strings.TrimPrefix(cur, st.base+string(filepath.Separator))
			}
		}
		if err := os.MkdirAll(filepath.Join(st.base, filepath.FromSlash(dir)), 0o755); err != nil {
			return err.Error()
		}
		st.dirs[dir] = true
	}
	return ""
}

func (st *pullState) skip(p, why string) {
	st.res.Skipped = append(st.res.Skipped, Skip{p, why})
}

func (st *pullState) conflict(p, why string) {
	st.res.Conflicts = append(st.res.Conflicts, Skip{p, why})
}

type pullAct int

const (
	actWrite pullAct = iota
	actAdopt         // local already matches the device: only the index changes
	actKeep          // the local state stays, the device did not change
	actConflict
)

// decide says what to do with an entry of the device given the local path
// and the index.
func (st *pullState) decide(dev IndexEntry) (pullAct, string, error) {
	loc, exists, err := statLocal(st.base, dev.Path)
	if err != nil {
		return 0, "", err
	}
	old, tracked := st.ix.get(dev.Path)
	if st.force {
		return actWrite, "", nil
	}
	if exists && loc.Type == "dir" && dev.Type != "dir" {
		return actConflict, "a folder here, not on the device", nil
	}
	switch {
	case !exists && !tracked:
		return actWrite, "", nil
	case !exists:
		if sameEntries(old, dev) {
			return actKeep, "", nil
		}
		return actConflict, "deleted here, changed on the device", nil
	case !tracked:
		if sameAsEntry(loc, dev) {
			return actAdopt, "", nil
		}
		return actConflict, "exists here and differs from the device", nil
	}
	devSame := sameEntries(old, dev)
	switch {
	case sameAsEntry(loc, old):
		if devSame {
			return actAdopt, "", nil
		}
		return actWrite, "", nil
	case devSame:
		return actKeep, "", nil
	case sameAsEntry(loc, dev):
		return actAdopt, "", nil
	}
	return actConflict, "changed here and on the device", nil
}

func (st *pullState) chown(full string, e IndexEntry) {
	if st.root {
		_ = os.Lchown(full, e.UID, e.GID)
	}
}

func (st *pullState) entry(h *tar.Header, rel, typ string) IndexEntry {
	return IndexEntry{Path: rel, Type: typ, Mode: fmt.Sprintf("%04o", h.Mode&0o7777), UID: h.Uid, GID: h.Gid, Target: h.Linkname}
}

// apply carries out the decision for one entry; tmp is the downloaded file
// of a regular file.
func (st *pullState) apply(dev IndexEntry, h *tar.Header, tmp string) error {
	full := filepath.Join(st.base, filepath.FromSlash(dev.Path))
	act, why, err := st.decide(dev)
	if err != nil {
		return err
	}
	switch act {
	case actConflict:
		st.conflict(dev.Path, why)
		return nil
	case actKeep:
		st.res.Kept = append(st.res.Kept, dev.Path)
		return nil
	case actAdopt:
		st.ix.set(dev)
		st.res.Unchanged++
		return nil
	}
	if loc, ok, _ := statLocal(st.base, dev.Path); ok && (loc.Type == "dir") != (dev.Type == "dir") {
		if err := os.RemoveAll(full); err != nil {
			return err
		}
	}
	switch dev.Type {
	case "dir":
		if err := os.MkdirAll(full, 0o755); err != nil {
			return err
		}
		st.chown(full, dev)
		_ = os.Chmod(full, fileMode(dev.mode())|0o700)
	case "symlink":
		_ = os.Remove(full)
		if err := os.Symlink(dev.Target, full); err != nil {
			return err
		}
		st.chown(full, dev)
	case "file":
		st.chown(tmp, dev) // before the mode: chown drops setuid
		if err := os.Chmod(tmp, fileMode(dev.mode())); err != nil {
			return err
		}
		if !h.ModTime.IsZero() {
			_ = os.Chtimes(tmp, time.Now(), h.ModTime)
		}
		if err := os.Rename(tmp, full); err != nil {
			return err
		}
	}
	st.ix.set(dev)
	st.res.Written = append(st.res.Written, dev.Path)
	return nil
}

// unpack reads one tar and applies every entry.
func (st *pullState) unpack(r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			if e, ok := err.(*Error); ok {
				return e
			}
			return errf(KindGeneric, "cannot read the tar: "+err.Error())
		}
		rel, why := cleanTarName(h.Name)
		if why != "" {
			st.skip(h.Name, why)
			continue
		}
		if rel == "" {
			continue
		}
		var typ string
		switch h.Typeflag {
		case tar.TypeDir:
			typ = "dir"
		case tar.TypeReg:
			typ = "file"
		case tar.TypeSymlink:
			typ = "symlink"
		case tar.TypeLink:
			st.skip(rel, "hard link")
			continue
		default:
			st.skip(rel, "special file (device node, socket or pipe)")
			continue
		}
		if why := st.prepareParent(rel); why != "" {
			st.skip(rel, why)
			continue
		}
		st.seen[rel] = true
		dev := st.entry(h, rel, typ)
		tmp := ""
		if typ == "file" {
			sum, size, name, err := st.download(tr, rel)
			if err != nil {
				return err
			}
			dev.SHA256, dev.Size, tmp = sum, size, name
		}
		err = st.apply(dev, h, tmp)
		if tmp != "" {
			os.Remove(tmp)
		}
		if err != nil {
			return err
		}
	}
}

// download writes the body of a file next to its place and returns its sum.
func (st *pullState) download(r io.Reader, rel string) (string, int64, string, error) {
	f, err := os.CreateTemp(filepath.Join(st.base, filepath.FromSlash(path.Dir(rel))), ".pvr-pull-")
	if err != nil {
		return "", 0, "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		if e, ok := err.(*Error); ok {
			return "", 0, "", e
		}
		return "", 0, "", errf(KindGeneric, rel+": "+err.Error())
	}
	return hex.EncodeToString(h.Sum(nil)), n, f.Name(), nil
}

// removeGone deletes what the device no longer has, below scope, unless it
// was changed here.
func (st *pullState) removeGone(scope string) {
	paths := st.ix.paths()
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	for _, p := range paths {
		if !inScope(scope, p) || st.seen[p] {
			continue
		}
		old, _ := st.ix.get(p)
		loc, exists, err := statLocal(st.base, p)
		switch {
		case err != nil:
			st.skip(p, err.Error())
		case !exists:
			st.ix.drop(p)
		case !st.force && !sameAsEntry(loc, old):
			st.conflict(p, "changed here, gone on the device")
		default:
			full := filepath.Join(st.base, filepath.FromSlash(p))
			var rerr error
			if old.Type == "dir" && !st.force {
				rerr = os.Remove(full) // a folder with other files stays
			} else {
				rerr = os.RemoveAll(full)
			}
			if rerr != nil {
				st.conflict(p, "cannot remove: "+rerr.Error())
				continue
			}
			st.ix.drop(p)
			st.res.Removed = append(st.res.Removed, p)
		}
	}
}

// pullTree checks out the container, or the given paths of it, from a tar.
// Without paths, a checkout that has an index is refreshed only where it was
// pulled before; all asks for the whole container.
func (e *Env) pullTree(ctx context.Context, name string, scopes []string, force, all bool, r *PullResult) (err error) {
	ix, err := loadIndex(e.Dir, name)
	if err != nil {
		return errf(KindGeneric, err.Error())
	}
	if ix == nil {
		ix = newIndex()
	} else if len(ix.Roots) == 0 {
		ix.Roots = []string{"."} // written before roots were recorded
	}
	if all {
		scopes = nil
	} else if len(scopes) == 0 {
		scopes = ix.scopes()
	}
	base := filepath.Join(e.Dir, name)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	st := &pullState{base: base, ix: ix, force: force, res: r, seen: map[string]bool{}, dirs: map[string]bool{}, root: os.Geteuid() == 0}
	defer func() {
		if serr := ix.save(e.Dir, name); err == nil && serr != nil {
			err = serr
		}
	}()
	if len(scopes) == 0 {
		scopes = []string{""}
	}
	for _, sc := range scopes {
		e.progress("pull %s/%s", name, strings.TrimSuffix(sc, "/"))
		rc, err := e.C.Tree(ctx, name, sc)
		if err != nil {
			return err
		}
		err = st.unpack(rc)
		if err == nil {
			_, err = io.Copy(io.Discard, rc) // the end of the stream checks the sum
		}
		rc.Close()
		if err != nil {
			return err
		}
		st.removeGone(sc)
		ix.addRoot(sc)
	}
	return nil
}
