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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func useGitHubServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	srv := httptest.NewServer(h)
	prev := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = prev; srv.Close() })
	return srv
}

func TestResolveGitHubReleaseTag(t *testing.T) {
	useGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[
			{"tag_name":"main-snapshot","prerelease":true},
			{"tag_name":"060-rc1","prerelease":true},
			{"tag_name":"059","prerelease":false},
			{"tag_name":"058","prerelease":false}]`))
	})
	cases := map[string]string{
		"stable":   "059",
		"latest":   "main-snapshot",
		"master":   "main-snapshot",
		"develop":  "main-snapshot",
		"main":     "main-snapshot",
		"056":      "056",
		"049-rc1":  "049-rc1",
		"whatever": "whatever",
	}
	for in, want := range cases {
		got, err := resolveGitHubReleaseTag(in)
		if err != nil || got != want {
			t.Errorf("resolveGitHubReleaseTag(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestResolveGitHubStableWithoutRelease(t *testing.T) {
	useGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"main-snapshot","prerelease":true}]`))
	})
	if _, err := resolveGitHubReleaseTag("stable"); err == nil {
		t.Fatal("expected an error when there is no release")
	}
}

func testPvr(t *testing.T, tag string) *Pvr {
	return &Pvr{Session: &Session{
		Configuration: &PvrGlobalConfig{DistributionTag: tag},
		configDir:     t.TempDir(),
	}}
}

// The digest in the release matches the installed version file: nothing is downloaded.
func TestUpdateFromGitHubAlreadyCurrent(t *testing.T) {
	arch, ext, err := packageArchExt()
	if err != nil {
		t.Skip(err)
	}
	asset := "pvr.059." + arch + "." + ext
	useGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/releases/tags/059") {
			_, _ = w.Write([]byte(`{"tag_name":"059","assets":[{"name":"` + asset + `","digest":"sha256:abc123","browser_download_url":"http://127.0.0.1:1/never"}]}`))
			return
		}
		http.NotFound(w, r)
	})
	pvr := testPvr(t, "059")
	if err := os.WriteFile(filepath.Join(pvr.Session.configDir, lastUpdateFile), []byte("abc123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pvr.updateFromGitHub(true, false); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestUpdateFromGitHubDigestMismatch(t *testing.T) {
	arch, ext, err := packageArchExt()
	if err != nil {
		t.Skip(err)
	}
	asset := "pvr.059." + arch + "." + ext
	var srv *httptest.Server
	srv = useGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/releases/tags/059"):
			_, _ = w.Write([]byte(`{"tag_name":"059","assets":[{"name":"` + asset + `","digest":"sha256:0000","browser_download_url":"` + srv.URL + `/dl"}]}`))
		case r.URL.Path == "/dl":
			_, _ = w.Write([]byte("not a tarball"))
		default:
			http.NotFound(w, r)
		}
	})
	err = testPvr(t, "059").updateFromGitHub(true, true)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected a digest mismatch error, got %v", err)
	}
}

func TestUpdateFromGitHubMissingAsset(t *testing.T) {
	if _, _, err := packageArchExt(); err != nil {
		t.Skip(err)
	}
	useGitHubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"059","assets":[]}`))
	})
	if err := testPvr(t, "059").updateFromGitHub(true, true); err == nil {
		t.Fatal("expected an error for a release without the asset")
	}
}

// GitHub is asked first, then GitLab; docker is the last resort.
func TestUpdatePvrFallbackOrder(t *testing.T) {
	var mu sync.Mutex
	var hits []string
	record := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			hits = append(hits, name)
			mu.Unlock()
			http.Error(w, "down", http.StatusInternalServerError)
		}
	}
	useGitHubServer(t, record("github"))
	gl := httptest.NewServer(record("gitlab"))
	defer gl.Close()

	prevAPI, prevBase, prevList, prevDocker := gitlabProjectAPI, gitlabGenericBase, gitlabPackageList, dockerSources
	gitlabProjectAPI = gl.URL
	gitlabGenericBase = gl.URL + "/generic"
	gitlabPackageList = gl.URL + "/packages"
	dockerSources = nil
	t.Cleanup(func() {
		gitlabProjectAPI, gitlabGenericBase, gitlabPackageList, dockerSources = prevAPI, prevBase, prevList, prevDocker
	})

	if _, _, err := packageArchExt(); err != nil {
		t.Skip(err)
	}
	if err := testPvr(t, "stable").UpdatePvr("", "", true, true); err == nil {
		t.Fatal("expected an error when every source is down")
	}
	if len(hits) < 2 || hits[0] != "github" || hits[len(hits)-1] != "gitlab" {
		t.Fatalf("source order = %v, want github first and gitlab after", hits)
	}
}
