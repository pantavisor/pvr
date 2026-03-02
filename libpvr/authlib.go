//
// Copyright 2017-2023  Pantacor Ltd.
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
//

package libpvr

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-resty/resty"
	"github.com/skratchdot/open-golang/open"
	"gitlab.com/pantacor/pvr/utils/pvjson"
)

type GetAccesstokenMethod int

const (
	CachedAccessTokenMethod GetAccesstokenMethod = iota
	RefreshTokenMethod
	UserPassMethod
	AnonTokenMethod
	InteractiveMethod
	OAuthMethod
)

type PvrAuthTarget struct {
	// Name is the name of the target; 'default' for api.pantahub.com
	Name     string
	TargetEp *url.URL
}

type PvrAuthTokens struct {
	Name string
	// Name is the name of the target; 'default' for api.pantahub.com
	AccessToken  string `json:"access-token"`
	RefreshToken string `json:"refresh-token"`
}

type PvrAuthConfig struct {
	Spec     string                   `json:"spec"`
	Tokens   map[string]PvrAuthTokens `json:"tokens"`
	User     string                   `json:"-"`
	Password string                   `json:"-"`
	path     string
}

func LoadConfig(filePath string) (*PvrAuthConfig, error) {
	_, err := os.Stat(filePath)

	if os.IsNotExist(err) {
		_, err := os.Stat(filepath.Dir(filePath))
		if os.IsNotExist(err) {
			err = os.MkdirAll(filepath.Dir(filePath), 0700)
		} else {
			err = nil
		}

		return newDefaultAuthConfig(filePath), err
	}

	if err != nil {
		return nil, errors.New("oS error getting stats for file in LoadConfig: " + err.Error())
	}

	byteJson, err := os.ReadFile(filePath)

	if err != nil {
		return nil, errors.New("oS error reading config file LoadConfig: " + err.Error())
	}

	var authConfig PvrAuthConfig

	err = pvjson.Unmarshal(byteJson, &authConfig)
	if err != nil {
		return nil, errors.New("jSON Unmarshal error parsing config file in LoadConfig (" + filePath + "): " + err.Error())
	}

	authConfig.path = filePath
	return &authConfig, nil
}

func (p *PvrAuthConfig) DoRefresh(authEp, token string) (string, string, error) {
	m := map[string]string{
		"token": token,
	}

	if token == "" {
		return "", "", errors.New("doRefresh: no token provided")
	}

	if authEp == "" {
		return "", "", errors.New("doAuthenticate: no authentication endpoint provided")
	}
	response, err := resty.R().SetBody(m).
		SetAuthToken(token).
		Get(authEp + "/login")
	if err != nil {
		return "", "", err
	}
	m1 := map[string]interface{}{}
	err = pvjson.Unmarshal(response.Body(), &m1)

	if err != nil {
		return "", "", err
	}

	if response.StatusCode() != 200 {
		return "", "", nil
	}

	return m1["token"].(string), m1["token"].(string), nil
}

func (p *PvrAuthConfig) Save() error {

	if p.path == "" {
		return errors.New("not persistent authconfig")
	}

	configNew := p.path + ".tmp"
	configPath := p.path

	_, err := os.Stat(configPath)
	if os.IsNotExist(err) {
		d := filepath.Dir(configPath)
		dInfo, err := os.Stat(d)
		if os.IsNotExist(err) {
			// lets mkdir a personal dir if it does not exists.
			err = os.MkdirAll(d, 0700)
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if !dInfo.IsDir() {

				return errors.New("pvr config directrory is not a directory, but a file: " + d)
			}
			// if directory exists and is a directory, we are happy to continue and
			// attempt write file...
		}
	} else if err != nil {
		// all other errors are bad news and we return
		return err
	}

	byteJson, err := json.MarshalIndent(p, "", "    ")
	if err != nil {
		return err
	}

	err = os.WriteFile(configNew, byteJson, 0600)
	if err != nil {
		return err
	}

	err = RenameFile(configNew, configPath)
	return err
}

func doAuthenticate(authEp, username, password, scopes string) (string, string, error) {
	m := map[string]string{
		"username": username,
		"password": password,
	}

	if scopes != "" {
		m["scopes"] = scopes
	}

	if username == "" {
		return "", "", errors.New("doAuthenticate: no username provided")
	}
	if password == "" {
		return "", "", errors.New("doAuthenticate: no password provided")
	}
	if authEp == "" {
		return "", "", errors.New("doAuthenticate: no authentication endpoint provided")
	}

	response, err := resty.R().SetBody(m).Post(authEp + "/login")
	if err != nil {
		return "", "", err
	}

	m1 := map[string]interface{}{}
	err = pvjson.Unmarshal(response.Body(), &m1)

	if err != nil {
		return "", "", err
	}

	if response.StatusCode() != 200 {
		return "", "", errors.New("failed to Login: " + string(response.Body()))
	}

	_, ok := m1["token"]

	if !ok {
		return "", "", errors.New("illegal response: " + string(response.Body()))
	}
	return m1["token"].(string), m1["token"].(string), nil
}

