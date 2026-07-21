//
// Copyright 2017-2026  Pantacor Ltd.
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
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli"
)

func newTestSession(t *testing.T, baseURL string) *Session {
	t.Helper()

	app := &cli.App{
		Metadata: map[string]interface{}{
			"PVR_CONFIG_DIR": t.TempDir(),
			"PVR_BASEURL":    baseURL,
			"PVR_AUTH":       "",
		},
	}

	session, err := NewSession(app)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	return session
}

// TestLoginNetrcFailsFastWithoutFallback ensures that `pvr login --netrc`
// fails immediately with the real .netrc error when there is no matching
// entry, rather than silently falling back to interactive/oauth/etc or
// surfacing an opaque empty-body error.
func TestLoginNetrcFailsFastWithoutFallback(t *testing.T) {
	// isolate from any real .netrc on the test machine
	chdirTemp(t)
	t.Setenv("HOME", t.TempDir())

	var authEp string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`JWT realm="pantahub services", ph-aeps="%s"`, authEp))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	authEp = server.URL + "/auth"

	session := newTestSession(t, server.URL)

	_, err := session.Login(server.URL+"/auth/auth_status", false, false, true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "netrc") {
		t.Fatalf("expected a netrc-related error, got: %v", err)
	}
}

// TestLoginNetrcSucceedsWithMatchingCredentials ensures the happy path
// still works: a matching .netrc entry is used to obtain a token.
func TestLoginNetrcSucceedsWithMatchingCredentials(t *testing.T) {
	chdirTemp(t)
	t.Setenv("HOME", t.TempDir())

	var authEp string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/login" {
			w.Write([]byte(`{"token":"faketoken"}`))
			return
		}
		if r.Header.Get("Authorization") == "Bearer faketoken" {
			w.Write([]byte(`{"nick":"alice"}`))
			return
		}
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`JWT realm="pantahub services", ph-aeps="%s"`, authEp))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	authEp = server.URL + "/auth"

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("failed to parse test server URL: %v", err)
	}

	netrcContent := fmt.Sprintf("machine %s login alice password s3cr3t\n", u.Host)
	if err := os.WriteFile(filepath.Join(".", ".netrc"), []byte(netrcContent), 0600); err != nil {
		t.Fatalf("failed to write .netrc: %v", err)
	}

	session := newTestSession(t, server.URL)

	response, err := session.Login(server.URL+"/auth/auth_status", false, false, true)
	if err != nil {
		t.Fatalf("expected successful login, got error: %v", err)
	}
	if response.StatusCode() != http.StatusOK {
		t.Fatalf("expected 200 status, got %d", response.StatusCode())
	}
}
