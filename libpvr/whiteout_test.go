//
// Copyright 2017-2024  Pantacor Ltd.
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

package libpvr

import (
	"archive/tar"
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateSquashfsOptionsRejectsCommandOptions asserts that options that
// mksquashfs would turn into command execution or host-file access are
// rejected (SR-01).
func TestValidateSquashfsOptionsRejectsCommandOptions(t *testing.T) {
	// -pf executes commands listed in a definition file, -af reads action
	// files, -ef reads an exclude list from a host file. -e only takes
	// relative paths inside the rootfs.
	dangerous := []string{
		"-pf /etc/shadow.dsq", // fails containment pairing anyway
		"-pf sentinel.dsq",
		"-ef excludes.txt",
		"-af actions.txt",
		"-e /etc",
		"-e ../outside",
		"-e lib/../../outside",
		"-e -comp",
		"-e",
		"-comp",
		"not-a-flag value",
		"-new-h option",
		"-root-mode 0755",
		"-mkfs-time 1",
		"-all-time 1",
		"-reproducible",
	}
	for _, options := range dangerous {
		if err := ValidateSquashfsOptions(options); err == nil {
			t.Errorf("ValidateSquashfsOptions(%q) accepted a dangerous option", options)
		}
	}

	// Odd flag count without a value (bare -comp) must also fail.
	if err := ValidateSquashfsOptions("-comp"); err == nil {
		t.Error("ValidateSquashfsOptions(\"-comp\") should fail: missing value")
	}

	// Tuning-only values from production usage must keep working.
	for _, options := range []string{
		"-comp xz",
		"-comp gzip",
		"-comp zstd -b 1M",
		"-Xdict-size 100%",
		"-Xcompression-level 6",
		"-comp xz -b 262144 -no-fragments -no-duplicates",
		"-comp lz4 -Xhc",
		"-comp xz -e lib/modules -e lib/firmware",
		"-all-root -e lib/modules -e lib/firmware",
	} {
		if err := ValidateSquashfsOptions(options); err != nil {
			t.Errorf("ValidateSquashfsOptions(%q) rejected a supported option: %v", options, err)
		}
	}
}

// TestSafeJoinRejectsEscape asserts the containment helper used by whiteout
// processing (SR-02).
func TestSafeJoinRejectsEscape(t *testing.T) {
	if _, err := SafeJoin("/tmp/root", "../other"); err == nil {
		t.Error("SafeJoin accepted a .. escape")
	}
	if _, err := SafeJoin("/tmp/root", "entry/../../other"); err == nil {
		t.Error("SafeJoin accepted a nested .. escape")
	}
	if _, err := SafeJoin("/tmp/root", "/etc/passwd"); err == nil {
		t.Error("SafeJoin accepted an absolute path")
	}
	got, err := SafeJoin("/tmp/root", "entry/.wh.file")
	if err != nil {
		t.Fatalf("SafeJoin rejected a contained path: %v", err)
	}
	if want := "/tmp/root/entry/.wh.file"; got != want {
		t.Errorf("SafeJoin = %q, want %q", got, want)
	}
}

// TestProcessWhiteoutsRejectsTraversal builds a temporary layer with a
// whiteout pointing outside the extraction root and verifies the rootfs
// outside the root survives (SR-02).
func TestProcessWhiteoutsRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	extract := filepath.Join(root, "extract")
	sentinel := filepath.Join(root, "sentinel")
	if err := os.MkdirAll(filepath.Join(extract, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	name := "../.wh.sentinel"
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: 0, Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Flush(); err != nil {
		t.Fatal(err)
	}

	layer := filepath.Join(root, "layer.tar")
	if err := os.WriteFile(layer, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ProcessWhiteouts(extract, layer, 0); err == nil {
		t.Fatal("ProcessWhiteouts accepted a traversal whiteout")
	}

	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("outside sentinel was removed: %v", err)
	}
}

// TestProcessWhiteoutsRemovesContainedWhiteout verifies normal OCI behavior
// still works.
func TestProcessWhiteoutsRemovesContainedWhiteout(t *testing.T) {
	root := t.TempDir()
	extract := filepath.Join(root, "extract")
	if err := os.MkdirAll(filepath.Join(extract, "home", "gone"), 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(extract, "home", "gone", "file.txt")
	if err := os.WriteFile(gone, []byte("delete me"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "home/gone/.wh.file.txt", Size: 0, Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Flush(); err != nil {
		t.Fatal(err)
	}

	layer := filepath.Join(root, "layer.tar")
	if err := os.WriteFile(layer, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ProcessWhiteouts(extract, layer, 0); err != nil {
		t.Fatalf("ProcessWhiteouts failed on a contained whiteout: %v", err)
	}

	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Fatalf("contained whiteout did not remove its target: stat err=%v", err)
	}
}

// TestDownloadFileRejectsNonHTTPS verifies the source URL policy (SR-03).
func TestDownloadFileRejectsNonHTTPS(t *testing.T) {
	if _, err := url.Parse("bad"); err != nil {
		t.Fatal(err)
	}
	ref, err := url.Parse("http://example.invalid/rootfs.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DownloadFile(ref); err == nil {
		t.Error("DownloadFile accepted a plain non-loopback http url")
	}
	httpsRef, err := url.Parse("https://example.invalid/rootfs.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DownloadFile(httpsRef); err == nil {
		t.Error("DownloadFile unexpectedly succeeded for an unresolvable host")
	}
}

// layerTar writes a tar layer of the given entries: name -> content,
// "->target" for a symlink, or "/" for a directory.
func layerTar(t *testing.T, path string, entries [][2]string) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := &tar.Header{Name: e[0], Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(e[1]))}
		if e[1] == "/" {
			h = &tar.Header{Name: e[0] + "/", Mode: 0o755, Typeflag: tar.TypeDir}
		}
		if strings.HasPrefix(e[1], "->") {
			h = &tar.Header{Name: e[0], Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: strings.TrimPrefix(e[1], "->")}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e[1])); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWhiteoutsDoNotFollowSymlinksOutOfTheRoot: a symlink an earlier layer
// left must not carry a whiteout, or an opaque one, outside the root (SR-02).
func TestWhiteoutsDoNotFollowSymlinksOutOfTheRoot(t *testing.T) {
	root := t.TempDir()
	extract := filepath.Join(root, "extract")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{extract, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(extract, "d")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"d/.wh.sentinel", "d/.wh..wh..opq"} {
		layer := filepath.Join(root, "layer.tar")
		layerTar(t, layer, [][2]string{{name, ""}})
		if err := ProcessWhiteouts(extract, layer, 1); err == nil {
			t.Errorf("%s through an outside symlink was processed", name)
		}
		if _, err := os.Stat(sentinel); err != nil {
			t.Fatalf("%s removed the outside sentinel: %v", name, err)
		}
	}
}

// TestLayersExtractInsideTheRootThroughEarlierSymlinks extracts layers with
// tar as the image import does: files under a symlink an earlier layer left
// (absolute, or ".." past the root) land inside the rootfs, and the image's
// own link targets are restored for packaging.
func TestLayersExtractInsideTheRootThroughEarlierSymlinks(t *testing.T) {
	if _, err := exec.LookPath(TAR_CMD); err != nil {
		t.Skip("tar is not installed")
	}
	root := t.TempDir()
	extract := filepath.Join(root, "extract")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{extract, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	first := filepath.Join(root, "first.tar")
	second := filepath.Join(root, "second.tar")
	// The image has the directory its links name, as images have /run.
	layerTar(t, first, [][2]string{{strings.TrimPrefix(outside, "/"), "/"}, {"abs", "->" + outside},
		{"up", "->../../../.." + outside}, {"inside", "->etc"}})
	layerTar(t, second, [][2]string{{"abs/a", "a"}, {"up/u", "u"}})

	contained := map[string]ContainedSymlink{}
	for i, layer := range []string{first, second} {
		if err := ProcessWhiteouts(extract, layer, i); err != nil {
			t.Fatal(err)
		}
		if err := Untar(extract, layer, []string{"--exclude", ".wh.*"}); err != nil {
			t.Fatal(err)
		}
		if err := ContainSymlinks(extract, contained); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatalf("layers wrote outside the root: %v", entries)
	}
	for _, name := range []string{"a", "u"} {
		if _, err := os.Stat(filepath.Join(extract, strings.TrimPrefix(outside, "/"), name)); err != nil {
			t.Errorf("%s is not inside the root where the container sees it: %v", name, err)
		}
	}
	if err := RestoreSymlinks(contained); err != nil {
		t.Fatal(err)
	}
	for link, want := range map[string]string{"abs": outside, "up": "../../../.." + outside, "inside": "etc"} {
		if got, _ := os.Readlink(filepath.Join(extract, link)); got != want {
			t.Errorf("%s -> %q after restore, want %q", link, got, want)
		}
	}
}

// TestDownloadFileStopsAtTheSizeCap: a source one byte over the cap fails
// instead of being truncated into a valid-looking rootfs (SR-03).
func TestDownloadFileStopsAtTheSizeCap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 11))
	}))
	defer server.Close()
	previous := MaxRootFsDownloadSize
	defer func() { MaxRootFsDownloadSize = previous }()
	ref, _ := url.Parse(server.URL + "/rootfs.tar")

	MaxRootFsDownloadSize = 10
	if path, err := DownloadFile(ref); err == nil {
		os.RemoveAll(filepath.Dir(path))
		t.Fatal("a download over the cap succeeded")
	}
	MaxRootFsDownloadSize = 11
	path, err := DownloadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(path))
	if data, _ := os.ReadFile(path); len(data) != 11 {
		t.Fatalf("downloaded %d bytes, want the file itself", len(data))
	}
}