func (p *PvrAuthConfig) resetCachedAccessToken(authHeader string) error {
	tokenKey, err := GetPhAuthHeaderTokenKey(authHeader)
	if err != nil {
		return err
	}
	delete(p.Tokens, tokenKey)
	return nil
}

func (p *PvrAuthConfig) getCachedAccessToken(authHeader string) (string, error) {

	tokenKey, err := GetPhAuthHeaderTokenKey(authHeader)
	if err != nil {
		return "", err
	}

	_, ok := p.Tokens[tokenKey]
	if ok && p.Tokens[tokenKey].AccessToken != "" {
		return p.Tokens[tokenKey].AccessToken, nil
	}

	return "", nil
}

// IsUserLoggedIn : To check if a user is logged in or not
func (session *Session) IsUserLoggedIn(baseURL string) (bool, error) {
	authEp := baseURL + "/auth"
	authHeader := "JWT realm=\"pantahub services\", ph-aeps=\"" + baseURL + "/auth\""
	authType, opts := getWwwAuthenticateInfo(authHeader)
	if authType != "JWT" && authType != "Bearer" {
		return false, nil
	}
	realm := opts["realm"]
	s, ok := session.auth.Tokens[authEp+" realm="+realm]
	if !ok {
		return false, nil
	}

	accessToken, refreshToken, err := session.auth.DoRefresh(authEp, s.RefreshToken)
	if err != nil {
		return false, err
	}
	if accessToken != "" && refreshToken != "" {
		return true, nil
	}
	return false, nil
}

func (p *PvrAuthConfig) getNewAccessToken(authHeader string, method GetAccesstokenMethod) (string, error) {

	authType, opts := getWwwAuthenticateInfo(authHeader)
	if authType != "JWT" && authType != "Bearer" {
		return "", errors.New("invalid www-authenticate header retrieved")
	}
	realm := opts["realm"]
	authEpString := opts["ph-aeps"]
	// _, requestFailed := opts["error"]
	authEps := strings.Split(authEpString, ",")

	if len(authEps) == 0 {
		return "", errors.New("bad Server Behaviour. Need ph-aeps token in Www-Authenticate header. Check your server version")
	}

	authEp := authEps[0]

	s, ok := p.Tokens[authEp+" realm="+realm]
	if !ok {
		s = PvrAuthTokens{}
	}

	s.AccessToken = ""
	var err error

	switch method {
	case RefreshTokenMethod:
		accessToken, refreshToken, err := p.DoRefresh(authEp, s.RefreshToken)
		if err != nil {
			return "", err
		}

		s.RefreshToken = refreshToken
		s.AccessToken = accessToken
		p.Tokens[authEp+" realm="+realm] = s
		p.Save()

		if accessToken != "" {
			return accessToken, nil
		}
	case UserPassMethod:
		accessToken, refreshToken, err := doAuthenticate(authEp, p.User, p.Password, "")
		if err != nil {
			return "", err
		}
		s.RefreshToken = refreshToken
		s.AccessToken = accessToken
		p.Tokens[authEp+" realm="+realm] = s
		p.Save()

		if accessToken != "" {
			return accessToken, nil
		}

	case AnonTokenMethod:
		username := "prn:pantahub.com:auth:/anon"
		password := "prn:pantahub.com:auth:/anon"
		scopes := "devices.readonly objects.readonly trails.readonly"

		accessToken, refreshToken, err := doAuthenticate(authEp, username, password, scopes)
		if err != nil {
			return "", err
		}

		if accessToken != "" {
			s.AccessToken = accessToken
			s.RefreshToken = refreshToken
			p.Tokens[authEp+" realm="+realm] = s
			p.Save()

			return accessToken, nil
		}
	case InteractiveMethod:
		username, password := readCredentials(authEp + " (realm=" + realm + ")")
		if username == "REGISTER" {
			email, username, password := readRegistration(authEp + " (realm=" + realm + ")")
			err = ShowOrOpenRegisterLink(authEp, email, username, password)
			if err != nil {
				log.Fatal("error registering with PH: " + err.Error())
				os.Exit(122)
			}
		}

		accessToken, refreshToken, err := doAuthenticate(authEp, username, password, "")
		if err != nil {
			return "", err
		}

		if accessToken != "" {
			s.AccessToken = accessToken
			s.RefreshToken = refreshToken
			p.Tokens[authEp+" realm="+realm] = s
			p.Save()

			return accessToken, nil
		}
	case OAuthMethod:
		parsedAuthEp, err := url.Parse(authEp)
		if err != nil {
			return "", errors.New("invalid authEp URL for OAuth: " + err.Error())
		}
		apiUrl := fmt.Sprintf("%s://%s", parsedAuthEp.Scheme, parsedAuthEp.Host)
		accessToken, refreshToken, err := handlePKCELogin(apiUrl)
		if err != nil {
			return "", err
		}
		if accessToken != "" {
			s.AccessToken = accessToken
			s.RefreshToken = refreshToken
			p.Tokens[authEp+" realm="+realm] = s
			p.Save()

			return accessToken, nil
		}
	default:
		return "", fmt.Errorf("unable to get access token, method %d not supported", method)
	}

	return "", errors.New("unable to get access token")
}

