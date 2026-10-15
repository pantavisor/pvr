package build

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/urfave/cli"
)

// stdinWith makes value what the command reads from stdin.
func stdinWith(t *testing.T, value string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString(value)
	_ = w.Close()
	previous := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = previous; _ = r.Close() })
}

func TestSecretValuesComeFromStdinOnly(t *testing.T) {
	var received []secretInput
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/secrets":
			fmt.Fprint(w, `[{"id":"0123456789abcdef01234567","name":"acme","type":"git","hosts":["github.com/acme"]}]`)
		case r.Method == "POST" || r.Method == "PUT":
			var in secretInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			received = append(received, in)
			fmt.Fprint(w, `{"id":"0123456789abcdef01234567","name":"acme"}`)
		case r.Method == "DELETE":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	app, out := testApp(t, server.URL)
	app.Commands = []cli.Command{CommandSecret(), CommandBuild()}

	stdinWith(t, "ghp_from_stdin\n")
	if err := app.Run([]string{"pvr", "secret", "add", "acme", "--host", "github.com/acme", "--host", "gitlab.com/acme"}); err != nil {
		t.Fatal(err)
	}
	if got := received[0]; got.Value != "ghp_from_stdin" || got.Type != "git" || len(got.Hosts) != 2 {
		t.Fatalf("add sent %+v", got)
	}
	if strings.Contains(out.String(), "ghp_from_stdin") {
		t.Fatal("the value was printed")
	}

	stdinWith(t, "ghp_rotated")
	if err := app.Run([]string{"pvr", "secret", "set", "acme", "--value"}); err != nil {
		t.Fatal(err)
	}
	if received[1].Value != "ghp_rotated" || requests[len(requests)-1] != "PUT /secrets/0123456789abcdef01234567" {
		t.Fatalf("set sent %+v by %v", received[1], requests)
	}
	if err := app.Run([]string{"pvr", "secret", "set", "acme"}); err == nil {
		t.Fatal("set with nothing to change")
	}
	if err := app.Run([]string{"pvr", "secret", "rm", "acme"}); err != nil {
		t.Fatal(err)
	}
	if requests[len(requests)-1] != "DELETE /secrets/0123456789abcdef01234567" {
		t.Fatalf("rm by %v", requests)
	}
	if err := app.Run([]string{"pvr", "secret", "rm", "unknown"}); err == nil || !strings.Contains(err.Error(), "no secret named") {
		t.Fatalf("rm of an unknown secret: %v", err)
	}
	stdinWith(t, "")
	if err := app.Run([]string{"pvr", "secret", "add", "empty", "--host", "github.com"}); err == nil {
		t.Fatal("added a secret without a value")
	}
}

func TestBuildStartNamesTheSecret(t *testing.T) {
	var in input
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&in)
		fmt.Fprintf(w, `{"id":%q,"status":"queued"}`, testID)
	}))
	defer server.Close()
	app, _ := testApp(t, server.URL)
	if err := app.Run([]string{"pvr", "build", "start", "--project", "demo", "--git-url", "https://github.com/acme/app", "--secret", "acme"}); err != nil {
		t.Fatal(err)
	}
	if in.Mode != "git" || in.Secret != "acme" {
		t.Fatalf("start sent %+v", in)
	}
	if err := app.Run([]string{"pvr", "build", "start", "--project", "demo", "--mode", "simulated", "--secret", "acme"}); err == nil {
		t.Fatal("a secret outside git mode")
	}
}

func TestBuildStartSendsTheOEMPart(t *testing.T) {
	var in map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&in)
		fmt.Fprintf(w, `{"id":%q,"status":"queued"}`, testID)
	}))
	defer server.Close()
	app, _ := testApp(t, server.URL)
	path := t.TempDir() + "/oem.json"
	_ = os.WriteFile(path, []byte(`{"configs": {"green": {"PH_FACTORY_AUTOTOK": {"secret": "green-autotok"}}}}`), 0o600)
	if err := app.Run([]string{"pvr", "build", "start", "--project", "demo", "--git-url", "https://github.com/acme/app", "--signing-secret", "acme-oem", "--oem", path}); err != nil {
		t.Fatal(err)
	}
	oem, _ := in["oem"].(map[string]interface{})
	if oem == nil || oem["configs"] == nil {
		t.Fatalf("start sent %v", in)
	}
	_ = os.WriteFile(path, []byte(`not json`), 0o600)
	if err := app.Run([]string{"pvr", "build", "start", "--project", "demo", "--git-url", "https://github.com/acme/app", "--oem", path}); err == nil {
		t.Fatal("an --oem file that is not JSON")
	}
}