// TestWhiteoutNamingNoEntryIsRefused: ".wh..." names "..": it must not
// remove the extraction root's parent (review BR-02).
func TestWhiteoutNamingNoEntryIsRefused(t *testing.T) {
	root := t.TempDir()
	extract := filepath.Join(root, "work", "extract")
	if err := os.MkdirAll(extract, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "work", "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".wh...", "d/.wh..", ".wh."} {
		layer := filepath.Join(root, "layer.tar")
		layerTar(t, layer, [][2]string{{name, ""}})
		_ = ProcessWhiteouts(extract, layer, 0)
		if _, err := os.Stat(sentinel); err != nil {
			t.Fatalf("whiteout %q removed outside the root: %v", name, err)
		}
	}
}

// TestComposedSymlinksStayInsideTheRoot: "d/a -> .." and "d/b -> a/../x"
// each look contained, but together the kernel resolves d/b above the
// root; a later layer must still write inside it (review BR-02).
func TestComposedSymlinksStayInsideTheRoot(t *testing.T) {
	if _, err := exec.LookPath(TAR_CMD); err != nil {
		t.Skip("tar is not installed")
	}
	root := t.TempDir()
	extract := filepath.Join(root, "extract")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{extract, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	first := filepath.Join(root, "first.tar")
	second := filepath.Join(root, "second.tar")
	layerTar(t, first, [][2]string{{"d", "/"}, {"outside", "/"}, {"d/a", "->.."}, {"d/b", "->a/../outside"}})
	layerTar(t, second, [][2]string{{"d/b/f", "escaped?"}})
	contained := map[string]ContainedSymlink{}
	for i, layer := range []string{first, second} {
		if err := ProcessWhiteouts(extract, layer, i); err != nil {
			t.Fatal(err)
		}
		if err := Untar(extract, layer, []string{"--exclude", ".wh.*"}); err != nil {
			t.Fatal(err)
		}
		if err := ContainSymlinks(extract, contained); err != nil {
			t.Fatal(err)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a later layer wrote outside the root through composed links: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(extract, "outside", "f")); err != nil {
		t.Fatalf("f is not where the container sees it: %v", err)
	}
	if err := RestoreSymlinks(contained); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(filepath.Join(extract, "d", "b")); got != "a/../outside" {
		t.Errorf("d/b -> %q after restore", got)
	}
}

func TestResolveInImage(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "usr", "lib"), 0o755)
	_ = os.Symlink("usr/lib", filepath.Join(root, "lib"))
	_ = os.Symlink("/../../etc", filepath.Join(root, "up"))
	_ = os.Symlink("loop", filepath.Join(root, "loop"))
	for in, want := range map[string]string{"/lib/x": "/usr/lib/x", "/up/passwd": "/etc/passwd", "/../../a": "/a", "/new/../b": "/b"} {
		if got, err := ResolveInImage(root, in); err != nil || got != want {
			t.Errorf("ResolveInImage(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ResolveInImage(root, "/loop/x"); err == nil {
		t.Error("a symlink loop resolved")
	}
}

// TestRootFsBudgetRefusesAnImageThatUnpacksTooBig: layers are measured
// before they are unpacked (review BR-05).
func TestRootFsBudgetRefusesAnImageThatUnpacksTooBig(t *testing.T) {
	root := t.TempDir()
	layer := filepath.Join(root, "layer.tar")
	layerTar(t, layer, [][2]string{{"a", "0123456789"}, {"b", "0123456789"}})
	previous, previousEntries := MaxRootFsBytes, MaxRootFsEntries
	defer func() { MaxRootFsBytes, MaxRootFsEntries = previous, previousEntries }()

	MaxRootFsBytes, MaxRootFsEntries = 25, 100
	var budget RootFsBudget
	if err := budget.Add(layer); err != nil {
		t.Fatalf("20 bytes under a 25-byte ceiling: %v", err)
	}
	if err := budget.Add(layer); err == nil {
		t.Fatal("a second 20-byte layer fit under 25 bytes")
	}
	MaxRootFsBytes, MaxRootFsEntries = 1000, 3
	budget = RootFsBudget{}
	_ = budget.Add(layer)
	if err := budget.Add(layer); err == nil {
		t.Fatal("4 entries fit under a ceiling of 3")
	}
}

// TestSquashfsArgsPutsExcludesLast checks that -e paths go after the flags
// pvr always sets, so mksquashfs does not read those flags as paths.
func TestSquashfsArgsPutsExcludesLast(t *testing.T) {
	got := strings.Join(squashfsArgs("-comp lz4 -Xhc -e lib/modules -e lib/firmware"), " ")
	want := "-comp lz4 -Xhc -reproducible -root-mode 0755 -mkfs-time 1 -all-time 1 -e lib/modules lib/firmware"
	if got != want {
		t.Errorf("squashfsArgs = %q, want %q", got, want)
	}
	got = strings.Join(squashfsArgs(""), " ")
	want = "-comp xz -reproducible -root-mode 0755 -mkfs-time 1 -all-time 1"
	if got != want {
		t.Errorf("squashfsArgs(\"\") = %q, want %q", got, want)
	}
}
