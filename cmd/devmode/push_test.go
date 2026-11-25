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

// calls returns the commands that start with prefix, in order.
func calls(f *fakeDev, prefix string) []string {
	var out []string
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			out = append(out, strings.TrimPrefix(c, "pvcontrol devmode files "))
		}
	}
	return out
}

func eq(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func TestPushSendsOnlyTheChanges(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": sampleTar(t)}}
	e, _ := newEnv(t, f)
	pull(t, e, false)
	f.cmds = nil
	base := filepath.Join(e.Dir, "a")

	// before any edit nothing is pushed
	if res, err := e.Push(context.Background(), []string{"a"}); err != nil || len(res[0].Pushed)+len(res[0].Deleted) != 0 {
		t.Fatalf("push of a clean checkout: %+v %v", res, err)
	}
	if n := len(calls(f, "pvcontrol devmode files ")); n != 0 {
		t.Errorf("a clean push called the device: %v", f.cmds)
	}

	put(t, e, "a", "etc/nginx/nginx.conf", "worker 4;\n")      // content
	os.Chmod(filepath.Join(base, "usr/bin/run.sh"), 0o700)     // mode only
	os.Remove(filepath.Join(base, "usr/bin/su"))               // deleted
	os.Remove(filepath.Join(base, "usr/bin/sh"))               // symlink changed
	os.Symlink("other", filepath.Join(base, "usr/bin/sh"))     //
	put(t, e, "a", "etc/new.conf", "n")                        // added
	put(t, e, "a", DeletedListName, "")                        // never pushed
	os.RemoveAll(filepath.Join(base, strings.Repeat("d", 60))) // a long path
	long := strings.Repeat("d", 60)

	st, err := e.Status(context.Background(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	d := st.Containers[0].Diff
	if !st.Containers[0].Checkout || len(d.Added) != 1 || len(d.Changed) != 3 || len(d.Deleted) != 2 {
		t.Errorf("status %+v", d)
	}
	if len(calls(f, "pvcontrol devmode files ls")) != 0 {
		t.Error("status asked the device for its files")
	}

	res, err := e.Push(context.Background(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "pushed", res[0].Pushed, []string{"etc/new.conf", "etc/nginx/nginx.conf", "usr/bin/run.sh", "usr/bin/sh"})
	eq(t, "puts", calls(f, "pvcontrol devmode files put"), []string{
		"put a etc/new.conf - --uid 0 --gid 0 --mode 0644",
		"put a etc/nginx/nginx.conf - --uid 33 --gid 33 --mode 0644",
		"put a usr/bin/run.sh - --uid 0 --gid 0 --mode 0700",
		"put a usr/bin/sh --type symlink --target other --uid 0 --gid 0",
	})
	eq(t, "removes", calls(f, "pvcontrol devmode files rm"), []string{
		"rm a " + long + "/" + strings.Repeat("e", 60) + "/file.txt --recursive",
		"rm a usr/bin/su --recursive",
	})
	ix := mustIndex(t, e, "a")
	if en, _ := ix.get("etc/nginx/nginx.conf"); en.UID != 33 || en.Size != 10 {
		t.Errorf("index after push %+v", en)
	}
	if _, ok := ix.get("usr/bin/su"); ok {
		t.Error("a deleted path is still in the index")
	}
	if _, ok := ix.get("etc/new.conf"); !ok {
		t.Error("a new file is not in the index")
	}

	// the second push has nothing to send
	f.cmds = nil
	if _, err := e.Push(context.Background(), []string{"a"}); err != nil || len(calls(f, "pvcontrol devmode files ")) != 0 {
		t.Errorf("second push: %v %v", err, f.cmds)
	}
}

// One file: pull it, edit it, push it back, and add a second one later.
func TestSingleFileFlow(t *testing.T) {
	one := func(name, body string) []byte {
		return buildTar(t, tent{name: name, mode: 0o640, uid: 33, gid: 33, body: body})
	}
	f := &fakeDev{tars: map[string][]byte{
		"a etc/nginx/nginx.conf": one("etc/nginx/nginx.conf", "worker 1;\n"),
		"a usr/bin/run.sh":       one("usr/bin/run.sh", "#!/bin/sh\n"),
	}}
	e, _ := newEnv(t, f)
	base := filepath.Join(e.Dir, "a")

	r := pull(t, e, false, "etc/nginx/nginx.conf")
	if len(r.Written) != 1 || len(r.Removed) != 0 {
		t.Fatalf("pull %+v", r)
	}
	ix := mustIndex(t, e, "a")
	if len(ix.Files) != 1 || ix.Files[0].Path != "etc/nginx/nginx.conf" {
		t.Fatalf("index %+v", ix.Files)
	}
	// the parent folders exist but are not recorded as checked out
	if fi, err := os.Stat(filepath.Join(base, "etc/nginx")); err != nil || !fi.IsDir() {
		t.Error("no parent folder")
	}
	put(t, e, "a", "etc/nginx/nginx.conf", "worker 8;\n")
	f.cmds = nil
	res, err := e.Push(context.Background(), []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "pushed", res[0].Pushed, []string{"etc/nginx/nginx.conf"})
	eq(t, "puts", calls(f, "pvcontrol devmode files put"), []string{"put a etc/nginx/nginx.conf - --uid 33 --gid 33 --mode 0640"})
	if len(res[0].Deleted) != 0 || len(calls(f, "pvcontrol devmode files rm")) != 0 {
		t.Error("a partial checkout deleted something")
	}

	// a second single-file pull adds to the index and leaves the first file as it is
	r = pull(t, e, false, "usr/bin/run.sh")
	if len(r.Written) != 1 || len(r.Removed) != 0 || len(r.Conflicts) != 0 {
		t.Fatalf("second pull %+v", r)
	}
	ix = mustIndex(t, e, "a")
	if len(ix.Files) != 2 {
		t.Fatalf("index %+v", ix.Files)
	}
	if readF(t, e, "a", "etc/nginx/nginx.conf") != "worker 8;\n" {
		t.Error("the first file was touched")
	}
	// the files of the container outside the checkout are not seen as deleted
	f.cmds = nil
	if res, err := e.Push(context.Background(), []string{"a"}); err != nil || len(res[0].Pushed)+len(res[0].Deleted) != 0 || len(calls(f, "pvcontrol devmode files ")) != 0 {
		t.Errorf("push after the second pull: %+v %v %v", res, err, f.cmds)
	}
	st, _ := e.Status(context.Background(), []string{"a"})
	if d := st.Containers[0].Diff; !d.Empty() {
		t.Errorf("status %+v", d)
	}
	// the device changed the second file; the first one has a local edit that was pushed
	f.tars["a usr/bin/run.sh"] = one("usr/bin/run.sh", "#!/bin/bash\n")
	r = pull(t, e, false, "usr/bin/run.sh")
	if len(r.Written) != 1 || readF(t, e, "a", "usr/bin/run.sh") != "#!/bin/bash\n" {
		t.Errorf("refresh %+v", r)
	}
}

func TestSyncFromIndexDeletesGonePaths(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": buildTar(t,
		tent{name: "etc/", typ: tar.TypeDir, mode: 0o755},
		tent{name: "etc/x", mode: 0o644, uid: 7, gid: 7, body: "x"},
		tent{name: "etc/y", mode: 0o644, body: "y"},
	)}}
	e, _ := newEnv(t, f)
	pull(t, e, false)
	os.Remove(filepath.Join(e.Dir, "a", "etc/y"))
	put(t, e, "a", "etc/x", "x2")
	if _, err := e.Sync(context.Background(), []string{"a"}, true); err != nil {
		t.Fatal(err)
	}
	eq(t, "sync puts", calls(f, "pvcontrol devmode files put"), []string{"put a etc/x - --uid 7 --gid 7 --mode 0644"})
	eq(t, "sync removes", calls(f, "pvcontrol devmode files rm"), []string{"rm a etc/y --recursive"})
	if _, ok := mustIndex(t, e, "a").get("etc/y"); ok {
		t.Error("index still lists a path sync deleted")
	}
}

func TestWatchCycleUsesTheIndex(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": sampleTar(t)}}
	e, _ := newEnv(t, f)
	pull(t, e, false)
	put(t, e, "a", "etc/nginx/nginx.conf", "changed\n")
	f.cmds = nil
	e.cycle(context.Background(), []string{"a"})
	eq(t, "cycle", calls(f, "pvcontrol devmode files "), []string{"put a etc/nginx/nginx.conf - --uid 33 --gid 33 --mode 0644"})
	if idx(f.cmds, "pvcontrol devmode apply a") < 0 {
		t.Errorf("no apply: %v", f.cmds)
	}
}