type PkceResponse struct {
	Token     string `json:"token"`
	ExpiresIn int64  `json:"expires_in"`
	Type      string `json:"token_type"`
}

func (r *PkceResponse) Body() []byte {
	return []byte(r.Token)
}

const clientID = "prn:pantahub.com:apis:/pvr"
const page = `
<html>
	<script>
		window.close();
	</script>
	<body>
		<h1>Login successful! You can close this window.</h1>
	</body>
</html>
`

type CLIInitResponse struct {
	AuthCode     string `json:"auth_code"`
	SessionID    string `json:"session_id"`
	AuthorizeURL string `json:"authorize_url"`
	ExpiresIn    int    `json:"expires_in"`
	Interval     int    `json:"interval"`
}

type PollTokenResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

func handlePKCELogin(apiURL string) (token string, refreshToken string, err error) {
	// Step 1: Generate code_verifier and code_challenge
	codeVerifierBytes := make([]byte, 32)
	_, err = rand.Read(codeVerifierBytes)
	if err != nil {
		return "", "", fmt.Errorf("error generating code_verifier: %v", err)
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(codeVerifierBytes)

	sha256Hasher := sha256.New()
	sha256Hasher.Write([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(sha256Hasher.Sum(nil))

	// Step 2: Start local callback server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", fmt.Errorf("error finding available port: %v", err)
	}
	defer listener.Close()
	redirectPort := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", redirectPort)

	stateBytes := make([]byte, 16)
	_, err = rand.Read(stateBytes)
	if err != nil {
		return "", "", fmt.Errorf("error generating random state: %v", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	// Step 3: Initialize PKCE session with API (NEW - no auth required)
	cliInitURL := fmt.Sprintf("%s/auth/oauth/pkce/init", apiURL)
	initPayload := map[string]string{
		"client_id":             clientID,
		"scope":                 "prn:pantahub.com:apis:/base/all",
		"redirect_uri":          redirectURI,
		"code_challenge":        codeChallenge,
		"code_challenge_method": "S256",
		"state":                 state,
	}

	resp, err := resty.R().
		SetHeader("Content-Type", "application/json").
		SetBody(initPayload).
		Post(cliInitURL)

	if err != nil {
		return "", "", fmt.Errorf("failed to initiate PKCE session: %v", err)
	}
	if resp.StatusCode() != http.StatusOK {
		return "", "", fmt.Errorf("PKCE init failed with status %d: %s",
			resp.StatusCode(), string(resp.Body()))
	}

	var initResp CLIInitResponse
	if err := json.Unmarshal(resp.Body(), &initResp); err != nil {
		return "", "", fmt.Errorf("failed to parse PKCE init response: %v", err)
	}

	// Step 4: Display authorization URL and open browser
	fmt.Printf("Opening browser for login...\n")
	fmt.Printf("If the browser doesn't open, visit this URL:\n\n")
	fmt.Printf("  %s\n\n", initResp.AuthorizeURL)

	go func() {
		open.Run(initResp.AuthorizeURL)
	}()

	// Step 5: Race callback vs polling
	callbackCh := make(chan string, 1)
	pollResultCh := make(chan PollTokenResponse, 1)
	doneCh := make(chan struct{})

	// Start callback server
	server := &http.Server{
		Addr: fmt.Sprintf(":%d", redirectPort),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("state") != state {
				http.Error(w, "Invalid state parameter", http.StatusBadRequest)
				return
			}
			code := r.URL.Query().Get("code")
			if code == "" {
				http.Error(w, "Authorization code not found", http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, page)
			select {
			case callbackCh <- code:
			default:
			}
		}),
	}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Local callback server error: %v\n", err)
		}
	}()

	// Start polling goroutine (NEW)
	go func() {
		tokenURL := fmt.Sprintf("%s/auth/oauth/token", apiURL)
		pollPayload := map[string]string{
			"grant_type":    "pkce_poll",
			"code":          initResp.AuthCode,
			"client_id":     clientID,
			"code_verifier": codeVerifier,
		}

		interval := time.Duration(initResp.Interval) * time.Second
		if interval < 5*time.Second {
			interval = 5 * time.Second
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-doneCh:
				return
			case <-ticker.C:
				resp, err := resty.R().
					SetHeader("Content-Type", "application/json").
					SetBody(pollPayload).
					Post(tokenURL)

				if err != nil {
					continue
				}

				var tokenResp PollTokenResponse
				if err := json.Unmarshal(resp.Body(), &tokenResp); err != nil {
					continue
				}

				if tokenResp.Error == "" && tokenResp.Token != "" {
					select {
					case pollResultCh <- tokenResp:
					default:
					}
					return
				}

				if tokenResp.Error == "slow_down" {
					interval += 5 * time.Second
					ticker.Reset(interval)
				}
			}
		}
	}()

	// Step 6: Wait for first completion
	var resultToken, resultRefreshToken string

	select {
	case authCode := <-callbackCh:
		// Callback received - exchange code for token (existing PKCE flow)
		close(doneCh)
		_ = server.Shutdown(context.Background())

		tokenURL := fmt.Sprintf("%s/auth/oauth/token", apiURL)
		tokenPayload := map[string]string{
			"grant_type":    "authorization_code",
			"client_id":     clientID,
			"redirect_uri":  redirectURI,
			"access-code":   authCode,
			"code_verifier": codeVerifier,
			"scope":         "prn:pantahub.com:apis:/base/all",
		}

		resp, err := resty.R().
			SetHeader("Content-Type", "application/json").
			SetBody(tokenPayload).
			Post(tokenURL)

		if err != nil {
			return "", "", fmt.Errorf("error exchanging code for token: %v", err)
		}
		if resp.StatusCode() != http.StatusOK {
			return "", "", fmt.Errorf("token exchange failed with status %d: %s",
				resp.StatusCode(), string(resp.Body()))
		}

		var pkceResp PkceResponse
		if err := json.Unmarshal(resp.Body(), &pkceResp); err != nil {
			return "", "", fmt.Errorf("error parsing token response: %v", err)
		}

		if pkceResp.Token == "" {
			return "", "", fmt.Errorf("token exchange succeeded but returned empty token: %s", string(resp.Body()))
		}

		resultToken = pkceResp.Token
		resultRefreshToken = pkceResp.Token

	case pollResult := <-pollResultCh:
		// Polling succeeded - got token directly
		close(doneCh)
		_ = server.Shutdown(context.Background())

		resultToken = pollResult.Token
		resultRefreshToken = pollResult.RefreshToken

	case <-time.After(time.Duration(initResp.ExpiresIn) * time.Second):
		close(doneCh)
		_ = server.Shutdown(context.Background())
		return "", "", fmt.Errorf("timed out waiting for authorization")
	}

	fmt.Printf("Authorization complete!\n")
	return resultToken, resultRefreshToken, nil
}

