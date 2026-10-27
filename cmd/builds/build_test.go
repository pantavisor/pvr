package build

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-resty/resty"
	"github.com/urfave/cli"
)

const testID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func testApp(t *testing.T, base string) (*cli.App, *bytes.Buffer) {
	t.Helper()
	previous := resty.DefaultClient
	resty.DefaultClient = resty.New()
	t.Cleanup(func() { resty.DefaultClient = previous })
	app := cli.NewApp()
	app.Metadata = map[string]interface{}{"PVR_BASEURL": base, "PVR_AUTH": "test-token", "PVR_CONFIG_DIR": t.TempDir()}
	out := new(bytes.Buffer)
	app.Writer = out
	app.ErrWriter = new(bytes.Buffer)
	app.Commands = []cli.Command{CommandBuild()}
	return app, out
}

func TestCommandsAndIdempotency(t *testing.T) {
	var requests []string
	var key string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing pvr authentication")
		}
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" && r.URL.Path == "/builds/executions" {
			var in input
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.Project != "demo" || in.Mode != "simulated" || in.LimitSeconds != 60 {
				t.Errorf("wrong input: %+v", in)
			}
			key = r.Header.Get("Idempotency-Key")
		}
		fmt.Fprintf(w, `{"id":%q,"status":"queued"}`, testID)
	}))
	defer server.Close()
	app, out := testApp(t, server.URL)
	// The real CLI leaves ErrWriter nil; submission must still print its key.
	app.ErrWriter = nil
	for _, args := range [][]string{
		{"pvr", "build", "start", "--project", "demo", "--idempotency-key", "replay-this"},
		{"pvr", "build", "status", testID}, {"pvr", "build", "logs", testID}, {"pvr", "build", "cancel", testID}, {"pvr", "build", "list"},
	} {
		if err := app.Run(args); err != nil {
			t.Fatal(err)
		}
	}
	if key != "replay-this" {
		t.Fatal("submission did not preserve idempotency key")
	}
	if len(requests) != 5 || requests[3] != "POST /builds/executions/"+testID+"/cancel" {
		t.Fatal(requests)
	}
	if !strings.Contains(out.String(), testID) {
		t.Fatal("missing execution JSON")
	}
}

func TestDownloadVerifiesAndDoesNotOverwrite(t *testing.T) {
	artifact := []byte("archive bytes")
	hash := sha256.Sum256(artifact)
	checksum := hex.EncodeToString(hash[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing download authentication")
		}
		if strings.HasSuffix(r.URL.Path, "/artifacts") {
			w.Write(artifact)
			return
		}
		fmt.Fprintf(w, `{"id":%q,"status":"succeeded","artifact_sha256":%q}`, testID, checksum)
	}))
	defer server.Close()
	app, _ := testApp(t, server.URL)
	destination := filepath.Join(t.TempDir(), "export.tgz")
	args := []string{"pvr", "build", "download", "--output", destination, testID}
	if err := app.Run(args); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(got, artifact) {
		t.Fatal("wrong downloaded archive", err)
	}
	if err = app.Run(args); err == nil {
		t.Fatal("overwrote existing artifact")
	}
	got, _ = os.ReadFile(destination)
	if !bytes.Equal(got, artifact) {
		t.Fatal("changed existing artifact")
	}
}

func TestRejectedDownloadsLeaveNoOutput(t *testing.T) {
	for _, mode := range []string{"checksum", "redirect", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			redirected := false
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/artifacts") {
					switch mode {
					case "redirect":
						http.Redirect(w, r, target.URL, http.StatusFound)
					case "oversize":
						w.Header().Set("Content-Length", fmt.Sprint(maxArtifact+1))
					default:
						w.Write([]byte("wrong archive"))
					}
					return
				}
				fmt.Fprintf(w, `{"id":%q,"status":"succeeded","artifact_sha256":%q}`, testID, testID)
			}))
			defer server.Close()
			app, _ := testApp(t, server.URL)
			destination := filepath.Join(t.TempDir(), "export.tgz")
			if err := app.Run([]string{"pvr", "build", "download", "--output", destination, testID}); err == nil {
				t.Fatal("accepted invalid archive")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("published rejected archive")
			}
			if redirected {
				t.Fatal("followed credential redirect")
			}
		})
	}
}

