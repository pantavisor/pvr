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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type toolCall struct {
	Tool string
	Args map[string]interface{}
}

// fakeHub is a Hub with the OAuth endpoints and an /mcp endpoint.
type fakeHub struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	calls      []toolCall
	registered int
	codeGrants int
	refreshes  int
	validAcc   map[string]bool
	validRef   map[string]bool
	challenge  string
	issued     int
	loginToken string // what the consent accepts
	redirect   string
	state      string
	// tools answers a tool call; a nil failure means success
	tools map[string]func(args map[string]interface{}) (map[string]interface{}, *toolFailure)
	puts  map[string][]byte
}

func newFakeHub(t *testing.T) *fakeHub {
	f := &fakeHub{t: t, validAcc: map[string]bool{}, validRef: map[string]bool{}, loginToken: "login-secret",
		tools: map[string]func(map[string]interface{}) (map[string]interface{}, *toolFailure){}, puts: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"resource": f.srv.URL + "/mcp", "scopes_supported": []string{"devices.devmode", "devices.logs"}})
	})
	mux.HandleFunc("/auth/oauth/register", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.registered++
		n := f.registered
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"client_id": fmt.Sprintf("client-%d", n)})
	})
	mux.HandleFunc("/auth/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			// the consent, as the Hub's web app sends it: login token plus the approval cookies
			f.mu.Lock()
			want, redirect, state := f.loginToken, f.redirect, f.state
			f.mu.Unlock()
			if r.Header.Get("Authorization") != "Bearer "+want {
				http.Error(w, "no", 401)
				return
			}
			ac, err1 := r.Cookie("pkce_auth_code")
			ru, err2 := r.Cookie("pkce_redirect_uri")
			if err1 != nil || err2 != nil || ac.Value != "AC" || ru.Value != redirect {
				http.Error(w, "bad", 400)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"code": "THECODE", "redirect_uri": redirect + "?code=THECODE&state=" + state})
			return
		}
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || !strings.Contains(q.Get("scope"), "offline_access") {
			http.Error(w, "bad", 400)
			return
		}
		f.mu.Lock()
		f.challenge, f.redirect, f.state = q.Get("code_challenge"), q.Get("redirect_uri"), q.Get("state")
		f.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "pkce_auth_code", Value: "AC", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "pkce_redirect_uri", Value: q.Get("redirect_uri"), Path: "/"})
		http.Redirect(w, r, "/oauth2/authorize?auth_code=AC", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/auth/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "THECODE" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				http.Error(w, "bad", 400)
				return
			}
			f.codeGrants++
		case "refresh_token":
			if !f.validRef[r.Form.Get("refresh_token")] {
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			f.refreshes++
		}
		f.issued++
		acc, ref := fmt.Sprintf("acc-%d", f.issued), fmt.Sprintf("ref-%d", f.issued)
		f.validAcc[acc], f.validRef[ref] = true, true
		json.NewEncoder(w).Encode(map[string]interface{}{"access_token": acc, "refresh_token": ref, "expires_in": 3600})
	})
	mux.HandleFunc("/mcp", f.mcp)
	mux.HandleFunc("/put/", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.puts[r.URL.Path] = b
		f.mu.Unlock()
	})
	mux.HandleFunc("/get/big", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "big content") })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHub) mcp(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	ok := f.validAcc[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	f.mu.Unlock()
	if !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Header.Get("MCP-Protocol-Version") == "" || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		http.Error(w, "headers", 400)
		return
	}
	var req struct {
		Params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		} `json:"params"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	f.calls = append(f.calls, toolCall{req.Params.Name, req.Params.Arguments})
	h := f.tools[req.Params.Name]
	f.mu.Unlock()
	reply := map[string]interface{}{}
	if h == nil {
		reply = map[string]interface{}{"isError": true, "content": []map[string]string{{"type": "text", "text": "unknown tool " + req.Params.Name}}}
	} else if out, fail := h(req.Params.Arguments); fail != nil {
		b, _ := json.Marshal(fail)
		reply = map[string]interface{}{"isError": true, "content": []map[string]string{{"type": "text", "text": string(b)}}}
	} else {
		b, _ := json.Marshal(out)
		reply = map[string]interface{}{"structuredContent": out, "content": []map[string]string{{"type": "text", "text": string(b)}}}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": reply})
}

func (f *fakeHub) on(tool string, h func(map[string]interface{}) (map[string]interface{}, *toolFailure)) {
	f.mu.Lock()
	f.tools[tool] = h
	f.mu.Unlock()
}

func (f *fakeHub) reply(tool string, out map[string]interface{}) {
	f.on(tool, func(map[string]interface{}) (map[string]interface{}, *toolFailure) { return out, nil })
}

func (f *fakeHub) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n []string
	for _, c := range f.calls {
		n = append(n, c.Tool)
	}
	return n
}

func (f *fakeHub) last(tool string) map[string]interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Tool == tool {
			return f.calls[i].Args
		}
	}
	f.t.Fatalf("%s was not called", tool)
	return nil
}

func (f *fakeHub) backend(t *testing.T, dir string) *HubBackend {
	login := func(renew bool) (string, error) { return f.loginToken, nil }
	return NewHubBackend("nick1", f.srv.URL, newHubTokenStore(dir), login)
}

func hubFixture(t *testing.T) (*fakeHub, *HubBackend, string) {
	f := newFakeHub(t)
	f.reply("lease", map[string]interface{}{"holder": "h", "expires_at": "2099-01-01T00:00:00Z"})
	f.reply("release", map[string]interface{}{})
	dir := t.TempDir()
	return f, f.backend(t, dir), dir
}

func TestHubTokenFlowAndRefresh(t *testing.T) {
	f := newFakeHub(t)
	f.reply("container_status", map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "a", "restart_policy": "container"}}})
	dir := t.TempDir()
	ctx := context.Background()

	m, err := f.backend(t, dir).Containers(ctx)
	if err != nil || m["a"].RestartPolicy != "container" {
		t.Fatalf("containers %v %v", m, err)
	}
	if f.registered != 1 || f.codeGrants != 1 {
		t.Errorf("registered %d, grants %d", f.registered, f.codeGrants)
	}
	st, err := os.Stat(filepath.Join(dir, "devmode-hub.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("token file %v %v", st, err)
	}

	// the saved token is used again, with no new approval
	if _, err := f.backend(t, dir).Containers(ctx); err != nil {
		t.Fatal(err)
	}
	if f.registered != 1 || f.codeGrants != 1 || f.refreshes != 0 {
		t.Errorf("second run: registered %d, grants %d, refreshes %d", f.registered, f.codeGrants, f.refreshes)
	}

	// the Hub drops the access token: the refresh token gets a new one
	f.mu.Lock()
	f.validAcc = map[string]bool{}
	f.mu.Unlock()
	if _, err := f.backend(t, dir).Containers(ctx); err != nil {
		t.Fatal(err)
	}
	if f.refreshes != 1 || f.codeGrants != 1 {
		t.Errorf("after refresh: refreshes %d, grants %d", f.refreshes, f.codeGrants)
	}

	// the Hub drops everything: the approval runs again with the same client
	f.mu.Lock()
	f.validAcc, f.validRef = map[string]bool{}, map[string]bool{}
	f.mu.Unlock()
	if _, err := f.backend(t, dir).Containers(ctx); err != nil {
		t.Fatal(err)
	}
	if f.codeGrants != 2 || f.registered != 1 {
		t.Errorf("after full renewal: grants %d, registered %d", f.codeGrants, f.registered)
	}
}

func TestHubTokensNeverShown(t *testing.T) {
	f := newFakeHub(t)
	f.loginToken = "other" // the pvr login is not accepted
	dir := t.TempDir()
	login := func(renew bool) (string, error) { return "wrong-login-secret", nil }
	b := NewHubBackend("n", f.srv.URL, newHubTokenStore(dir), login)
	_, err := b.Containers(context.Background())
	if err == nil || strings.Contains(err.Error(), "wrong-login-secret") || !strings.Contains(err.Error(), "pvr login") {
		t.Errorf("error: %v", err)
	}
}

func TestHubErrors(t *testing.T) {
	f, h, _ := hubFixture(t)
	ctx := context.Background()
	cases := map[string]Kind{
		"DEV_MODE_NOT_ALLOWED": KindNotAllowed, "NOT_IN_DEV_MODE": KindNotActive, "REBOOT_NEEDED": KindRebootNeeded,
		"DEVICE_OFFLINE": KindUnreachable, "APPLY_FAILED": KindApplyFailed, "LEASE_HELD": KindGeneric,
	}
	for code, kind := range cases {
		f.on("reset_changes", func(map[string]interface{}) (map[string]interface{}, *toolFailure) {
			return nil, &toolFailure{Code: code, Message: "message of " + code}
		})
		err := h.Reset(ctx, "a")
		e, ok := err.(*Error)
		if !ok || e.Kind != kind || !strings.Contains(e.Msg, "message of "+code) {
			t.Errorf("%s: %v", code, err)
		}
	}
}

func TestHubLease(t *testing.T) {
	f, h, _ := hubFixture(t)
	ctx := context.Background()
	f.reply("reset_changes", map[string]interface{}{"reset": true})
	f.reply("ls", map[string]interface{}{"entries": []interface{}{}})
	h.Manifest(ctx, "a") // reading takes no lease
	h.Reset(ctx, "a")
	h.Reset(ctx, "b")
	h.Close()
	want := []string{"ls", "lease", "reset_changes", "reset_changes", "release"}
	if got := f.names(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls %v", got)
	}
}

func TestHubLeaseRequiredRetry(t *testing.T) {
	f, h, _ := hubFixture(t)
	n := 0
	f.on("reset_changes", func(map[string]interface{}) (map[string]interface{}, *toolFailure) {
		n++
		if n == 1 {
			return nil, &toolFailure{Code: "LEASE_REQUIRED", Message: "take the lease"}
		}
		return map[string]interface{}{}, nil
	})
	if err := h.Reset(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	if got := f.names(); !reflect.DeepEqual(got, []string{"lease", "reset_changes", "lease", "reset_changes"}) {
		t.Errorf("calls %v", got)
	}
}

func TestHubStatusEnterLeave(t *testing.T) {
	f, h, _ := hubFixture(t)
	ctx := context.Background()
	f.reply("get_devmode_context", map[string]interface{}{"connected": true, "devmode": true, "devmode_base": "5", "devmode_on_device": "devmode/r/5",
		"committed_revision": "5"})
	f.reply("container_status", map[string]interface{}{"containers": []interface{}{
		map[string]interface{}{"name": "b", "restart_policy": "system"}, map[string]interface{}{"name": "a", "restart_policy": "container"}}})
	st, err := h.Status(ctx)
	if err != nil || !st.Enabled || !st.Active || !st.Ready || st.Base != "5" || st.Revision != "devmode/r/5" ||
		len(st.Containers) != 2 || st.Containers[0].Name != "a" {
		t.Fatalf("status %+v %v", st, err)
	}
	f.reply("dev_begin", map[string]interface{}{"base": "5", "on_device": "devmode/r/5", "rebooted": true, "pending": true})
	r, err := h.Enter(ctx, "5", true)
	if err != nil || !r.Reboot || r.Base != "5" || r.Revision != "devmode/r/5" {
		t.Fatalf("enter %+v %v", r, err)
	}
	if a := f.last("dev_begin"); a["base"] != "5" || a["resume"] != true || a["device"] != "nick1" {
		t.Errorf("dev_begin args %v", a)
	}
	f.reply("dev_end", map[string]interface{}{"mode": "discard", "rebooted": true})
	if err := h.Leave(ctx, "7"); err == nil {
		t.Error("leaving to another revision should fail")
	}
	if err := h.Leave(ctx, "5"); err != nil {
		t.Fatal(err)
	}
	if a := f.last("dev_end"); a["mode"] != "discard" {
		t.Errorf("dev_end args %v", a)
	}
	// dev_end gave the lease back, so Close does not release it again
	h.Close()
	for _, n := range f.names() {
		if n == "release" {
			t.Error("released a lease that dev_end already dropped")
		}
	}

	f.reply("get_devmode_context", map[string]interface{}{"connected": false})
	if _, err := h.Status(ctx); err == nil || err.(*Error).Kind != KindUnreachable {
		t.Errorf("offline: %v", err)
	}
}

func TestHubPut(t *testing.T) {
	f, h, _ := hubFixture(t)
	ctx := context.Background()
	f.reply("apply_changes", map[string]interface{}{"applied": 2})

	if err := h.Put(ctx, "a", "etc/app/x.conf", strings.NewReader("hello"), PutOpts{Mode: 0o640}); err != nil {
		t.Fatal(err)
	}
	a := f.last("apply_changes")
	ops, _ := json.Marshal(a["ops"])
	want := `[{"op":"mkdir","parents":true,"path":"etc/app"},{"mode":"0640","op":"put","path":"etc/app/x.conf","base64":"aGVsbG8=","uid":0,"gid":0}]`
	var got, exp []map[string]interface{}
	json.Unmarshal(ops, &got)
	json.Unmarshal([]byte(want), &exp)
	if !reflect.DeepEqual(got, exp) || a["container"] != "a" || a["apply_id"] == "" {
		t.Errorf("ops %s args %v", ops, a)
	}

	h.Put(ctx, "a", "empty", nil, PutOpts{Mode: 0o644})
	if ops, _ := json.Marshal(f.last("apply_changes")["ops"]); !strings.Contains(string(ops), `"text":""`) {
		t.Errorf("empty file ops %s", ops)
	}
	h.Put(ctx, "a", "d", nil, PutOpts{Mode: 0o750, Type: "dir"})
	if ops, _ := json.Marshal(f.last("apply_changes")["ops"]); string(ops) != `[{"gid":0,"mode":"0750","op":"mkdir","parents":true,"path":"d","uid":0}]` {
		t.Errorf("dir ops %s", ops)
	}
	h.Put(ctx, "a", "bin/ln", nil, PutOpts{Type: "symlink", Target: "../x"})
	if ops, _ := json.Marshal(f.last("apply_changes")["ops"]); !strings.Contains(string(ops), `{"gid":0,"op":"symlink","path":"bin/ln","target":"../x","uid":0}`) {
		t.Errorf("symlink ops %s", ops)
	}
	// an owner goes to the Hub in all three ops
	h.Put(ctx, "a", "f", strings.NewReader("x"), PutOpts{Mode: 0o600, UID: 1000, GID: 100})
	if ops, _ := json.Marshal(f.last("apply_changes")["ops"]); !strings.Contains(string(ops), `"gid":100`) || !strings.Contains(string(ops), `"uid":1000`) {
		t.Errorf("owner of a file: %s", ops)
	}
	h.Put(ctx, "a", "d2", nil, PutOpts{Type: "dir", UID: 5, GID: 6})
	if ops, _ := json.Marshal(f.last("apply_changes")["ops"]); !strings.Contains(string(ops), `"gid":6`) || !strings.Contains(string(ops), `"uid":5`) {
		t.Errorf("owner of a dir: %s", ops)
	}
	h.Put(ctx, "a", "ln2", nil, PutOpts{Type: "symlink", Target: "x", UID: 7, GID: 8})
	if ops, _ := json.Marshal(f.last("apply_changes")["ops"]); !strings.Contains(string(ops), `"gid":8`) || !strings.Contains(string(ops), `"uid":7`) {
		t.Errorf("owner of a symlink: %s", ops)
	}

	// a large file goes through the object store
	big := bytes.Repeat([]byte("0123456789abcdef"), hubInlineMax/8)
	sum := sha256.Sum256(big)
	hexsum := hex.EncodeToString(sum[:])
	f.on("get_object_upload_link", func(args map[string]interface{}) (map[string]interface{}, *toolFailure) {
		if args["sha256"] != hexsum || args["size"] != float64(len(big)) {
			return nil, &toolFailure{Code: "INVALID", Message: "bad args"}
		}
		return map[string]interface{}{"sha256": hexsum, "put_url": f.srv.URL + "/put/" + hexsum}, nil
	})
	if err := h.Put(ctx, "a", "big.bin", bytes.NewReader(big), PutOpts{Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.puts["/put/"+hexsum], big) {
		t.Error("the file was not uploaded")
	}
	ops, _ = json.Marshal(f.last("apply_changes")["ops"])
	if !strings.Contains(string(ops), `"source":{"sha256":"`+hexsum+`","size":`+fmt.Sprint(len(big))+`}`) {
		t.Errorf("big ops %s", ops)
	}
}

func TestHubPendingApply(t *testing.T) {
	f, h, _ := hubFixture(t)
	n := 0
	var ids []string
	f.on("apply_changes", func(args map[string]interface{}) (map[string]interface{}, *toolFailure) {
		ids = append(ids, fmt.Sprint(args["apply_id"]))
		if n++; n < 3 {
			return map[string]interface{}{"pending": true}, nil
		}
		return map[string]interface{}{"applied": 1}, nil
	})
	if err := h.Remove(context.Background(), "a", "old", true); err != nil {
		t.Fatal(err)
	}
	if n != 3 || ids[0] != ids[1] || ids[1] != ids[2] {
		t.Errorf("calls %d ids %v", n, ids)
	}
	if a := f.calls[len(f.calls)-1].Args; a["ops"] != nil {
		t.Errorf("a wait carries no ops: %v", a)
	}
}

func TestHubRemoveResetApply(t *testing.T) {
	f, h, _ := hubFixture(t)
	ctx := context.Background()
	f.reply("apply_changes", map[string]interface{}{})
	f.reply("reset_changes", map[string]interface{}{})
	f.reply("container_ctl", map[string]interface{}{})
	h.Remove(ctx, "a", "usr/share/doc", true)
	if ops, _ := json.Marshal(f.last("apply_changes")["ops"]); string(ops) != `[{"op":"rm","path":"usr/share/doc","recursive":true}]` {
		t.Errorf("rm ops %s", ops)
	}
	h.Reset(ctx, "a")
	h.Apply(ctx, "b", true)
	if a := f.last("container_ctl"); a["action"] != "restart" || a["allow_reboot"] != true || a["container"] != "b" {
		t.Errorf("container_ctl args %v", a)
	}
}

// a Hub from before uid and gid refuses the ops that carry them
func TestHubPutOldHub(t *testing.T) {
	f, h, _ := hubFixture(t)
	ctx := context.Background()
	var seen []string
	f.on("apply_changes", func(args map[string]interface{}) (map[string]interface{}, *toolFailure) {
		b, _ := json.Marshal(args["ops"])
		seen = append(seen, string(b))
		if strings.Contains(string(b), `"uid"`) {
			return nil, &toolFailure{Code: "INVALID_ARGUMENT", Message: `ops[0]: unknown field "uid"`}
		}
		return map[string]interface{}{"applied": 1}, nil
	})
	// owner root: sent again without uid and gid, the body is not lost
	if err := h.Put(ctx, "a", "f", io.NopCloser(strings.NewReader("data")), PutOpts{Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || strings.Contains(seen[1], "uid") || !strings.Contains(seen[1], base64.StdEncoding.EncodeToString([]byte("data"))) {
		t.Errorf("calls %v", seen)
	}
	// another owner cannot be given, and the error says so
	err := h.Put(ctx, "a", "g", strings.NewReader("x"), PutOpts{Mode: 0o644, UID: 1000})
	if e, ok := err.(*Error); !ok || e.Kind != KindUsage || !strings.Contains(e.Msg, "--keep-owner") {
		t.Errorf("keep-owner: %v", err)
	}
}

func TestHubManifestDeleted(t *testing.T) {
	f, h, _ := hubFixture(t)
	var asked []interface{}
	f.on("ls", func(args map[string]interface{}) (map[string]interface{}, *toolFailure) {
		asked = append(asked, args["include_deleted"])
		return map[string]interface{}{"entries": []interface{}{
			map[string]interface{}{"path": "etc/old.conf", "type": "file", "origin": "image", "deleted": true},
			map[string]interface{}{"path": "etc/new.conf", "type": "file", "origin": "inlay", "mode": "0644", "size": 1},
		}}, nil
	})
	f.reply("hash", map[string]interface{}{"entries": []interface{}{map[string]interface{}{"path": "etc/new.conf", "sha256": "abc"}}})
	m, err := h.Manifest(context.Background(), "a")
	if err != nil || len(asked) != 1 || asked[0] != true {
		t.Fatalf("%v %v", err, asked)
	}
	want := []Entry{{Path: "etc/old.conf", Type: "deleted"}, {Path: "etc/new.conf", Type: "file", Size: 1, Mode: "0644", SHA256: "abc"}}
	if !reflect.DeepEqual(m.Files, want) {
		t.Errorf("files %+v", m.Files)
	}
	// the deleted path is known, so it is not deleted again
	d := ComputeDiff(nil, m, []string{"etc/old.conf", "etc/other"})
	if len(d.ToDelete) != 1 || d.ToDelete[0].Path != "etc/other" {
		t.Errorf("to delete %+v", d.ToDelete)
	}
}

// a Hub that ignores include_deleted never lists deleted entries; one that refuses it is asked again
func TestHubManifestOldHub(t *testing.T) {
	f, h, _ := hubFixture(t)
	calls := 0
	f.on("ls", func(args map[string]interface{}) (map[string]interface{}, *toolFailure) {
		calls++
		if _, ok := args["include_deleted"]; ok {
			return nil, &toolFailure{Code: "INVALID_ARGUMENT", Message: "unknown argument include_deleted"}
		}
		return map[string]interface{}{"entries": []interface{}{map[string]interface{}{"path": "a", "type": "dir", "origin": "inlay"}}}, nil
	})
	m, err := h.Manifest(context.Background(), "a")
	if err != nil || calls != 2 || len(m.Files) != 1 {
		t.Fatalf("%v calls %d %+v", err, calls, m.Files)
	}
	d := ComputeDiff(nil, m, []string{"gone"})
	if len(d.ToDelete) != 1 {
		t.Errorf("deleted paths should be listed as before: %+v", d.ToDelete)
	}
	// ignoring the argument gives the same manifest as before
	f.on("ls", func(args map[string]interface{}) (map[string]interface{}, *toolFailure) {
		return map[string]interface{}{"entries": []interface{}{map[string]interface{}{"path": "a", "type": "dir", "origin": "inlay"}}}, nil
	})
	if m2, err := h.Manifest(context.Background(), "a"); err != nil || !reflect.DeepEqual(m.Files, m2.Files) {
		t.Errorf("%v %+v", err, m2.Files)
	}
}

func TestHubStatusAllowed(t *testing.T) {
	f, h, _ := hubFixture(t)
	ctx := context.Background()
	for _, c := range []struct {
		name string
		val  interface{}
		set  bool
		want bool
	}{{"true", true, true, true}, {"false", false, true, false}, {"null", nil, true, true}, {"old Hub, missing", nil, false, true}} {
		r := map[string]interface{}{"connected": true, "devmode": false}
		if c.set {
			r["devmode_allowed"] = c.val
		}
		f.reply("get_devmode_context", r)
		st, err := h.Status(ctx)
		if err != nil || st.Enabled != c.want {
			t.Errorf("%s: %+v %v", c.name, st, err)
		}
	}
}

func TestHubManifest(t *testing.T) {
	f, h, _ := hubFixture(t)
	pages := 0
	f.on("ls", func(args map[string]interface{}) (map[string]interface{}, *toolFailure) {
		if args["recursive"] != true || args["path"] != "." {
			return nil, &toolFailure{Code: "INVALID", Message: "args"}
		}
		pages++
		if args["page"] == nil {
			return map[string]interface{}{"entries": []interface{}{
				map[string]interface{}{"path": "etc", "type": "dir", "mode": "0755", "origin": "inlay"},
				map[string]interface{}{"path": "etc/image-file", "type": "file", "origin": "image"},
				map[string]interface{}{"path": "etc/x.conf", "type": "file", "size": 5, "mode": "0644", "origin": "inlay"},
			}, "next_page": "p2"}, nil
		}
		return map[string]interface{}{"entries": []interface{}{
			map[string]interface{}{"path": "bin/ln", "type": "symlink", "target": "../x", "origin": "inlay", "mode": float64(0o777)},
			map[string]interface{}{"path": "sock", "type": "other", "origin": "inlay"},
		}}, nil
	})
	f.on("hash", func(args map[string]interface{}) (map[string]interface{}, *toolFailure) {
		if !reflect.DeepEqual(args["paths"], []interface{}{"etc/x.conf"}) {
			return nil, &toolFailure{Code: "INVALID", Message: fmt.Sprint(args["paths"])}
		}
		return map[string]interface{}{"entries": []interface{}{map[string]interface{}{"path": "etc/x.conf", "type": "file", "sha256": "abc"}}}, nil
	})
	m, err := h.Manifest(context.Background(), "a")
	if err != nil || pages != 2 {
		t.Fatalf("%v pages %d", err, pages)
	}
	want := []Entry{
		{Path: "etc", Type: "dir", Mode: "0755"},
		{Path: "etc/x.conf", Type: "file", Size: 5, Mode: "0644", SHA256: "abc"},
		{Path: "bin/ln", Type: "symlink", Mode: "0777", Target: "../x"},
		{Path: "sock", Type: "special"},
	}
	if !reflect.DeepEqual(m.Files, want) {
		t.Errorf("files %+v", m.Files)
	}
	// the manifest feeds the usual comparison
	d := ComputeDiff([]LocalFile{{Path: "etc/x.conf", Type: "file", Mode: 0o644, SHA256: "abc"}}, m, nil)
	if len(d.Changed) != 0 || len(d.Added) != 0 {
		t.Errorf("diff %+v", d)
	}
}

func TestHubGet(t *testing.T) {
	f, h, _ := hubFixture(t)
	ctx := context.Background()
	data := []byte("line one\nline two\n")
	sum := sha256.Sum256(data)
	f.on("get_file", func(args map[string]interface{}) (map[string]interface{}, *toolFailure) {
		off := int(args["offset"].(float64))
		// two chunks of 9 bytes
		end := off + 9
		out := map[string]interface{}{"size": len(data), "offset": off, "sha256": hex.EncodeToString(sum[:]), "eof": end >= len(data)}
		if off == 0 {
			out["text"] = string(data[:9])
		} else {
			out["base64"] = base64.StdEncoding.EncodeToString(data[off:])
		}
		return out, nil
	})
	var out bytes.Buffer
	if err := h.Get(ctx, "a", "etc/f", &out); err != nil || out.String() != string(data) {
		t.Fatalf("get %q %v", out.String(), err)
	}

	// a file that changes while it is read is refused
	f.on("get_file", func(args map[string]interface{}) (map[string]interface{}, *toolFailure) {
		return map[string]interface{}{"text": "x", "eof": true, "sha256": strings.Repeat("0", 64)}, nil
	})
	if err := h.Get(ctx, "a", "etc/f", io.Discard); err == nil {
		t.Error("a wrong sha256 should fail")
	}

	// a large file comes as a link, after one pending answer
	calls := 0
	f.on("get_file", func(args map[string]interface{}) (map[string]interface{}, *toolFailure) {
		if calls++; calls == 1 {
			return map[string]interface{}{"pending": true, "transfer_id": "t1"}, nil
		}
		if args["transfer_id"] != "t1" {
			return nil, &toolFailure{Code: "INVALID", Message: "no transfer id"}
		}
		return map[string]interface{}{"get_url": f.srv.URL + "/get/big", "size": 11}, nil
	})
	out.Reset()
	if err := h.Get(ctx, "a", "big", &out); err != nil || out.String() != "big content" {
		t.Fatalf("link get %q %v", out.String(), err)
	}
}
