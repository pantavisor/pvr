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
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// tent is one entry of a test tar.
type tent struct {
	name  string
	typ   byte // tar.Type*; zero means a regular file
	mode  int64
	uid   int
	gid   int
	body  string
	link  string
	mtime time.Time
}

func buildTar(t testing.TB, ents ...tent) []byte {
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, e := range ents {
		if e.typ == 0 {
			e.typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: e.mode, Uid: e.uid, Gid: e.gid, Linkname: e.link,
			Format: tar.FormatPAX, ModTime: e.mtime}
		if e.typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			io.WriteString(w, e.body)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// sampleTar is a small container: folders, files with modes, a symlink, a
// setuid file and a name longer than 100 bytes.
func sampleTar(t testing.TB) []byte {
	long := strings.Repeat("d", 60) + "/" + strings.Repeat("e", 60) + "/file.txt"
	return buildTar(t,
		tent{name: "etc/", typ: tar.TypeDir, mode: 0o755},
		tent{name: "etc/nginx/", typ: tar.TypeDir, mode: 0o750, uid: 33, gid: 33},
		tent{name: "etc/nginx/nginx.conf", mode: 0o644, uid: 33, gid: 33, body: "worker 1;\n"},
		tent{name: "usr/", typ: tar.TypeDir, mode: 0o755},
		tent{name: "usr/bin/", typ: tar.TypeDir, mode: 0o755},
		tent{name: "usr/bin/run.sh", mode: 0o755, body: "#!/bin/sh\n"},
		tent{name: "usr/bin/su", mode: 0o4755, body: "su"},
		tent{name: "usr/bin/sh", typ: tar.TypeSymlink, mode: 0o777, link: "run.sh"},
		tent{name: long, mode: 0o600, uid: 5, gid: 6, body: "long"},
	)
}

func readTree(t *testing.T, b Backend, c, p string) ([]string, error) {
	rc, err := b.Tree(t.Context(), c, p)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	tr := tar.NewReader(rc)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			_, err = io.Copy(io.Discard, rc)
			return names, err
		}
		if err != nil {
			return names, err
		}
		names = append(names, h.Name)
	}
}

func TestTreePvcontrol(t *testing.T) {
	f := &fakeDev{tars: map[string][]byte{"a": sampleTar(t), "a etc/nginx/nginx.conf": sampleTar(t)}}
	c := &Client{T: f}
	names, err := readTree(t, c, "a", "")
	if err != nil || len(names) != 9 {
		t.Fatalf("tree %v %v", names, err)
	}
	if _, err := readTree(t, c, "a", "etc/nginx/nginx.conf"); err != nil {
		t.Fatal(err)
	}
	if idx(f.cmds, "pvcontrol devmode files get a --tar") < 0 || idx(f.cmds, "pvcontrol devmode files get a etc/nginx/nginx.conf --tar") < 0 {
		t.Errorf("commands %v", f.cmds)
	}
	if _, err := readTree(t, c, "a", "../x"); err == nil {
		t.Error("a path with .. should fail")
	}
	if _, err := readTree(t, c, "missing", ""); err == nil || isNoTar(err) {
		t.Errorf("missing path: %v", err)
	}
	if _, err := readTree(t, &Client{T: &fakeDev{noTar: true}}, "a", ""); !isNoTar(err) {
		t.Errorf("old pvcontrol: %v", err)
	}
	// a pv-ctrl without tar answers the file or the manifest as text
	f.tars["json"] = []byte(`{"container":"a","files":[]}` + strings.Repeat(" ", 600))
	if _, err := readTree(t, c, "json", ""); !isNoTar(err) {
		t.Errorf("text answer: %v", err)
	}
	// an empty container is two zero blocks
	f.tars["empty"] = buildTar(t)
	if names, err := readTree(t, c, "empty", ""); err != nil || len(names) != 0 {
		t.Errorf("empty tar: %v %v", names, err)
	}
}

func TestTreeHTTP(t *testing.T) {
	f := &fakeCGI{ctype: "application/x-tar", reply: func(call string) (int, string) {
		switch call {
		case "GET /devmode/files/a?format=tar", "GET /devmode/files/a/etc/a%20b.conf?format=tar":
			return 200, string(sampleTar(t))
		case "GET /devmode/files/old?format=tar":
			return 400, `{"Error":"Bad request"}`
		case "GET /devmode/files/gone?format=tar":
			return 404, `{"Error":"Not found"}`
		}
		return 200, "{}"
	}}
	h := newHTTPBackend(t, f)
	if names, err := readTree(t, h, "a", "."); err != nil || len(names) != 9 {
		t.Fatalf("tree %v %v", names, err)
	}
	if _, err := readTree(t, h, "a", "etc/a b.conf"); err != nil {
		t.Fatal(err)
	}
	if _, err := readTree(t, h, "old", ""); !isNoTar(err) {
		t.Errorf("400: %v", err)
	}
	if _, err := readTree(t, h, "gone", ""); err == nil || isNoTar(err) {
		t.Errorf("404: %v", err)
	}
	// JSON instead of a tar: pv-ctrl without ?format=tar
	f.ctype = "application/json"
	if _, err := readTree(t, h, "a", ""); !isNoTar(err) {
		t.Errorf("json answer: %v", err)
	}
}
