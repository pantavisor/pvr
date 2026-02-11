//
// Copyright 2017-2023  Pantacor Ltd.
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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-resty/resty"
	"github.com/urfave/cli"
)

const (
	ConfigurationFile = "config.json"
)

type CallMethod int

const (
	InteractiveCallMethod CallMethod = iota
	OAuthCallMethod
	NoneCallMethod
)

type Session struct {
	app           *cli.App
	auth          *PvrAuthConfig
	Configuration *PvrGlobalConfig
	configDir     string
}

func NewSession(app *cli.App) (*Session, error) {
	configDir := app.Metadata["PVR_CONFIG_DIR"].(string)
	authConfigPath := filepath.Join(configDir, "auth.json")
	generalConfigPath := filepath.Join(configDir, ConfigurationFile)

	authConfig, err := LoadConfig(authConfigPath)
	if err != nil {
		return nil, errors.New("Cannot load config: " + err.Error())
	}

	configuration, err := LoadConfiguration(generalConfigPath)
	if err != nil {
		return nil, err
	}

	return &Session{
		app:           app,
		auth:          authConfig,
		configDir:     configDir,
		Configuration: configuration,
	}, nil
}

func (s *Session) GetConfigDir() string {
	return s.configDir
}

func (s *Session) GetApp() *cli.App {
	return s.app
}

// Login : Login to a given URL
func (s *Session) Login(APIURL string, interactiveOnly bool, oauth bool) (*resty.Response, error) {
	//clear token
	u, err := url.Parse(APIURL)
	if err != nil {
		return nil, err
	}
	host := u.Scheme + "://" + u.Host
	authHeader := "JWT realm=\"pantahub services\", ph-aeps=\"" + host + "/auth\""
	err = s.auth.resetCachedAccessToken(authHeader)
	if err != nil {
		return nil, err
	}
	s.GetApp().Metadata["PVR_AUTH"] = ""
	method := NoneCallMethod
	if interactiveOnly {
		method = InteractiveCallMethod
	}
	if oauth {
		method = OAuthCallMethod
	}
	response, err := s.DoAuthCall(false, method, func(req *resty.Request) error {
		req.Method = resty.MethodGet
		req.URL = APIURL
		return nil
	})
	if err != nil {
		return response, err
	}
	return response, nil
}

func (s *Session) getNewCacheHeader() (string, error) {
	u, err := url.Parse(s.GetApp().Metadata["PVR_BASEURL"].(string))
	if err != nil {
		return "", err
	}
	host := u.Scheme + "://" + u.Host
	authHeader := "JWT realm=\"pantahub services\", ph-aeps=\"" + host + "/auth\""
	return authHeader, nil
}

func (s *Session) DoAuthCall(withAnonToken bool, method CallMethod, fn WrappableRestyCallFunc) (*resty.Response, error) {
	// Prepare the request using the callback
	req := resty.R()
	if err := fn(req); err != nil {
		return nil, err
	}

	// If it's a local device URL, skip authentication entirely
	if IsLocalDeviceURL(req.URL) {
		return req.Execute(req.Method, req.URL)
	}

	var bearer string
	var err error
	var response *resty.Response
	var authHeader string

	bearer = s.GetApp().Metadata["PVR_AUTH"].(string)
	user, userOk := s.GetApp().Metadata["PVR_USERNAME"].(string)
	password, passOk := s.GetApp().Metadata["PVR_PASSWORD"].(string)

	// if there is no bearer token, try to get one using the username/password
	if userOk && passOk && user != "" && password != "" {
		s.auth.User = user
		s.auth.Password = password
	}

	// retry methods array with order of preference
	retryMethods := []GetAccesstokenMethod{
		CachedAccessTokenMethod,
		RefreshTokenMethod,
		UserPassMethod,
	}

	if withAnonToken {
		retryMethods = append(retryMethods, AnonTokenMethod)
	}

	retryMethods = append(retryMethods, OAuthMethod)

	// rewrite all the retry methods if we only want interactive
	if method == InteractiveCallMethod {
		retryMethods = []GetAccesstokenMethod{
			InteractiveMethod,
		}
	}

	if method == OAuthCallMethod {
		retryMethods = []GetAccesstokenMethod{
			OAuthMethod,
		}
	}

	// Add one last retry with the last method, if it exists
	retryMethods = append(retryMethods, GetAccesstokenMethod(-1))
	for _, method := range retryMethods {
		var newAuthHeader string

		// Execute request with current bearer token
		req.SetAuthToken(bearer)
		response, err = req.Execute(req.Method, req.URL)
		if err != nil {
			break
		}

		// we continue looping for 401 and 403 error codes, everything
		// else are error conditions that need to be handled upstream
		if response.StatusCode() != http.StatusUnauthorized &&
			response.StatusCode() != http.StatusForbidden {
			err = nil
			break
		}

		// if we see www-authenticate, we need to auth ...
		newAuthHeader = response.Header().Get("www-authenticate")

		// first try cached accesstoken or get a new one
		if newAuthHeader != "" && authHeader != "" {
			s.auth.resetCachedAccessToken(authHeader)
		}
		authHeader = newAuthHeader

		if authHeader == "" {
			authHeader, err = s.getNewCacheHeader()
			if err != nil {
				continue
			}
		}

		switch method {
		case CachedAccessTokenMethod:
			bearer, err = s.auth.getCachedAccessToken(authHeader)
			if bearer != "" {
				s.GetApp().Metadata["PVR_AUTH"] = bearer
				continue
			}
		case RefreshTokenMethod:
			bearer, err = s.auth.getNewAccessToken(authHeader, RefreshTokenMethod)
			if bearer != "" {
				s.GetApp().Metadata["PVR_AUTH"] = bearer
				continue
			}
		case UserPassMethod:
			bearer, err = s.auth.getNewAccessToken(authHeader, UserPassMethod)
			if bearer != "" {
				s.GetApp().Metadata["PVR_AUTH"] = bearer
				continue
			}
		case AnonTokenMethod:
			bearer, err = s.auth.getNewAccessToken(authHeader, AnonTokenMethod)
			if bearer != "" {
				s.GetApp().Metadata["PVR_AUTH"] = bearer
				continue
			}
		case InteractiveMethod:
			bearer, err = s.auth.getNewAccessToken(authHeader, InteractiveMethod)
			if bearer != "" {
				s.GetApp().Metadata["PVR_AUTH"] = bearer
				continue
			}
		case OAuthMethod:
			bearer, err = s.auth.getNewAccessToken(authHeader, OAuthMethod)
			if bearer != "" {
				s.GetApp().Metadata["PVR_AUTH"] = bearer
				continue
			}
		}
	}

	// now that we would have a refreshed bearer, lets go again
	if bearer != "" {
		s.GetApp().Metadata["PVR_AUTH"] = bearer
	}

	if bearer == "" && err != nil {
		fmt.Fprintln(os.Stderr, "** ACCESS DENIED: user cannot access repository. **")
	}

	return response, err
}

// IsLocalDeviceURL checks if a URL points to a local device.
// Local devices are identified by the presence of /cgi-bin in the URL path,
// which is only present when talking directly to a Pantavisor device
// (via the CGI interface), not when going through the cloud API.
func IsLocalDeviceURL(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}

	// Check for /cgi-bin in the path - this indicates a direct device connection
	return strings.Contains(u.Path, "/cgi-bin")
}