func newDefaultAuthConfig(filePath string) *PvrAuthConfig {
	return &PvrAuthConfig{
		Spec:   "1",
		Tokens: map[string]PvrAuthTokens{},
		path:   filePath,
	}
}

func (s *Session) GetWhoami() (nick string, err error) {
	pvrAuthConfig := s.auth
	for k := range pvrAuthConfig.Tokens {
		splits := strings.Split(k, " ")
		if len(splits) == 0 {
			continue
		}
		authEndPoint := splits[0]
		authStatusURL := authEndPoint + "/auth_status"
		response, err := s.DoAuthCall(true, NoneCallMethod, func(req *resty.Request) error {
			req.Method = resty.MethodGet
			req.URL = authStatusURL
			return nil
		})
		if err != nil {
			return nick, err
		}
		responseBody := map[string]interface{}{}
		err = pvjson.Unmarshal(response.Body(), &responseBody)
		if err != nil {
			return nick, err
		}
		if responseBody["nick"] == nil {
			continue
		}
		nick = responseBody["nick"].(string)
	}

	return nick, err
}

// Whoami : List all loggedin nick names
func (s *Session) Whoami() (err error) {
	pvrAuthConfig := s.auth
	for k := range pvrAuthConfig.Tokens {
		splits := strings.Split(k, " ")
		if len(splits) == 0 {
			continue
		}
		authEndPoint := splits[0]
		authStatusURL := authEndPoint + "/auth_status"
		response, err := s.DoAuthCall(true, NoneCallMethod, func(req *resty.Request) error {
			req.Method = resty.MethodGet
			req.URL = authStatusURL
			return nil
		})
		if err != nil {
			return err
		}
		responseBody := map[string]interface{}{}
		err = pvjson.Unmarshal(response.Body(), &responseBody)
		if err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, responseBody["nick"].(string)+"("+responseBody["prn"].(string)+") at "+authEndPoint+"\n")
	}

	return nil
}
