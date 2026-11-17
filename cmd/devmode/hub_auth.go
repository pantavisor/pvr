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
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The Hub gives an MCP token to an application the user approves. pvr is
// the application and the user's pvr login approves it, so no browser opens.

const (
	hubClientName = "pvr-devmode"
	hubRedirect   = "http://127.0.0.1:8976/callback"
	hubUserAgent  = "pvr-devmode"
)

// hubTokens is what is kept for one Hub.
type hubTokens struct {
	ClientID     string    `json:"client_id,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

type hubTokenFile struct {
	Schema int                  `json:"schema"`
	Hubs   map[string]hubTokens `json:"hubs"`
}

// hubTokenStore keeps the tokens per Hub in <config dir>/devmode-hub.json,
// readable by the user only.
type hubTokenStore struct{ Path string }

func newHubTokenStore(configDir string) *hubTokenStore {
	return &hubTokenStore{Path: filepath.Join(configDir, "devmode-hub.json")}
}

func (s *hubTokenStore) load() hubTokenFile {
	f := hubTokenFile{Schema: 1, Hubs: map[string]hubTokens{}}
	if b, err := os.ReadFile(s.Path); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	if f.Hubs == nil {
		f.Hubs = map[string]hubTokens{}
	}
	return f
}

func (s *hubTokenStore) Get(hub string) hubTokens { return s.load().Hubs[hub] }

func (s *hubTokenStore) Set(hub string, t hubTokens) error {
	f := s.load()
	f.Schema = 1
	f.Hubs[hub] = t
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// LoginToken gives the token of the user's pvr login. With renew it asks
// the Hub for a fresh one first.
type LoginToken func(renew bool) (string, error)

// hubAuth gets and renews the MCP token of one Hub.
type hubAuth struct {
	base  string
	http  *http.Client
	store *hubTokenStore
	login LoginToken
	mu    sync.Mutex
}

func newHubAuth(base string, store *hubTokenStore, login LoginToken) *hubAuth {
	return &hubAuth{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 30 * time.Second}, store: store, login: login}
}

// Token gives a token that has not run out, getting one when needed.
func (a *hubAuth) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.store.Get(a.base)
	if t.AccessToken != "" && (t.ExpiresAt.IsZero() || time.Until(t.ExpiresAt) > 30*time.Second) {
		return t.AccessToken, nil
	}
	return a.renewLocked(ctx, t)
}

// Renew replaces a token the Hub refused.
func (a *hubAuth) Renew(ctx context.Context, refused string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := a.store.Get(a.base)
	if t.AccessToken != "" && t.AccessToken != refused {
		return t.AccessToken, nil // another call already renewed it
	}
	return a.renewLocked(ctx, t)
}

func (a *hubAuth) renewLocked(ctx context.Context, t hubTokens) (string, error) {
	if t.RefreshToken != "" && t.ClientID != "" {
		if nt, err := a.refresh(ctx, t); err == nil {
			return nt.AccessToken, nil
		}
	}
	nt, err := a.authorize(ctx, t.ClientID)
	if err != nil {
		return "", err
	}
	return nt.AccessToken, nil
}

func hubErr(format string, a ...interface{}) *Error {
	return errf(KindGeneric, fmt.Sprintf(format, a...))
}

// postForm and getJSON never put a token or a code into an error.
func (a *hubAuth) send(req *http.Request, out interface{}) (int, http.Header, error) {
	req.Header.Set("User-Agent", hubUserAgent)
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, nil, errf(KindUnreachable, "cannot reach the Hub: "+urlErrorText(err))
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if out != nil && resp.StatusCode/100 == 2 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp.StatusCode, resp.Header, hubErr("the Hub answered something unexpected at %s", req.URL.Path)
		}
	}
	return resp.StatusCode, resp.Header, nil
}

// urlErrorText drops the request URL, which can hold a code, from an error.
func urlErrorText(err error) string {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err.Error()
	}
	return err.Error()
}

func randomText(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (a *hubAuth) keep(t hubTokens, ans tokenAnswer) (hubTokens, error) {
	if ans.AccessToken == "" {
		return t, hubErr("the Hub gave no access token")
	}
	t.AccessToken = ans.AccessToken
	if ans.RefreshToken != "" {
		t.RefreshToken = ans.RefreshToken
	}
	t.ExpiresAt = time.Time{}
	if ans.ExpiresIn > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(ans.ExpiresIn) * time.Second)
	}
	if err := a.store.Set(a.base, t); err != nil {
		return t, hubErr("cannot save the Hub token: %v", err)
	}
	return t, nil
}

func (a *hubAuth) tokenRequest(ctx context.Context, form url.Values, ans *tokenAnswer) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", a.base+"/auth/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	code, _, err := a.send(req, ans)
	return code, err
}

func (a *hubAuth) resource(ctx context.Context) (resource string, scopes []string, err error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", a.base+"/.well-known/oauth-protected-resource/mcp", nil)
	var meta struct {
		Resource string   `json:"resource"`
		Scopes   []string `json:"scopes_supported"`
	}
	code, _, err := a.send(req, &meta)
	if err != nil {
		return "", nil, err
	}
	if code != 200 || meta.Resource == "" {
		return "", nil, hubErr("the Hub at %s has no MCP endpoint (HTTP %d)", a.base, code)
	}
	return meta.Resource, meta.Scopes, nil
}

// refresh uses the refresh token.
func (a *hubAuth) refresh(ctx context.Context, t hubTokens) (hubTokens, error) {
	resource, _, err := a.resource(ctx)
	if err != nil {
		return t, err
	}
	var ans tokenAnswer
	code, err := a.tokenRequest(ctx, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {t.RefreshToken},
		"client_id": {t.ClientID}, "resource": {resource},
	}, &ans)
	if err != nil {
		return t, err
	}
	if code != 200 {
		return t, hubErr("the Hub refused the refresh token (HTTP %d)", code)
	}
	return a.keep(t, ans)
}

func (a *hubAuth) register(ctx context.Context) (string, error) {
	b, _ := json.Marshal(map[string]interface{}{
		"client_name": hubClientName, "redirect_uris": []string{hubRedirect},
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", a.base+"/auth/oauth/register", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	var ans struct {
		ClientID string `json:"client_id"`
	}
	code, _, err := a.send(req, &ans)
	if err != nil {
		return "", err
	}
	if code/100 != 2 || ans.ClientID == "" {
		return "", hubErr("the Hub did not register pvr as an application (HTTP %d)", code)
	}
	return ans.ClientID, nil
}

// authorize runs the whole flow: register (once), ask for a code, approve it
// with the pvr login, and trade it for tokens.
func (a *hubAuth) authorize(ctx context.Context, clientID string) (hubTokens, error) {
	t := hubTokens{ClientID: clientID}
	resource, scopes, err := a.resource(ctx)
	if err != nil {
		return t, err
	}
	scope := strings.Join(append(append([]string{}, scopes...), "offline_access"), " ")
	for attempt := 0; ; attempt++ {
		if t.ClientID == "" {
			if t.ClientID, err = a.register(ctx); err != nil {
				return t, err
			}
		}
		nt, retry, err := a.codeFlow(ctx, t, resource, scope)
		if err == nil {
			return nt, nil
		}
		// a client id the Hub forgot: register again once
		if retry && attempt == 0 {
			t.ClientID = ""
			continue
		}
		return t, err
	}
}

// codeFlow returns retry when the Hub does not know the client id.
func (a *hubAuth) codeFlow(ctx context.Context, t hubTokens, resource, scope string) (hubTokens, bool, error) {
	verifier, err := randomText(48)
	if err != nil {
		return t, false, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state, _ := randomText(12)

	q := url.Values{
		"response_type": {"code"}, "client_id": {t.ClientID}, "redirect_uri": {hubRedirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {state},
		"scope": {scope}, "resource": {resource},
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", a.base+"/auth/oauth/authorize?"+q.Encode(), nil)
	noRedirect := *a.http
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req.Header.Set("User-Agent", hubUserAgent)
	resp, err := noRedirect.Do(req)
	if err != nil {
		return t, false, errf(KindUnreachable, "cannot reach the Hub: "+urlErrorText(err))
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return t, resp.StatusCode == 400 || resp.StatusCode == 401, hubErr("the Hub did not start the approval (HTTP %d)", resp.StatusCode)
	}
	lu, err := url.Parse(loc)
	if err != nil {
		return t, false, hubErr("the Hub sent a bad approval address")
	}
	lq := lu.Query()

	for renew := false; ; renew = true {
		login, err := a.login(renew)
		if err != nil {
			return t, false, err
		}
		body, _ := json.Marshal(map[string]string{
			"service": lq.Get("client_id"), "scopes": lq.Get("scope"), "state": lq.Get("state"),
			"redirect_uri": lq.Get("redirect_uri"), "response_type": lq.Get("response_type"), "auth_code": lq.Get("auth_code"),
		})
		creq, _ := http.NewRequestWithContext(ctx, "POST", a.base+"/auth/code", strings.NewReader(string(body)))
		creq.Header.Set("Content-Type", "application/json")
		creq.Header.Set("Authorization", "Bearer "+login)
		var back struct {
			RedirectURI string `json:"redirect_uri"`
		}
		code, _, err := a.send(creq, &back)
		if err != nil {
			return t, false, err
		}
		if code == 401 && !renew {
			continue // the pvr login ran out; ask for a new one once
		}
		if code != 200 {
			return t, false, hubErr("the Hub did not accept your pvr login to approve pvr as an application (HTTP %d); run pvr login with your user name and password (a personal access token cannot approve it)", code)
		}
		bu, err := url.Parse(back.RedirectURI)
		if err != nil || bu.Query().Get("code") == "" {
			return t, false, hubErr("the Hub gave no approval code")
		}
		var ans tokenAnswer
		code, err = a.tokenRequest(ctx, url.Values{
			"grant_type": {"authorization_code"}, "code": {bu.Query().Get("code")},
			"code_verifier": {verifier}, "redirect_uri": {hubRedirect},
			"client_id": {t.ClientID}, "resource": {resource},
		}, &ans)
		if err != nil {
			return t, false, err
		}
		if code != 200 {
			return t, false, hubErr("the Hub did not give a token for the approval (HTTP %d)", code)
		}
		nt, err := a.keep(t, ans)
		return nt, false, err
	}
}
