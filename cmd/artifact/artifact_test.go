package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-resty/resty"
	"github.com/urfave/cli"
)

type fakeHub struct {
	t         *testing.T
	server    *httptest.Server
	artifacts bool // serve /artifacts
	hasObject bool
	requests  []string
	uploaded  []byte
	posted    []Entry
	stored    []Entry
}

func newFakeHub(t *testing.T, artifacts, hasObject bool) *fakeHub {
	f := &fakeHub{t: t, artifacts: artifacts, hasObject: hasObject}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeHub) serve(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.RequestURI())
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path != "/signed-put" && r.Header.Get("Authorization") != "Bearer test-token" {
		f.t.Errorf("no auth on %s", r.URL.Path)
	}
	switch {
	case r.URL.Path == "/objects/" && r.Method == "POST":
		if f.hasObject {
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{}`)
			return
		}
		fmt.Fprintf(w, `{"signed-puturl":"%s/signed-put"}`, f.server.URL)
	case r.URL.Path == "/signed-put" && r.Method == "PUT":
		f.uploaded, _ = io.ReadAll(r.Body)
	case !strings.HasPrefix(r.URL.Path, "/artifacts") || !f.artifacts:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "404 page not found")
	case r.URL.Path == "/artifacts" && r.Method == "POST":
		var e Entry
		_ = json.NewDecoder(r.Body).Decode(&e)
		for _, o := range f.stored {
			if o.Kind == e.Kind && o.Name == e.Name && o.Version == e.Version {
				w.WriteHeader(http.StatusConflict)
				return
			}
		}
		f.posted = append(f.posted, e)
		e.ID = "0123456789abcdef01234567"
		f.stored = append(f.stored, e)
		_ = json.NewEncoder(w).Encode(e)
	case r.URL.Path == "/artifacts" && r.Method == "GET":
		out := []Entry{}
		for _, e := range f.stored {
			if (r.URL.Query().Get("kind") == "" || r.URL.Query().Get("kind") == e.Kind) &&
				(r.URL.Query().Get("name") == "" || r.URL.Query().Get("name") == e.Name) {
				out = append(out, e)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == "DELETE":
		w.WriteHeader(http.StatusNoContent)
	}
}

func run(t *testing.T, f *fakeHub, args ...string) (string, string, error) {
	t.Helper()
	previous := resty.DefaultClient
	resty.DefaultClient = resty.New()
	t.Cleanup(func() { resty.DefaultClient = previous })
	app := cli.NewApp()
	app.Metadata = map[string]interface{}{"PVR_BASEURL": f.server.URL, "PVR_AUTH": "test-token", "PVR_CONFIG_DIR": t.TempDir()}
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	app.Writer, app.ErrWriter = out, errOut
	app.Commands = []cli.Command{CommandArtifact(), CommandSDK()}
	err := app.Run(append([]string{"pvr"}, args...))
	return out.String(), errOut.String(), err
}

func writeFile(t *testing.T, content string) (string, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sdk.sh")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	return p, hex.EncodeToString(sum[:])
}

func TestPublishNewObject(t *testing.T) {
	f := newFakeHub(t, true, false)
	file, sha := writeFile(t, "installer bytes")
	out, _, err := run(t, f, "artifact", "publish", file, "--kind", "sdk", "--name", "acme", "--version", "1.2",
		"--meta", "machine=qemux86-64", "--description", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if string(f.uploaded) != "installer bytes" {
		t.Fatalf("uploaded %q", f.uploaded)
	}
	if len(f.posted) != 1 || f.posted[0].SHA256 != sha || f.posted[0].Size != 15 || f.posted[0].Meta["machine"] != "qemux86-64" || f.posted[0].Kind != "sdk" {
		t.Fatalf("posted %+v", f.posted)
	}
	if !strings.Contains(out, "0123456789abcdef01234567") {
		t.Fatalf("output %q", out)
	}
	// Uploading comes before registering the entry.
	if f.requests[len(f.requests)-1] != "POST /artifacts" {
		t.Fatal(f.requests)
	}
}

func TestPublishObjectAlreadyPresent(t *testing.T) {
	f := newFakeHub(t, true, true)
	file, _ := writeFile(t, "installer bytes")
	_, errOut, err := run(t, f, "artifact", "publish", file, "--kind", "sdk", "--name", "acme", "--version", "1.2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if f.uploaded != nil || len(f.posted) != 1 || !strings.Contains(errOut, "already has") {
		t.Fatalf("uploaded=%q posted=%v err=%q", f.uploaded, f.posted, errOut)
	}
	if _, _, err := run(t, f, "artifact", "publish", file, "--kind", "sdk", "--name", "acme", "--version", "1.2"); err == nil || !strings.Contains(err.Error(), "already published") {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestLsAndRm(t *testing.T) {
	f := newFakeHub(t, true, true)
	file, _ := writeFile(t, "x")
	for _, v := range []string{"1", "2"} {
		if _, _, err := run(t, f, "artifact", "publish", file, "--kind", "doc", "--name", "guide", "--version", v); err != nil {
			t.Fatal(err)
		}
	}
	out, _, err := run(t, f, "artifact", "ls", "--kind", "doc")
	if err != nil || !strings.Contains(out, "KIND") || !strings.Contains(out, "guide") {
		t.Fatalf("%q %v", out, err)
	}
	out, _, _ = run(t, f, "artifact", "ls", "--json", "--name", "none")
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty json %q", out)
	}
	if _, _, err := run(t, f, "artifact", "rm", "doc/guide@2"); err != nil {
		t.Fatal(err)
	}
	if f.requests[len(f.requests)-1] != "DELETE /artifacts/0123456789abcdef01234567" {
		t.Fatal(f.requests)
	}
	if _, _, err := run(t, f, "artifact", "rm", "0123456789abcdef01234567"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, f, "artifact", "rm", "doc/guide@9"); err == nil || !strings.Contains(err.Error(), "no published artifact") {
		t.Fatalf("unknown: %v", err)
	}
}

func TestHubWithoutArtifacts(t *testing.T) {
	f := newFakeHub(t, false, false)
	file, _ := writeFile(t, "x")
	for _, args := range [][]string{
		{"artifact", "ls"},
		{"artifact", "rm", "doc/guide@1"},
		{"artifact", "publish", file, "--kind", "doc", "--name", "g", "--version", "1"},
	} {
		if _, _, err := run(t, f, args...); err == nil || err.Error() != "this Hub does not support published artifacts yet" {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if f.uploaded != nil {
		t.Fatal("uploaded to a Hub without artifacts")
	}
}

func TestMissingFlags(t *testing.T) {
	f := newFakeHub(t, true, false)
	file, _ := writeFile(t, "x")
	if _, _, err := run(t, f, "artifact", "publish", file, "--kind", "doc"); err == nil {
		t.Fatal("name and version are required")
	}
	if _, _, err := run(t, f, "artifact", "publish", file, "--kind", "doc", "--name", "a", "--version", "1", "--meta", "novalue"); err == nil {
		t.Fatal("bad meta")
	}
}

func TestSDKForm(t *testing.T) {
	f := newFakeHub(t, true, false)
	file, _ := writeFile(t, "sdk bytes")
	if _, _, err := run(t, f, "sdk", "publish", file, "--name", "acme", "--version", "1.0", "--machine", "m",
		"--target", "aarch64", "--platform", "linux", "--image", "img", "--build-id", "b1"); err != nil {
		t.Fatal(err)
	}
	e := f.posted[0]
	if e.Kind != "sdk" || e.Meta["machine"] != "m" || e.Meta["target"] != "aarch64" || e.Meta["platform"] != "linux" ||
		e.Meta["image"] != "img" || e.Meta["build_id"] != "b1" {
		t.Fatalf("posted %+v", e)
	}
	out, _, err := run(t, f, "sdk", "ls")
	if err != nil || !strings.Contains(out, "MACHINE") || !strings.Contains(out, "aarch64") {
		t.Fatalf("%q %v", out, err)
	}
	if !strings.Contains(f.requests[len(f.requests)-1], "kind=sdk") {
		t.Fatal(f.requests)
	}
	if _, _, err := run(t, f, "sdk", "rm", "acme@1.0"); err != nil {
		t.Fatal(err)
	}
	if f.requests[len(f.requests)-1] != "DELETE /artifacts/0123456789abcdef01234567" {
		t.Fatal(f.requests)
	}
	if _, _, err := run(t, f, "sdk", "rm", "acme"); err == nil {
		t.Fatal("rm needs a version")
	}
	if _, _, err := run(t, f, "sdk", "publish", file, "--name", "acme"); err == nil {
		t.Fatal("version required")
	}
	g := newFakeHub(t, false, false)
	if _, _, err := run(t, g, "sdk", "ls"); err == nil || err.Error() != "this Hub does not support published artifacts yet" {
		t.Fatal(err)
	}
}

const testDescriptor = `{"name":"acme-image-m","version":"1.0-20260101","machine":"m","target":"x86_64-poky-linux",
"image":"acme-image","build_id":"20260101","file":"acme.sh","tune":"core2","libc":"glibc","packages":"x"}`

func writeDescriptor(t *testing.T, descriptor string, installer bool) string {
	t.Helper()
	dir := t.TempDir()
	if installer {
		if err := os.WriteFile(filepath.Join(dir, "acme.sh"), []byte("sdk bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, "acme-image-m.sdk.json")
	if err := os.WriteFile(p, []byte(descriptor), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSDKPublishDescriptor(t *testing.T) {
	f := newFakeHub(t, true, false)
	p := writeDescriptor(t, testDescriptor, true)
	if _, _, err := run(t, f, "sdk", "publish", p); err != nil {
		t.Fatal(err)
	}
	e := f.posted[0]
	if e.Kind != "sdk" || e.Name != "acme-image-m" || e.Version != "1.0-20260101" || e.Meta["machine"] != "m" ||
		e.Meta["target"] != "x86_64-poky-linux" || e.Meta["image"] != "acme-image" || e.Meta["build_id"] != "20260101" {
		t.Fatalf("posted %+v", e)
	}
	if e.Size != 9 {
		t.Fatalf("the installer next to the descriptor was not uploaded: %+v", e)
	}
}

func TestSDKPublishDescriptorFlagWins(t *testing.T) {
	f := newFakeHub(t, true, false)
	p := writeDescriptor(t, testDescriptor, true)
	if _, _, err := run(t, f, "sdk", "publish", p, "--version", "2.0", "--machine", "other"); err != nil {
		t.Fatal(err)
	}
	e := f.posted[0]
	if e.Version != "2.0" || e.Meta["machine"] != "other" || e.Name != "acme-image-m" || e.Meta["image"] != "acme-image" {
		t.Fatalf("posted %+v", e)
	}
}

func TestSDKPublishDescriptorErrors(t *testing.T) {
	f := newFakeHub(t, true, false)
	missing := filepath.Join(t.TempDir(), "none.sdk.json")
	if _, _, err := run(t, f, "sdk", "publish", missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("unreadable descriptor: %v", err)
	}
	if _, _, err := run(t, f, "sdk", "publish", writeDescriptor(t, "{not json", true)); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("bad JSON: %v", err)
	}
	if _, _, err := run(t, f, "sdk", "publish", writeDescriptor(t, `{"name":"a","version":"1"}`, true)); err == nil || !strings.Contains(err.Error(), `no "file"`) {
		t.Fatalf("no file: %v", err)
	}
	p := writeDescriptor(t, testDescriptor, false)
	_, _, err := run(t, f, "sdk", "publish", p)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(filepath.Dir(p), "acme.sh")) {
		t.Fatalf("installer not found should name the path: %v", err)
	}
	if len(f.posted) != 0 {
		t.Fatal("nothing should be published")
	}
}

// writeBundle writes an image descriptor that lists an SDK and a kernel source
// in a folder next to it, as the build does. The SDK has version sdkVersion.
func writeBundle(t *testing.T, sdkVersion, kernelSHA string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sdk"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	write("sdk/acme.sh", "sdk bytes")
	write("sdk/linux.tar.xz", "kernel bytes")
	if kernelSHA == "" {
		kernelSHA = sum("kernel bytes")
	}
	write("sdk/acme-image-m.sdk.json", fmt.Sprintf(`{"name":"acme-image-m","version":%q,"kind":"sdk","machine":"m","file":"acme.sh","sha256":%q}`, sdkVersion, sum("sdk bytes")))
	write("sdk/acme-image-m.kernel-source.json", fmt.Sprintf(`{"name":"acme-image-m","version":"1.0-1","kind":"kernel-source","file":"linux.tar.xz","sha256":%q,"meta":{"kernel_release":"6.6.1-x"}}`, kernelSHA))
	write("images/acme.sdk.json", `{"name":"acme-image-m","version":"1.0-1","publish":["../sdk/acme-image-m.sdk.json","../sdk/acme-image-m.kernel-source.json"]}`)
	return filepath.Join(dir, "images/acme.sdk.json")
}

func TestSDKPublishImageDescriptorPublishesBoth(t *testing.T) {
	f := newFakeHub(t, true, false)
	if _, _, err := run(t, f, "sdk", "publish", writeBundle(t, "1.0-1", "")); err != nil {
		t.Fatal(err)
	}
	if len(f.posted) != 2 || f.posted[0].Kind != "sdk" || f.posted[1].Kind != "kernel-source" {
		t.Fatalf("posted %+v", f.posted)
	}
	k := f.posted[1]
	if k.Name != "acme-image-m" || k.Version != "1.0-1" || k.Meta["kernel_release"] != "6.6.1-x" || k.Size != 12 {
		t.Fatalf("kernel source %+v", k)
	}
}

func TestSDKPublishImageDescriptorStopsBeforeUploading(t *testing.T) {
	f := newFakeHub(t, true, false)
	// an SDK of another run
	if _, _, err := run(t, f, "sdk", "publish", writeBundle(t, "1.0-0", "")); err == nil || !strings.Contains(err.Error(), "different builds") {
		t.Fatalf("version mismatch: %v", err)
	}
	// a kernel source that is not the file that was built
	if _, _, err := run(t, f, "sdk", "publish", writeBundle(t, "1.0-1", strings.Repeat("0", 64))); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("sha256 mismatch: %v", err)
	}
	// a file that is gone
	p := writeBundle(t, "1.0-1", "")
	if err := os.Remove(filepath.Join(filepath.Dir(p), "../sdk/linux.tar.xz")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, f, "sdk", "publish", p); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("missing file: %v", err)
	}
	if _, _, err := run(t, f, "sdk", "publish", writeBundle(t, "1.0-1", ""), "--version", "2"); err == nil || !strings.Contains(err.Error(), "--version cannot be used") {
		t.Fatalf("flag: %v", err)
	}
	if len(f.posted) != 0 || f.uploaded != nil {
		t.Fatalf("nothing should be uploaded: %v", f.posted)
	}
}

func TestSDKPublishImageDescriptorAlreadyPublished(t *testing.T) {
	f := newFakeHub(t, true, false)
	f.stored = []Entry{{Kind: "kernel-source", Name: "acme-image-m", Version: "1.0-1"}}
	if _, _, err := run(t, f, "sdk", "publish", writeBundle(t, "1.0-1", "")); err == nil || !strings.Contains(err.Error(), "already published") {
		t.Fatalf("%v", err)
	}
	if len(f.posted) != 0 || f.uploaded != nil {
		t.Fatalf("the SDK must not be uploaded alone: %v", f.posted)
	}
}
