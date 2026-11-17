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
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fakeCGI answers the calls of the pass-through CGI of the pvr-sdk container.
type fakeCGI struct {
	mu    sync.Mutex
	calls []string
	body  map[string]string
	// reply gives the status and body for a "METHOD /path?query" line.
	reply func(call string) (int, string)
}

func (f *fakeCGI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	call := r.Method + " " + strings.TrimPrefix(r.URL.EscapedPath(), cgiPrefix)
	if r.URL.RawQuery != "" {
		call += "?" + r.URL.RawQuery
	}
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, call)
	if f.body == nil {
		f.body = map[string]string{}
	}
	f.body[call] = string(b)
	f.mu.Unlock()
	status, out := http.StatusOK, "{}"
	if f.reply != nil {
		status, out = f.reply(call)
	}
	w.WriteHeader(status)
	io.WriteString(w, out)
}

func newHTTPBackend(t *testing.T, f *fakeCGI) *HTTPBackend {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	h, err := NewHTTPBackend(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestParseHTTPTarget(t *testing.T) {
	for in, want := range map[string]string{
		"http://dev.local":      "http://dev.local:12368",
		"http://dev.local:8080": "http://dev.local:8080",
		"http://[::1]":          "http://[::1]:12368",
		"https://dev.local/":    "https://dev.local:12368",
	} {
		got, err := ParseHTTPTarget(in)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"http://", "ftp://x", "http://h/cgi-bin", "http://u@h"} {
		if _, err := ParseHTTPTarget(bad); err == nil {
			t.Errorf("%s should fail", bad)
		}
	}
}

func TestHTTPCalls(t *testing.T) {
	f := &fakeCGI{reply: func(call string) (int, string) {
		switch call {
		case "GET /devmode":
			return 200, `{"enabled":true,"active":true,"ready":true,"revision":"devmode/r/1","base":"1","containers":[{"name":"a"}]}`
		case "POST /devmode":
			return 200, `{"revision":"devmode/r/1","base":"1","resumed":false,"reboot":true}`
		case "GET /containers":
			return 200, `[{"name":"a","status":"STARTED","restart_policy":"container"},{"name":"b","restart_policy":"system"}]`
		case "GET /devmode/files/a":
			return 200, `{"container":"a","files":[{"path":"etc/x","type":"file","mode":"0644","sha256":"aa"}]}`
		case "GET /devmode/files/a/etc/a%20b.conf":
			return 200, "content"
		}
		return 200, "{}"
	}}
	h := newHTTPBackend(t, f)
	ctx := context.Background()

	st, err := h.Status(ctx)
	if err != nil || !st.Active || st.Base != "1" || len(st.Containers) != 1 {
		t.Fatalf("status %+v %v", st, err)
	}
	r, err := h.Enter(ctx, "locals/x", true)
	if err != nil || !r.Reboot || r.Revision != "devmode/r/1" {
		t.Fatalf("enter %+v %v", r, err)
	}
	if got := f.body["POST /devmode"]; got != `{"base":"locals/x","resume":true}` {
		t.Errorf("enter body %s", got)
	}
	pol, err := h.RestartPolicies(ctx)
	if err != nil || !reflect.DeepEqual(pol, map[string]string{"a": "container", "b": "system"}) {
		t.Fatalf("policies %v %v", pol, err)
	}
	m, err := h.Manifest(ctx, "a")
	if err != nil || len(m.Files) != 1 || m.Files[0].SHA256 != "aa" {
		t.Fatalf("manifest %+v %v", m, err)
	}
	var out bytes.Buffer
	if err := h.Get(ctx, "a", "etc/a b.conf", &out); err != nil || out.String() != "content" {
		t.Fatalf("get %q %v", out.String(), err)
	}
	h.Put(ctx, "a", "etc/a b.conf", strings.NewReader("hello"), PutOpts{Mode: 0o644})
	h.Put(ctx, "a", "var/empty", nil, PutOpts{Mode: 0o750, Type: "dir"})
	h.Put(ctx, "a", "bin/ln", nil, PutOpts{Type: "symlink", Target: "../x y"})
	h.Remove(ctx, "a", "old", true)
	h.Remove(ctx, "a", "f", false)
	h.Reset(ctx, "a")
	h.Apply(ctx, "b", true)
	h.Apply(ctx, "a", false)
	h.Leave(ctx, "1")

	want := []string{
		"GET /devmode",
		"POST /devmode",
		"GET /containers",
		"GET /devmode/files/a",
		"GET /devmode/files/a/etc/a%20b.conf",
		"PUT /devmode/files/a/etc/a%20b.conf?gid=0&mode=0644&uid=0",
		"PUT /devmode/files/a/var/empty?gid=0&mode=0750&type=dir&uid=0",
		"PUT /devmode/files/a/bin/ln?gid=0&target=..%2Fx+y&type=symlink&uid=0",
		"DELETE /devmode/files/a/old?recursive=1",
		"DELETE /devmode/files/a/f",
		"DELETE /devmode/containers/a",
		"PUT /containers/b?reboot=1",
		"PUT /containers/a",
		"POST /commands",
	}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}
	if f.body["PUT /devmode/files/a/etc/a%20b.conf?gid=0&mode=0644&uid=0"] != "hello" {
		t.Error("file content not sent")
	}
	if f.body["PUT /containers/a"] != `{"action":"restart"}` {
		t.Errorf("apply body %q", f.body["PUT /containers/a"])
	}
	if f.body["POST /commands"] != `{"op":"LOCAL_RUN","payload":"1"}` {
		t.Errorf("leave body %q", f.body["POST /commands"])
	}
}

func TestHTTPErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   Kind
	}{
		{403, `{"Error":"Devmode is disabled, enable PV_DEVMODE_ALLOWED in pantavisor.config or the OEM config"}`, KindNotAllowed},
		{409, `{"Error":"Not running a devmode revision"}`, KindNotActive},
		{409, `{"Error":"Container has restart_policy 'system', reboot needed; use ?reboot=1"}`, KindRebootNeeded},
		{404, `{"Error":"not forwarded"}`, KindGeneric},
		{500, "", KindGeneric},
	}
	for _, c := range cases {
		f := &fakeCGI{reply: func(string) (int, string) { return c.status, c.body }}
		h := newHTTPBackend(t, f)
		err := h.Reset(context.Background(), "a")
		e, ok := err.(*Error)
		if !ok || e.Kind != c.kind {
			t.Errorf("%d %s: got %v", c.status, c.body, err)
		}
	}
	// a restart that fails is an apply failure
	f := &fakeCGI{reply: func(string) (int, string) { return 500, `{"Error":"boom"}` }}
	err := newHTTPBackend(t, f).Apply(context.Background(), "a", false)
	if e, ok := err.(*Error); !ok || e.Kind != KindApplyFailed || e.Msg != "boom" {
		t.Errorf("apply: %v", err)
	}
}

func TestHTTPUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	h, _ := NewHTTPBackend(url)
	_, err := h.Status(context.Background())
	if e, ok := err.(*Error); !ok || e.Kind != KindUnreachable {
		t.Errorf("got %v", err)
	}
}

// a whole sync runs over the http backend, not only single calls
func TestHTTPSync(t *testing.T) {
	f := &fakeCGI{reply: func(call string) (int, string) {
		switch call {
		case "GET /devmode":
			return 200, `{"enabled":true,"active":true,"ready":true,"revision":"devmode/r/1","base":"1","containers":[{"name":"a"}]}`
		case "GET /containers":
			return 200, `[{"name":"a","restart_policy":"container"}]`
		}
		return 200, "{}"
	}}
	h := newHTTPBackend(t, f)
	e := &Env{C: h, Dir: t.TempDir(), Out: io.Discard, Err: io.Discard, NoWait: true}
	put(t, e, "a", "etc/x.conf", "1")
	if _, err := e.Sync(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	if !contains(f.calls, "DELETE /devmode/containers/a") || !contains(f.calls, "PUT /devmode/files/a/etc/x.conf?gid=0&mode=0644&uid=0") || !contains(f.calls, "PUT /containers/a") {
		t.Errorf("calls: %v", f.calls)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
