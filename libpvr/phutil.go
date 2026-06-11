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
	"log"
	"net/url"
	"os"
	"time"

	"github.com/go-resty/resty"
	"github.com/skratchdot/open-golang/open"
	"gitlab.com/pantacor/pantahub-base/logs"
	"gitlab.com/pantacor/pvr/utils/pvjson"
)

const (
	// PhTrailsEp constant defining pantahub /trails
	PhTrailsEp = "/trails"

	// PhTrailsSummaryEp constant defines pantahub /trails/summary EP
	PhTrailsSummaryEp = PhTrailsEp + "/summary"

	// PhAccountsEp constant defines pantahub /accounts endpoint
	PhAccountsEp = "/auth/accounts"

	// PhLogsEp constant defining pantahub /logs/
	PhLogsEp = "/logs/"

	// PhLogsEpCursor constant defines path to /logs/cursor
	PhLogsEpCursor = PhLogsEp + "cursor"

	// PhDeviceTokens constant defines path to /devices/tokens
	PhDeviceTokens = "/devices/tokens"
)

// ShowOrOpenRegisterLink show or open the user the registration link
func ShowOrOpenRegisterLink(baseAPIURL, email, username, password string) error {
	encryptedAccount, err := GetEncryptedAccount(baseAPIURL, email, username, password)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\n\r\n\rYour registration process needs to be complete two steps \r\n")
	fmt.Fprintln(os.Stderr, "1.- Confirm you aren't a bot")
	fmt.Fprintln(os.Stderr, "2.- Confirm your email address")

	fmt.Fprintf(os.Stderr, "\n\r\n\rFollow this link to continue and after that come back and continue \r\n")
	fmt.Fprintf(os.Stderr, "%s \r\n\r\n", encryptedAccount.RedirectURI)

	open.Run(encryptedAccount.RedirectURI)

	return nil
}

// Account data model
type Account struct {
	Email    string `json:"email" bson:"email"`
	Nick     string `json:"nick" bson:"nick"`
	Password string `json:"password,omitempty" bson:"password"`
}

// EncryptedAccountData Encrypted account response
type EncryptedAccountData struct {
	Token       string `json:"token"`
	RedirectURI string `json:"redirect-uri"`
}

// GetEncryptedAccount encrypt account data in order to open the browser to finish the registration process
func GetEncryptedAccount(authEp, email, username, password string) (*EncryptedAccountData, error) {
	if authEp == "" {
		return nil, errors.New("getEncryptedAccount: no authentication endpoint provided")
	}
	if email == "" {
		return nil, errors.New("getEncryptedAccount: no email provided")
	}
	if username == "" {
		return nil, errors.New("getEncryptedAccount: no username provided")
	}
	if password == "" {
		return nil, errors.New("getEncryptedAccount: no password provided")
	}

	u1, err := url.Parse(authEp)
	if err != nil {
		return nil, errors.New("getEncryptedAccount: error parsing EP url")
	}

	accountsEp := u1.Scheme + "://" + u1.Hostname() + ":" + u1.Port() + PhAccountsEp

	m := Account{
		Email:    email,
		Nick:     username,
		Password: password,
	}

	response, err := resty.R().SetBody(m).
		Post(accountsEp)

	if err != nil {
		log.Fatal("Error calling POST for registration: " + err.Error())
		return nil, err
	}

	m1 := EncryptedAccountData{}
	err = pvjson.Unmarshal(response.Body(), &m1)

	if err != nil {
		log.Fatal("Error parsing Register body(" + err.Error() + ") for " + accountsEp + ": " + string(response.Body()))
		return nil, err
	}

	if response.StatusCode() != 200 {
		return nil, errors.New("Failed to register: " + string(response.Body()))
	}

	return &m1, nil
}

type PantahubDevice struct {
	Id               string    `json:"deviceid"`
	Prn              string    `json:"device"`
	Nick             string    `json:"device-nick"`
	Revision         int       `json:"revision"`
	ProgressRevision int       `json:"progress-revision"`
	RealIP           string    `json:"real-ip"`
	Timestamp        time.Time `json:"timestamp"`
	StateSha         string    `json:"state-sha"`
	Status           string    `json:"status"`
	StatusMsg        string    `json:"status-msg"`
}

