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
		dInfo, err := os.Stat(configPath)
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

func handlePKCELogin(apiURL string) (token string, refreshToken string, err error) {
	// Step 1: Generate code_verifier and code_challenge
	codeVerifierBytes := make([]byte, 32)
	_, err = rand.Read(codeVerifierBytes)
	if err != nil {
		return token, refreshToken, fmt.Errorf("Error generating code_verifier: %v", err)
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(codeVerifierBytes)

	sha256Hasher := sha256.New()
	sha256Hasher.Write([]byte(codeVerifier))
	codeChallengeBytes := sha256Hasher.Sum(nil)
	codeChallenge := base64.RawURLEncoding.EncodeToString(codeChallengeBytes)

	// Step 2: Initiate Authorization Request (Browser Redirect)
	// Find an available port for the local callback server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return token, refreshToken, fmt.Errorf("Error finding available port: %v", err)
	}
	defer listener.Close()
	redirectPort := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", redirectPort)

	stateBytes := make([]byte, 16)
	_, err = rand.Read(stateBytes)
	if err != nil {
		return token, refreshToken, fmt.Errorf("Error generating random state: %v", err)
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)

	authURL := fmt.Sprintf(
		"%s/auth/oauth/authorize?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&code_challenge=%s&code_challenge_method=S256&state=%s",
		apiURL,
		clientID,
		url.QueryEscape(redirectURI),
		url.QueryEscape("prn:pantahub.com:apis:/base/all"),
		url.QueryEscape(codeChallenge),
		url.QueryEscape(state))

	fmt.Printf("Opening browser for login. Please complete the authentication in your browser.\n")
	fmt.Printf("If your browser does not open automatically, please navigate to: %s\n", authURL)
	open.Run(authURL)

	codeCh := make(chan string)
	server := &http.Server{
		Addr: fmt.Sprintf(":%d", redirectPort),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			queryState := r.URL.Query().Get("state")
			if queryState != state {
				errStr := "Invalid state parameter"
				http.Error(w, errStr, http.StatusBadRequest)
				return
			}
			code := r.URL.Query().Get("code")
			if code == "" {
				errStr := "Authorization code not found"
				http.Error(w, errStr, http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, page)
			codeCh <- code
		}),
	}

	go func() {
		fmt.Printf("Starting local callback server on 127.0.0.1:%d\n", redirectPort)
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Local callback server error: %v\n", err)
		}
	}()

	var authCode string
	select {
	case authCode = <-codeCh:
	case <-time.After(5 * time.Minute):
		_ = server.Shutdown(context.Background())
		return token, refreshToken, fmt.Errorf("Timed out waiting for authorization code")
	}

	_ = server.Shutdown(context.Background())

	// Step 3: Client Exchanges Authorization Code for Access Token

	tokenURL := fmt.Sprintf("%s/auth/oauth/token", apiURL)
	tokenPayload := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"redirect_uri":  redirectURI,
		"access-code":   authCode,
		"code_verifier": codeVerifier,
		"scope":         "prn:pantahub.com:apis:/base/all",
	}

	var body []byte // Declare body here to match original structure

	restyResp, err := resty.R().
		SetHeader("Content-Type", "application/json").
		SetBody(tokenPayload).
		Post(tokenURL)

	if err != nil {
		return token, refreshToken, fmt.Errorf("Error making token exchange request: %v", err)
	}

	// resty handles closing the response body, so no explicit defer is needed.
	// The body is readily available from the response object.
	body = restyResp.Body()

	if restyResp.StatusCode() != http.StatusOK {
		return token, refreshToken, fmt.Errorf("Token exchange failed with status %d: %s", restyResp.StatusCode(), string(body))
	}

	response := PkceResponse{}
	err = json.Unmarshal(body, &response)
	if err != nil {
		return token, refreshToken, fmt.Errorf("Error unmarshalling token response: %v", err)
	}

	response.ExpiresIn = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second).Unix()
	return response.Token, response.Token, nil
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
		response, err := s.DoAuthCall(true, NoneCallMethod, func(req *resty.Request) (*resty.Response, error) {
			return req.Get(authEndPoint + "/auth_status")
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
		response, err := s.DoAuthCall(true, NoneCallMethod, func(req *resty.Request) (*resty.Response, error) {
			return req.Get(authEndPoint + "/auth_status")
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
