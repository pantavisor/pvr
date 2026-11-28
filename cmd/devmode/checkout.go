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
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// IndexName is the file in ./devmode/<container>/ that records what pull
// checked out. It is never pushed.
const IndexName = ".devmode-index"

const indexVersion = 1

// IndexEntry is one checked-out path as the device had it.
type IndexEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"` // file, dir or symlink
	Mode   string `json:"mode"` // octal, with setuid, setgid and sticky
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
}

func (e IndexEntry) mode() uint32 {
	m, _ := parseMode(e.Mode)
	return m & 0o7777
}

// Index is the content of .devmode-index.
type Index struct {
	Version int          `json:"version"`
	Files   []IndexEntry `json:"files"`
	// Roots are the paths pulls checked out: "." for the whole container.
	// An index without roots is a whole-container checkout.
	Roots []string `json:"roots,omitempty"`
	// Deleted lists the deleted-list paths already deleted on the device.
	Deleted []string `json:"deleted,omitempty"`
	// Pending lists paths a sync reset the container for but could not send;
	// the next sync or push sends them again.
	Pending []string `json:"pending,omitempty"`

	m map[string]IndexEntry
}

func newIndex() *Index { return &Index{Version: indexVersion, m: map[string]IndexEntry{}} }

func indexPath(root, container string) string {
	return filepath.Join(root, container, IndexName)
}

// loadIndex reads the index of a container; nil when there is none.
func loadIndex(root, container string) (*Index, error) {
	b, err := os.ReadFile(indexPath(root, container))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ix := newIndex()
	if err := json.Unmarshal(b, ix); err != nil {
		return nil, fmt.Errorf("%s: %v", IndexName, err)
	}
	if ix.Version > indexVersion {
		return nil, fmt.Errorf("%s is from a newer pvr (version %d)", IndexName, ix.Version)
	}
	ix.m = map[string]IndexEntry{}
	for _, f := range ix.Files {
		ix.m[f.Path] = f
	}
	return ix, nil
}

func (ix *Index) get(p string) (IndexEntry, bool) {
	e, ok := ix.m[p]
	return e, ok
}

func (ix *Index) set(e IndexEntry) { ix.m[e.Path] = e }

// drop forgets a path and everything below it.
func (ix *Index) drop(p string) {
	delete(ix.m, p)
	for k := range ix.m {
		if strings.HasPrefix(k, p+"/") {
			delete(ix.m, k)
		}
	}
}

func (ix *Index) paths() []string {
	out := make([]string, 0, len(ix.m))
	for k := range ix.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (ix *Index) markDeleted(p string) {
	for _, d := range ix.Deleted {
		if d == p {
			return
		}
	}
	ix.Deleted = append(ix.Deleted, p)
}

func (ix *Index) wasDeleted(p string) bool {
	for _, d := range ix.Deleted {
		if d == p {
			return true
		}
	}
	return false
}

// addRoot records a pulled path; the whole container ("." or "") absorbs the rest.
func (ix *Index) addRoot(scope string) {
	if scope == "" {
		scope = "."
	}
	if len(ix.Roots) == 1 && ix.Roots[0] == "." {
		return
	}
	if scope == "." {
		ix.Roots = []string{"."}
		return
	}
	for _, r := range ix.Roots {
		if r == scope {
			return
		}
	}
	ix.Roots = append(ix.Roots, scope)
}

// scopes lists what a plain pull refreshes: the pulled roots, in the form the
// device call takes ("" for the whole container).
func (ix *Index) scopes() []string {
	var out []string
	for _, r := range ix.Roots {
		if r == "." {
			return []string{""}
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// save writes the index next to the files, through a temporary file.
func (ix *Index) save(root, container string) error {
	ix.Version = indexVersion
	ix.Files = ix.Files[:0]
	for _, p := range ix.paths() {
		ix.Files = append(ix.Files, ix.m[p])
	}
	sort.Strings(ix.Deleted)
	b, err := json.MarshalIndent(ix, "", " ")
	if err != nil {
		return err
	}
	dst := indexPath(root, container)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// unixMode gives the permission bits with setuid, setgid and sticky.
func unixMode(m fs.FileMode) uint32 {
	v := uint32(m.Perm())
	if m&fs.ModeSetuid != 0 {
		v |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		v |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		v |= 0o1000
	}
	return v
}

// fileMode is the reverse of unixMode.
func fileMode(v uint32) fs.FileMode {
	m := fs.FileMode(v & 0o777)
	if v&0o4000 != 0 {
		m |= fs.ModeSetuid
	}
	if v&0o2000 != 0 {
		m |= fs.ModeSetgid
	}
	if v&0o1000 != 0 {
		m |= fs.ModeSticky
	}
	return m
}

// statLocal describes one path of a container folder without following symlinks.
func statLocal(base, rel string) (LocalFile, bool, error) {
	p := filepath.Join(base, filepath.FromSlash(rel))
	fi, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return LocalFile{}, false, nil
	}
	if err != nil {
		return LocalFile{}, false, err
	}
	l := LocalFile{Path: rel, Mode: unixMode(fi.Mode())}
	switch {
	case fi.IsDir():
		l.Type = "dir"
	case fi.Mode()&fs.ModeSymlink != 0:
		l.Type = "symlink"
		l.Target, err = os.Readlink(p)
	case fi.Mode().IsRegular():
		l.Type = "file"
		l.SHA256, l.Size, err = fileSHA(p)
	default:
		l.Type = "other"
	}
	return l, true, err
}

// sameAsEntry tells if a local path is what the entry says.
func sameAsEntry(l LocalFile, e IndexEntry) bool {
	if l.Type != e.Type {
		return false
	}
	switch l.Type {
	case "file":
		return l.SHA256 == e.SHA256 && l.Mode&0o7777 == e.mode()
	case "symlink":
		return l.Target == e.Target
	}
	return l.Type == "dir"
}

// sameEntries tells if two entries describe the same content and mode.
func sameEntries(a, b IndexEntry) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case "file":
		return a.SHA256 == b.SHA256 && a.mode() == b.mode()
	case "symlink":
		return a.Target == b.Target
	}
	return true
}

func entryOf(l LocalFile, uid, gid int) IndexEntry {
	return IndexEntry{Path: l.Path, Type: l.Type, Mode: fmt.Sprintf("%04o", l.Mode&0o7777), UID: uid, GID: gid,
		Size: l.Size, SHA256: l.SHA256, Target: l.Target}
}

// inScope tells if p is the scope path or below it; the empty scope is everything.
func inScope(scope, p string) bool {
	return scope == "" || p == scope || strings.HasPrefix(p, scope+"/")
}