type PantahubDevicesJoinToken struct {
	ID              string                 `json:"id"`
	Prn             string                 `json:"prn"`
	Nick            string                 `json:"nick"`
	Owner           string                 `json:"owner"`
	Token           string                 `json:"token,omitempty"`
	TokenSha        []byte                 `json:"token-sha,omitempty"`
	DefaultUserMeta map[string]interface{} `json:"default-user-meta"`
	Disabled        bool                   `json:"disabled"`
	TimeCreated     time.Time              `json:"time-created"`
	TimeModified    time.Time              `json:"time-modified"`
}

func (p *Session) TokenCreate(baseurl string, usermeta map[string]interface{}) (*PantahubDevicesJoinToken, error) {
	burl, err := url.Parse(baseurl)
	if err != nil {
		return nil, errors.New("Cannot parse baseurl '" + baseurl + "': " + err.Error())
	}

	dtUrl, err := url.Parse(PhDeviceTokens)
	if err != nil {
		return nil, errors.New("Cannot parse PhDeviceTokensUrl '" + dtUrl.String() + "': " + err.Error())
	}

	fullURL := burl.ResolveReference(dtUrl)

	token := PantahubDevicesJoinToken{
		DefaultUserMeta: usermeta,
	}
	body, err := pvjson.Marshal(token, pvjson.MarshalOptions{Compact: true})
	if err != nil {
		return nil, err
	}

	res, err := p.DoAuthCall(false, NoneCallMethod, func(req *resty.Request) error {
		req.Method = resty.MethodPost
		req.URL = fullURL.String()
		req.Body = body
		return nil
	})

	if err != nil {
		return nil, errors.New("ERROR: authenticated call to " + baseurl + " failed with: " + err.Error())
	}

	respBody := res.Body()
	var resultSet *PantahubDevicesJoinToken
	err = pvjson.Unmarshal(respBody, &resultSet)
	if err != nil {
		return nil, errors.New("ERROR: cannot decode result of authenticated call to " + baseurl + ": " + err.Error())
	}

	return resultSet, nil
}

func (p *Session) TokenDisable(baseurl string, id string) (*PantahubDevicesJoinToken, error) {
	burl, err := url.Parse(baseurl)
	if err != nil {
		return nil, errors.New("Cannot parse baseurl '" + baseurl + "': " + err.Error())
	}

	dtUrl, err := url.Parse(PhDeviceTokens + "/" + id)
	if err != nil {
		return nil, errors.New("Cannot parse PhDeviceTokensUrl '" + dtUrl.String() + "': " + err.Error())
	}

	fullURL := burl.ResolveReference(dtUrl)

	_, err = p.DoAuthCall(false, NoneCallMethod, func(req *resty.Request) error {
		req.Method = resty.MethodDelete
		req.URL = fullURL.String()
		return nil
	})

	if err != nil {
		return nil, errors.New("ERROR: authenticated call to " + baseurl + " failed with: " + err.Error())
	}

	resultSet := &PantahubDevicesJoinToken{
		ID:       id,
		Disabled: true,
	}

	return resultSet, nil
}

func (p *Session) TokenLs(baseurl string) ([]PantahubDevicesJoinToken, error) {
	burl, err := url.Parse(baseurl)
	if err != nil {
		return nil, errors.New("Cannot parse baseurl '" + baseurl + "': " + err.Error())
	}

	dtUrl, err := url.Parse(PhDeviceTokens)
	if err != nil {
		return nil, errors.New("Cannot parse PhDeviceTokensUrl '" + dtUrl.String() + "': " + err.Error())
	}

	fullURL := burl.ResolveReference(dtUrl)

	res, err := p.DoAuthCall(false, NoneCallMethod, func(req *resty.Request) error {
		req.Method = resty.MethodGet
		req.URL = fullURL.String()
		return nil
	})

	if err != nil {
		return nil, errors.New("ERROR: authenticated call to " + baseurl + " failed with: " + err.Error())
	}

	var resultSet []PantahubDevicesJoinToken
	err = pvjson.Unmarshal(res.Body(), &resultSet)

	if err != nil {
		return nil, errors.New("ERROR: cannot decode result of authenticated call to " + baseurl + ": " + err.Error())
	}

	return resultSet, nil
}

