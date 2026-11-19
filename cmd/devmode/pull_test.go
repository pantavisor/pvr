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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readF(t *testing.T, e *Env, c, p string) string {
	b, err := os.ReadFile(filepath.Join(e.Dir, c, filepath.FromSlash(p)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustIndex(t *testing.T, e *Env, c string) *Index {
	ix, err := loadIndex(e.Dir, c)
	if err != nil || ix == nil {
		t.Fatalf("index: %v %v", ix, err)
	}
	return ix
}

func pull(t *testing.T, e *Env, force bool, paths ...string) PullResult {
	r, err := e.Pull(context.Background(), []string{"a"}, paths, force)
	if err != nil {
		t.Fatal(err)
	}
	return r[0]
}

func TestPullCheckout(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": sampleTar(t)}}
	e, _ := newEnv(t, f)
	r := pull(t, e, false)
	if r.Method != "tar" || len(r.Written) != 9 || len(r.Skipped) != 0 {
		t.Fatalf("result %+v", r)
	}
	if readF(t, e, "a", "etc/nginx/nginx.conf") != "worker 1;\n" {
		t.Error("content")
	}
	fi, err := os.Lstat(filepath.Join(e.Dir, "a", "usr/bin/su"))
	if err != nil || fi.Mode()&os.ModeSetuid == 0 || fi.Mode().Perm() != 0o755 {
		t.Errorf("setuid file: %v %v", fi, err)
	}
	if l, err := os.Readlink(filepath.Join(e.Dir, "a", "usr/bin/sh")); err != nil || l != "run.sh" {
		t.Errorf("symlink %q %v", l, err)
	}
	long := strings.Repeat("d", 60) + "/" + strings.Repeat("e", 60) + "/file.txt"
	if readF(t, e, "a", long) != "long" {
		t.Error("long name")
	}
	ix := mustIndex(t, e, "a")
	if got := len(ix.Files); got != 9 {
		t.Errorf("index has %d entries", got)
	}
	if en, _ := ix.get("etc/nginx/nginx.conf"); en.Type != "file" || en.Mode != "0644" || en.UID != 33 || en.GID != 33 || en.Size != 10 || len(en.SHA256) != 64 {
		t.Errorf("entry %+v", en)
	}
	if en, _ := ix.get("usr/bin/su"); en.Mode != "4755" {
		t.Errorf("setuid entry %+v", en)
	}
	if en, _ := ix.get("usr/bin/sh"); en.Type != "symlink" || en.Target != "run.sh" {
		t.Errorf("symlink entry %+v", en)
	}
	if en, _ := ix.get("etc/nginx"); en.Type != "dir" || en.Mode != "0750" {
		t.Errorf("dir entry %+v", en)
	}
	// nothing differs right after a pull
	if r := pull(t, e, false); len(r.Written) != 0 || len(r.Conflicts) != 0 || r.Unchanged != 9 {
		t.Errorf("second pull %+v", r)
	}
}

func TestPullRefusesUnsafeNames(t *testing.T) {
	outside := t.TempDir()
	f := &fakeDev{tars: map[string][]byte{"a": buildTar(t,
		tent{name: "/abs/file", body: "x", mode: 0o644},
		tent{name: "../escape", body: "x", mode: 0o644},
		tent{name: "a/../../escape2", body: "x", mode: 0o644},
		tent{name: "link", typ: tar.TypeSymlink, mode: 0o777, link: outside},
		tent{name: "link/evil", body: "x", mode: 0o644},
		tent{name: "hard", typ: tar.TypeLink, link: "ok"},
		tent{name: "dev/null", typ: tar.TypeChar, mode: 0o666},
		tent{name: "ok", body: "fine", mode: 0o644},
	)}}
	e, _ := newEnv(t, f)
	r := pull(t, e, false)
	if len(r.Skipped) != 6 {
		t.Errorf("skipped %+v", r.Skipped)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Errorf("wrote through a symlink: %v", ents)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "escape")); err == nil {
		t.Error("wrote outside the folder")
	}
	if readF(t, e, "a", "ok") != "fine" {
		t.Error("the safe file is missing")
	}
	// a second pull must not write through the symlink it made
	pull(t, e, false)
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Errorf("wrote through a symlink on the second pull: %v", ents)
	}
}

func TestPullConflictAndForce(t *testing.T) {
	v1 := buildTar(t,
		tent{name: "etc/", typ: tar.TypeDir, mode: 0o755},
		tent{name: "etc/a.conf", mode: 0o644, body: "a1"},
		tent{name: "etc/b.conf", mode: 0o644, body: "b1"},
		tent{name: "etc/c.conf", mode: 0o644, body: "c1"},
		tent{name: "etc/d.conf", mode: 0o644, body: "d1"},
		tent{name: "etc/e.conf", mode: 0o644, body: "e1"},
	)
	f := &fakeDev{tars: map[string][]byte{"a": v1}}
	e, _ := newEnv(t, f)
	pull(t, e, false)
	put(t, e, "a", "etc/a.conf", "a-local") // changed here, changed on the device
	put(t, e, "a", "etc/b.conf", "b-local") // changed here only
	put(t, e, "a", "etc/e.conf", "e-local") // changed here, gone on the device
	// c changes on the device only; d is gone on the device
	f.tars["a"] = buildTar(t,
		tent{name: "etc/", typ: tar.TypeDir, mode: 0o755},
		tent{name: "etc/a.conf", mode: 0o644, body: "a2"},
		tent{name: "etc/b.conf", mode: 0o644, body: "b1"},
		tent{name: "etc/c.conf", mode: 0o644, body: "c2"},
		tent{name: "etc/new.conf", mode: 0o644, body: "n"},
	)
	r := pull(t, e, false)
	if len(r.Conflicts) != 2 || r.Conflicts[0].Path != "etc/a.conf" && r.Conflicts[1].Path != "etc/a.conf" {
		t.Errorf("conflicts %+v", r.Conflicts)
	}
	if readF(t, e, "a", "etc/a.conf") != "a-local" || readF(t, e, "a", "etc/b.conf") != "b-local" || readF(t, e, "a", "etc/e.conf") != "e-local" {
		t.Error("a local change was overwritten")
	}
	if readF(t, e, "a", "etc/c.conf") != "c2" || readF(t, e, "a", "etc/new.conf") != "n" {
		t.Error("device changes did not arrive")
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "a", "etc/d.conf")); err == nil || len(r.Removed) != 1 || r.Removed[0] != "etc/d.conf" {
		t.Errorf("gone file not removed: %+v", r.Removed)
	}
	if len(r.Kept) != 1 || r.Kept[0] != "etc/b.conf" {
		t.Errorf("kept %v", r.Kept)
	}
	// the index still says what the device had at the first pull for the conflicts
	if en, _ := mustIndex(t, e, "a").get("etc/a.conf"); en.SHA256 == "" || en.Size != 2 {
		t.Errorf("index %+v", en)
	}
	r = pull(t, e, true)
	if len(r.Conflicts) != 0 || readF(t, e, "a", "etc/a.conf") != "a2" || readF(t, e, "a", "etc/b.conf") != "b1" {
		t.Errorf("force: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "a", "etc/e.conf")); err == nil {
		t.Error("force kept a file that is gone on the device")
	}
}

func TestPullFallsBackOnOldDevice(t *testing.T) {
	m := `{"container":"a","files":[{"path":"etc","type":"dir","mode":"0755"},{"path":"usr/doc","type":"deleted"}]}`
	f := &fakeDev{noTar: true, manifests: map[string]string{"a": m}}
	e, errb := newEnv(t, f)
	put(t, e, "a", IndexName, `{"version":1,"files":[]}`)
	r := pull(t, e, false)
	if r.Method != "delta" || len(r.DeletedList) != 1 {
		t.Errorf("result %+v", r)
	}
	if !strings.Contains(errb.String(), "cannot send a folder as a tar") {
		t.Errorf("no notice: %q", errb.String())
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "a", IndexName)); err == nil {
		t.Error("a stale index is left after a pull without tar")
	}
}

