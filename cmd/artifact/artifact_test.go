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
	app.Commands = []cli.Command{CommandArtifact()}
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