func (p *Session) DoPs(baseurl string) ([]PantahubDevice, error) {
	burl, err := url.Parse(baseurl)
	if err != nil {
		return nil, errors.New("Cannot parse baseurl '" + baseurl + "': " + err.Error())
	}

	trailSummaryEpURL, err := url.Parse(PhTrailsSummaryEp)
	if err != nil {
		return nil, errors.New("Cannot parse trailsSummaryEpURL '" + trailSummaryEpURL.String() + "': " + err.Error())
	}

	fullURL := burl.ResolveReference(trailSummaryEpURL)

	res, err := p.DoAuthCall(false, NoneCallMethod, func(req *resty.Request) error {
		req.Method = resty.MethodGet
		req.URL = fullURL.String()
		return nil
	})

	if err != nil {
		return nil, errors.New("ERROR: authenticated call to " + baseurl + " failed with: " + err.Error())
	}

	var resultSet []PantahubDevice
	err = pvjson.Unmarshal(res.Body(), &resultSet)

	if err != nil {
		return nil, errors.New("ERROR: cannot decode result of authenticated call to " + baseurl + ": " + err.Error())
	}

	return resultSet, nil
}

func (p *Session) DoLogsCursor(baseurl string, cursor string) (logEntries []*logs.Entry, cursorID string, err error) {
	burl, err := url.Parse(baseurl)
	if err != nil {
		return nil, "", errors.New("Cannot parse baseurl '" + baseurl + "': " + err.Error())
	}

	logsEpCursorURL, err := url.Parse(PhLogsEpCursor)
	if err != nil {
		return nil, "", errors.New("Cannot parse PhLogsEp '" + PhLogsEp + "': " + err.Error())
	}

	fullURL := burl.ResolveReference(logsEpCursorURL)

	bodyMap := map[string]interface{}{
		"next-cursor": cursor,
	}

	res, err := p.DoAuthCall(false, NoneCallMethod, func(req *resty.Request) error {
		req.Method = resty.MethodPost
		req.URL = fullURL.String()
		req.SetBody(bodyMap)
		return nil
	})

	if err != nil {
		return nil, "", errors.New("ERROR: authenticated call to " + baseurl + " failed with: " + err.Error())
	}

	var resultPage logs.Pager
	err = pvjson.Unmarshal(res.Body(), &resultPage)

	if err != nil {
		return nil, "", errors.New("ERROR: cannot decode result of authenticated call to " + baseurl + ": " + err.Error())
	}

	return resultPage.Entries, resultPage.NextCursor, nil
}

// LogFilter : Log Filter
type LogFilter struct {
	Devices   string
	Sources   string
	Levels    string
	Platforms string
}

func (p *Session) DoLogs(
	baseurl string,
	deviceIds []string,
	rev string,
	startTime *time.Time,
	endTime *time.Time,
	cursor bool,
	logFilter LogFilter,
) (logEntries []*logs.Entry, cursorID string, err error) {
	burl, err := url.Parse(baseurl)
	if err != nil {
		return nil, "", errors.New("cannot parse baseurl '" + baseurl + "': " + err.Error())
	}

	logsEpURL, err := url.Parse(PhLogsEp)
	if err != nil {
		return nil, "", errors.New("cannot parse PhLogsEp '" + PhLogsEp + "': " + err.Error())
	}

	fullURL := burl.ResolveReference(logsEpURL)
	q := fullURL.Query()

	// if cursor we enable in backend request too...
	if cursor {
		q.Add("cursor", "true")
		q.Add("page", "500")
	}

	q.Add("sort", "time-created")

	loc, _ := time.LoadLocation("UTC")

	if rev != "-1" {
		q.Add("rev", rev)
	}
	if startTime != nil {
		q.Add("after", startTime.In(loc).Format(time.RFC3339))
	}
	if endTime != nil && !endTime.IsZero() {
		q.Add("before", endTime.In(loc).Format(time.RFC3339))
	}
	if logFilter.Devices != "" {
		q.Add("dev", logFilter.Devices)
	}
	if logFilter.Sources != "" {
		q.Add("src", logFilter.Sources)
	}
	if logFilter.Levels != "" {
		q.Add("lvl", logFilter.Levels)
	}
	if logFilter.Platforms != "" {
		q.Add("plat", logFilter.Platforms)
	}

	fullURL.RawQuery = q.Encode()
	urlStr := fullURL.String()

	res, err := p.DoAuthCall(false, NoneCallMethod, func(req *resty.Request) error {
		req.Method = resty.MethodGet
		req.URL = urlStr
		return nil
	})

	if err != nil {
		return nil, "", errors.New("ERROR: authenticated call to " + baseurl + " failed with: " + err.Error())
	}

	var resultPage logs.Pager
	err = pvjson.Unmarshal(res.Body(), &resultPage)

	if err != nil {
		return nil, "", errors.New("ERROR: cannot decode result of authenticated call to " + baseurl + ": " + err.Error())
	}

	return resultPage.Entries, resultPage.NextCursor, nil
}