func TestInvalidInputsNeverSubmit(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	app, _ := testApp(t, server.URL)
	for _, args := range [][]string{
		{"pvr", "build", "start", "--project", "demo", "--limit-seconds", "0"},
		{"pvr", "build", "start", "--project", "demo", "--mode", "docker", "--image", "alpine:latest"},
		{"pvr", "build", "status", "../another-owner"},
	} {
		if err := app.Run(args); err == nil {
			t.Fatal("accepted invalid input")
		}
	}
	if calls != 0 {
		t.Fatal("submitted invalid configuration")
	}
}

func TestDownloadFetchesTheArtifactObject(t *testing.T) {
	content := []byte("a pvr export")
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	var blobAuth []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/builds/executions/"+testID:
			fmt.Fprintf(w, `{"id":%q,"status":"succeeded","artifact_sha256":%q}`, testID, sha)
		case r.URL.Path == "/builds/executions/"+testID+"/artifacts":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"artifacts":[{"name":"app.pvrexport.tgz","sha256":%q,"size":%d,"object":"/objects/%s"}]}`, sha, len(content), sha)
		case r.URL.Path == "/objects/"+sha:
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Error("object lookup without pvr authentication")
			}
			fmt.Fprintf(w, `{"id":%q,"signed-geturl":"http://%s/local-s3/signed"}`, sha, r.Host)
		case r.URL.Path == "/local-s3/signed":
			// Production storage answers the signed URL with a redirect.
			http.Redirect(w, r, "/local-s3/blob", http.StatusFound)
		case r.URL.Path == "/local-s3/blob":
			blobAuth = append(blobAuth, r.Header.Get("Authorization"))
			_, _ = w.Write(content)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	app, out := testApp(t, server.URL)
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	if err := app.Run([]string{"pvr", "build", "download", testID}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "app.pvrexport.tgz"))
	if err != nil || !bytes.Equal(got, content) || !strings.Contains(out.String(), "app.pvrexport.tgz") {
		t.Fatalf("download wrote %q, %v", got, err)
	}
	if len(blobAuth) != 1 || blobAuth[0] != "" {
		t.Fatalf("the signed URL received credentials: %q", blobAuth)
	}
	if err := app.Run([]string{"pvr", "build", "download", "--name", "other", testID}); err == nil || !strings.Contains(err.Error(), "no artifact named") {
		t.Fatalf("unknown artifact: %v", err)
	}
}

func TestSignedRedirectsStayOnHTTPS(t *testing.T) {
	req := func(raw string) *http.Request {
		r, _ := http.NewRequest(http.MethodGet, raw, nil)
		return r
	}
	signed := []*http.Request{req("https://s3.example/signed")}
	if err := signedRedirects(req("https://cdn.example/blob"), signed); err != nil {
		t.Fatalf("refused an https redirect: %v", err)
	}
	if err := signedRedirects(req("http://cdn.example/blob"), signed); err == nil {
		t.Fatal("followed a redirect from https to http")
	}
	many := make([]*http.Request, 10)
	for i := range many {
		many[i] = req("https://s3.example/signed")
	}
	if err := signedRedirects(req("https://cdn.example/blob"), many); err == nil {
		t.Fatal("followed more than 10 redirects")
	}
}

func TestObjectDownloadRejectsIncorrectDeclaredSize(t *testing.T) {
	data := []byte("complete bytes with a correct checksum")
	sum := sha256.Sum256(data)
	destination := filepath.Join(t.TempDir(), "artifact.tgz")
	err := saveArtifact(bytes.NewReader(data), destination, hex.EncodeToString(sum[:]), int64(len(data)+1), true)
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("accepted incorrect object size: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("published an artifact with the wrong size")
	}
}

func TestGitBuildTargetPlatforms(t *testing.T) {
	received := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in input
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
		}
		received = in.Platform
		fmt.Fprintf(w, `{"id":%q,"status":"queued"}`, testID)
	}))
	defer server.Close()
	app, _ := testApp(t, server.URL)
	for _, platform := range []string{"linux/amd64", "linux/arm64", "linux/arm/v6"} {
		if err := app.Run([]string{"pvr", "build", "start", "--project", "mqtt", "--git-url", "https://github.com/pantavisor/pv-mqtt-sdk.git", "--platform", platform}); err != nil {
			t.Fatal(err)
		}
		if received != platform {
			t.Fatalf("submitted %q, wanted %q", received, platform)
		}
	}
}

func TestPipelineSubmissionAndArtifactSelection(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var in input
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Platform != "amd64,arm64,arm32v6" {
			t.Errorf("targets: %q", in.Platform)
		}
		fmt.Fprintf(w, `{"id":%q,"status":"queued","children":[{"platform":"linux/amd64"},{"platform":"linux/arm64"},{"platform":"linux/arm/v6"}]}`, testID)
	}))
	defer server.Close()
	app, _ := testApp(t, server.URL)
	if err := app.Run([]string{"pvr", "build", "start", "--project", "mqtt", "--git-url", "https://github.com/pantavisor/pv-mqtt-sdk.git", "--platform", "amd64,arm64,arm32v6"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("client fanned out submissions instead of creating one pipeline")
	}
	list := []artifact{{Name: "app.pvrexport.tgz", SHA256: testID, Size: 1, Platform: "linux/amd64"}, {Name: "app.pvrexport.tgz", SHA256: testID, Size: 1, Platform: "linux/arm64"}}
	if _, err := chooseArtifact(list, "", ""); err == nil {
		t.Fatal("silently chose one architecture")
	}
	selected, err := chooseArtifact(list, "", "arm64")
	if err != nil || selected.Platform != "linux/arm64" {
		t.Fatalf("selection: %+v %v", selected, err)
	}
}

func TestImageBaseAndImageBuilds(t *testing.T) {
	var received input
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = input{}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		fmt.Fprintf(w, `{"id":%q,"status":"queued"}`, testID)
	}))
	defer server.Close()
	app, _ := testApp(t, server.URL)
	if err := app.Run([]string{"pvr", "build", "start", "--project", "cm5", "--target", "image-base", "--git-url", "https://github.com/pantavisor/meta-pantavisor",
		"--image-base", "kas/machines/rpi.yaml, kas/build-configs/build-image-base.yaml"}); err != nil {
		t.Fatal(err)
	}
	if received.Mode != "git" || received.LimitSeconds != 4*3600 || received.ImageBase == nil || strings.Join(received.ImageBase.Kas, ":") != "kas/machines/rpi.yaml:kas/build-configs/build-image-base.yaml" {
		t.Errorf("image base: %+v", received)
	}
	app, _ = testApp(t, server.URL)
	if err := app.Run([]string{"pvr", "build", "start", "--project", "cm5", "--target", "image", "--base", testID, "--identity", "acme",
		"--app-build", testID, "--device", "golden@3", "--policy", "green", "--policy", "red"}); err != nil {
		t.Fatal(err)
	}
	img := received.DeviceImage
	if received.Mode != "image" || received.LimitSeconds != 1800 || received.Platform != "" || img == nil || img.Base != testID || img.Identity != "acme" ||
		len(img.Apps) != 1 || img.Device == nil || img.Device.Device != "golden" || img.Device.Revision != 3 || strings.Join(img.Policies, ",") != "green,red" {
		t.Errorf("image: %+v %+v", received, img)
	}
	app, _ = testApp(t, server.URL)
	if err := app.Run([]string{"pvr", "build", "start", "--project", "cm5", "--base", testID, "--identity", "acme", "--make-factory", "--policy", "green"}); err != nil {
		t.Fatal(err)
	}
	if received.DeviceImage == nil || !received.DeviceImage.MakeFactory || received.DeviceImage.Base != testID {
		t.Errorf("factory build: %+v", received.DeviceImage)
	}
	app, _ = testApp(t, server.URL)
	if err := app.Run([]string{"pvr", "build", "start", "--project", "cm5", "--factory", testID, "--identity", "acme", "--app-build", testID}); err != nil {
		t.Fatal(err)
	}
	if received.Mode != "image" || received.DeviceImage == nil || received.DeviceImage.Factory != testID || received.DeviceImage.Base != "" {
		t.Errorf("image of a factory: %+v", received.DeviceImage)
	}
	for _, args := range [][]string{
		{"--base", testID, "--factory", testID, "--identity", "acme"},
		{"--base", testID, "--identity", "acme", "--make-factory", "--app-build", testID},
		{"--mode", "git", "--git-url", "https://github.com/a/b", "--identity", "acme"},
		{"--base", testID, "--identity", "acme", "--device", "golden"},
		{"--base", testID, "--identity", "Acme"},
		{"--base", testID, "--identity", "acme", "--signing-secret", "acme-oem"},
		{"--mode", "docker", "--image-base", "a.yaml"},
	} {
		app, _ = testApp(t, server.URL)
		if err := app.Run(append([]string{"pvr", "build", "start", "--project", "cm5"}, args...)); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
