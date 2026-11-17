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

package device

import (
	"errors"
	"net/url"
	"path"
	"strings"

	"github.com/go-resty/resty"
	"github.com/pantavisor/pvr/libpvr"
	"github.com/pantavisor/pvr/utils/pvjson"
)

func isHexID(s string) bool {
	if len(s) != 24 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// deviceInfo holds the resolved API base URL, device identifier,
// and optionally a revision extracted from the URL.
type deviceInfo struct {
	BaseURL  string
	DeviceID string
	Rev      string // non-empty if the URL included a revision (e.g. .../9)
}

// resolveDevice takes the raw device argument (nick, ID, or pvr URL)
// and resolves it to an API base URL and device identifier.
//
// Supported formats:
//   - plain nick or ID: "labslave_var-som-mx8mn", "69bf4ccec0329e0009885cb9"
//   - pvr URL: "https://pvr.pantahub.com/asacasa/labslave_var-som-mx8mn"
//   - pvr URL with rev: "https://pvr.pantahub.com/asacasa/labslave_var-som-mx8mn/9"
func resolveDevice(session *libpvr.Session, defaultBaseURL, deviceArg string) (*deviceInfo, error) {
	if !strings.Contains(deviceArg, "://") {
		// Check if it's a valid hex ObjectID (24 chars) — if so, use directly
		if isHexID(deviceArg) {
			return &deviceInfo{
				BaseURL:  defaultBaseURL,
				DeviceID: deviceArg,
			}, nil
		}
		// It's a nick — resolve to device ID via devices API
		id, _, err := getDeviceInfo(session, defaultBaseURL, deviceArg)
		if err != nil {
			return nil, err
		}
		return &deviceInfo{
			BaseURL:  defaultBaseURL,
			DeviceID: id,
		}, nil
	}

	// It's a URL — resolve via .pvrremote
	pvrRemoteURL, err := url.Parse(deviceArg)
	if err != nil {
		return nil, errors.New("cannot parse URL: " + err.Error())
	}
	pvrRemoteURL.Path = path.Join(pvrRemoteURL.Path, ".pvrremote")

	response, err := session.DoAuthCall(true, libpvr.NoneCallMethod, func(req *resty.Request) error {
		req.Method = resty.MethodGet
		req.URL = pvrRemoteURL.String()
		return nil
	})
	if err != nil {
		return nil, errors.New("error resolving pvr remote: " + err.Error())
	}
	if response.StatusCode() != 200 {
		return nil, errors.New("error resolving pvr remote: " + string(response.Body()))
	}

	var remote struct {
		PostURL    string `json:"post-url"`
		JSONGetURL string `json:"json-get-url"`
	}
	err = pvjson.Unmarshal(response.Body(), &remote)
	if err != nil {
		return nil, errors.New("error parsing pvr remote: " + err.Error())
	}

	// post-url: https://api.pantahub.com/trails/69bf4ccec0329e0009885cb9/steps
	postURL, err := url.Parse(remote.PostURL)
	if err != nil {
		return nil, errors.New("error parsing post-url: " + err.Error())
	}

	parts := strings.Split(strings.Trim(postURL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "trails" {
		return nil, errors.New("unexpected post-url format: " + remote.PostURL)
	}
	deviceID := parts[1]
	apiBase := postURL.Scheme + "://" + postURL.Host

	// json-get-url: https://api.pantahub.com/trails/<id>/steps/<rev>/state
	// Extract rev if present
	rev := ""
	if remote.JSONGetURL != "" {
		getURL, err := url.Parse(remote.JSONGetURL)
		if err == nil {
			getParts := strings.Split(strings.Trim(getURL.Path, "/"), "/")
			// trails/<id>/steps/<rev>/state
			if len(getParts) >= 4 && getParts[2] == "steps" {
				rev = getParts[3]
			}
		}
	}

	return &deviceInfo{
		BaseURL:  apiBase,
		DeviceID: deviceID,
		Rev:      rev,
	}, nil
}