func hasCmd(f *fakeDev, c string) bool {
	for _, x := range f.cmds {
		if x == c {
			return true
		}
	}
	return false
}

func TestPullAgainRefreshesOnlyWhatWasPulled(t *testing.T) {
	conf := buildTar(t, tent{name: "etc/nginx/nginx.conf", mode: 0o644, uid: 33, gid: 33, body: "worker 1;\n"})
	f := &fakeDev{tars: map[string][]byte{"a": sampleTar(t), "a etc/nginx/nginx.conf": conf}}
	e, _ := newEnv(t, f)
	whole := "pvcontrol devmode files get a --tar"
	one := "pvcontrol devmode files get a etc/nginx/nginx.conf --tar"

	// a single file, then a plain pull again: only that file is asked for
	pull(t, e, false, "etc/nginx/nginx.conf")
	if got := mustIndex(t, e, "a").Roots; len(got) != 1 || got[0] != "etc/nginx/nginx.conf" {
		t.Fatalf("roots %v", got)
	}
	f.cmds = nil
	pull(t, e, false)
	if hasCmd(f, whole) || !hasCmd(f, one) {
		t.Fatalf("plain pull asked for %v", f.cmds)
	}
	if _, err := os.Stat(filepath.Join(e.Dir, "a", "usr/bin/run.sh")); err == nil {
		t.Error("a plain pull checked out the whole container")
	}

	// --all takes the whole container and makes it the root
	if _, err := e.PullWith(context.Background(), []string{"a"}, nil, false, true); err != nil {
		t.Fatal(err)
	}
	if got := mustIndex(t, e, "a").Roots; len(got) != 1 || got[0] != "." {
		t.Fatalf("roots after --all %v", got)
	}
	if readF(t, e, "a", "usr/bin/run.sh") != "#!/bin/sh\n" {
		t.Error("--all did not check out the container")
	}
	f.cmds = nil
	pull(t, e, false)
	if !hasCmd(f, whole) || hasCmd(f, one) {
		t.Errorf("plain pull of a whole checkout asked for %v", f.cmds)
	}
}

func TestPullAgainOldIndexWithoutRoots(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": sampleTar(t), "a etc/nginx/nginx.conf": sampleTar(t)}}
	e, _ := newEnv(t, f)
	pull(t, e, false)
	ix := mustIndex(t, e, "a")
	ix.Roots = nil // an index from before roots were recorded
	if err := ix.save(e.Dir, "a"); err != nil {
		t.Fatal(err)
	}
	f.cmds = nil
	if r := pull(t, e, false); r.Unchanged != 9 {
		t.Errorf("result %+v", r)
	}
	if !hasCmd(f, "pvcontrol devmode files get a --tar") {
		t.Errorf("an index without roots was not pulled whole: %v", f.cmds)
	}
	if got := mustIndex(t, e, "a").Roots; len(got) != 1 || got[0] != "." {
		t.Errorf("roots %v", got)
	}
}
