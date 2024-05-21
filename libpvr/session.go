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

	"github.com/go-resty/resty"
	"github.com/urfave/cli"
)

const (
	ConfigurationFile = "config.json"
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
func (s *Session) Login(APIURL string, interactiveOnly bool) (*resty.Response, error) {
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
	response, err := s.DoAuthCall(false, interactiveOnly, func(req *resty.Request) (*resty.Response, error) {
		return req.Get(APIURL)
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

func (s *Session) DoAuthCall(withAnonToken, interactiveOnly bool, fn WrappableRestyCallFunc) (*resty.Response, error) {

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

	if !interactiveOnly {
		retryMethods = append(retryMethods, InteractiveMethod)
	}

	// rewrite all the retry methods if we only want interactive
	if interactiveOnly {
		retryMethods = []GetAccesstokenMethod{
			InteractiveMethod,
		}
	}

	for _, method := range retryMethods {
		var newAuthHeader string

		response, err = fn(resty.R().SetAuthToken(bearer))
		// we continue looping for 401 and 403 error codes, everything
		// else are error conditions that need to be handled upstream
		if err == nil &&
			response.StatusCode() != http.StatusUnauthorized &&
			response.StatusCode() != http.StatusForbidden {
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
				continue
			}
		case RefreshTokenMethod:
			bearer, err = s.auth.getNewAccessToken(authHeader, RefreshTokenMethod)
			if bearer != "" {
				continue
			}
		case UserPassMethod:
			bearer, err = s.auth.getNewAccessToken(authHeader, UserPassMethod)
			if bearer != "" {
				continue
			}
		case AnonTokenMethod:
			bearer, err = s.auth.getNewAccessToken(authHeader, AnonTokenMethod)
			if bearer != "" {
				continue
			}
		case InteractiveMethod:
			bearer, err = s.auth.getNewAccessToken(authHeader, InteractiveMethod)
			if bearer != "" {
				continue
			}
		}
	}

	// now that we would have a refreshed bearer, lets go again
	if bearer != "" {
		s.GetApp().Metadata["PVR_AUTH"] = bearer
	} else {
		fmt.Fprintln(os.Stderr, "** ACCESS DENIED: user cannot access repository. **")
	}

	return response, err
}
