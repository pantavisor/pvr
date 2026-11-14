//
// Copyright 2026  Pantacor Ltd.
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

// The Pantahub REST calls behind `pvr device live-logs` and `pvr device ssh`,
// made exactly as the Hub UI makes them: remote commands (LIST_LOG_SOURCES,
// LIST_CONTAINERS), log sessions and SSH sessions. Contracts: pv-mqtt-sdk
// docs/commands.md, docs/logs.md and docs/ssh.md.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-resty/resty"
	"github.com/pantavisor/pvr/libpvr"
)

// hubCaller makes one authenticated call: body, when not nil, is sent as
// JSON. It answers the HTTP status and the response body.
type hubCaller func(ctx context.Context, method, url string, body interface{}) (int, []byte, error)

// hubClient calls the Hub API about one device.
type hubClient struct {
	baseURL  string
	deviceID string
	call     hubCaller
}

// sessionCaller makes the calls with pvr's session, so they authenticate
// (and refresh the token, or log in) like every other pvr command.
func sessionCaller(session *libpvr.Session) hubCaller {
	return func(ctx context.Context, method, u string, body interface{}) (int, []byte, error) {
		resp, err := session.DoAuthCall(false, libpvr.NoneCallMethod, func(req *resty.Request) error {
			req.Method = method
			req.URL = u
			req.SetContext(ctx)
			if body != nil {
				req.SetHeader("Content-Type", "application/json")
				req.SetBody(body)
			}
			return nil
		})
		if err != nil {
			return 0, nil, err
		}
		if resp == nil {
			return 0, nil, errors.New("no response")
		}
		return resp.StatusCode(), resp.Body(), nil
	}
}

// deviceURL is the device's API URL with the given path segments, each
// escaped.
func (h *hubClient) deviceURL(parts ...string) string {
	u := strings.TrimRight(h.baseURL, "/") + "/devices/" + url.PathEscape(h.deviceID)
	for _, p := range parts {
		u += "/" + url.PathEscape(p)
	}
	return u
}

// apiError is an answer outside the expected statuses.
type apiError struct {
	Op     string
	Status int
	Msg    string
}

func (e *apiError) Error() string {
	msg := e.Msg
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("%s: %s (HTTP %d)", e.Op, msg, e.Status)
}

// statusOf is the HTTP status of an apiError, 0 for any other error.
func statusOf(err error) int {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

// maxShownText caps text from the API or the device that pvr prints.
const maxShownText = 256

// apiMessage is the reason an error answer gives: the Hub's `msg` (or
// `message`/`error`), else the body itself, cut and without control
// characters.
func apiMessage(body []byte) string {
	var v struct {
		Msg     string `json:"msg"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	text := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &v) == nil {
		switch {
		case v.Msg != "":
			text = v.Msg
		case v.Message != "":
			text = v.Message
		case v.Error != "":
			text = v.Error
		}
	}
	return shownText(text)
}

// shownText makes text that came from the network safe to print: control
// characters (an escape sequence could rewrite the terminal) become '?',
// and it is cut to maxShownText runes.
func shownText(text string) string {
	var b strings.Builder
	n := 0
	for _, r := range text {
		if n == maxShownText {
			b.WriteString("…")
			break
		}
		if isControl(r) {
			r = '?'
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// isControl: C0 (except tab), DEL and C1.
func isControl(r rune) bool {
	return (r < 0x20 && r != '\t') || (r >= 0x7f && r <= 0x9f)
}

// doJSON makes a call and decodes the answer into out (when not nil). Any
// status outside ok is an *apiError named after op.
func (h *hubClient) doJSON(ctx context.Context, op, method, u string, body, out interface{}, ok ...int) error {
	status, data, err := h.call(ctx, method, u, body)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s: %w", op, err)
	}
	for _, s := range ok {
		if status == s {
			if out == nil || len(data) == 0 {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("%s: cannot parse the answer: %w", op, err)
			}
			return nil
		}
	}
	return &apiError{Op: op, Status: status, Msg: apiMessage(data)}
}

// describeSessionError adds what an error status means for a session start.
func describeSessionError(err error) error {
	var ae *apiError
	if !errors.As(err, &ae) {
		return err
	}
	switch ae.Status {
	case http.StatusNotFound:
		return fmt.Errorf("%w: unknown device, or not yours", err)
	case http.StatusConflict:
		return fmt.Errorf("%w: the device is not connected to the Hub over MQTT", err)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: too many sessions, try again later", err)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: your token lacks the scope for this", err)
	}
	return err
}

// errSessionGone is a renew answered 410: the session has ended.
var errSessionGone = errors.New("the session has ended")

// renewInterval: sessions hold a 60 s lease, renewed every 20 s.
const renewInterval = 20 * time.Second

// renewLease calls renew every interval until ctx is done or renew answers
// errSessionGone. Other failures are reported to warn and retried at the next
// tick (the lease outlives two missed renewals).
func renewLease(ctx context.Context, interval time.Duration, renew func(context.Context) error, warn func(error)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		err := renew(ctx)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errSessionGone) {
			return
		}
		if err != nil && warn != nil {
			warn(err)
		}
	}
}

// sleepCtx waits d, or less if ctx is done first (answering false then).
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// backoff doubles base per failure in a row, up to max.
func backoff(failures int, base, max time.Duration) time.Duration {
	if failures < 1 {
		return base
	}
	d := float64(base) * math.Pow(2, float64(failures))
	if d > float64(max) {
		return max
	}
	return time.Duration(d)
}

// retryable: statuses a polling loop waits out rather than gives up on.
func retryable(status int) bool {
	return status == 0 || status == http.StatusTooManyRequests || status >= 500
}

// Remote commands (docs/commands.md).

const (
	commandPollInterval = time.Second
	// The API reports a command still pending after 120 s as timeout.
	commandMaxWait    = 125 * time.Second
	commandMaxBackoff = 8 * time.Second
)

type deviceCommand struct {
	ID     string          `json:"id"`
	Cmd    string          `json:"cmd"`
	Status string          `json:"status"`
	Code   int             `json:"code"`
	Error  string          `json:"error"`
	Output json.RawMessage `json:"output"`
}

// runCommand sends one of the allowlisted remote commands and polls it until
// the device answers. It returns the command's output as JSON: the API
// passes a body that is not JSON on as a JSON string, which is unwrapped.
func (h *hubClient) runCommand(ctx context.Context, cmd string, args map[string]interface{}, poll, maxWait time.Duration) (json.RawMessage, error) {
	if args == nil {
		args = map[string]interface{}{}
	}
	var doc deviceCommand
	err := h.doJSON(ctx, cmd, http.MethodPost, h.deviceURL("commands"),
		map[string]interface{}{"cmd": cmd, "args": args}, &doc, http.StatusCreated, http.StatusOK)
	if err != nil {
		return nil, describeSessionError(err)
	}
	if doc.ID == "" {
		return nil, fmt.Errorf("%s: the API answered no command id", cmd)
	}
	deadline := time.Now().Add(maxWait)
	failures := 0
	for doc.Status == "" || doc.Status == "pending" {
		wait := backoff(failures, poll, commandMaxBackoff)
		if time.Now().Add(wait).After(deadline) {
			return nil, fmt.Errorf("%s: the device did not answer in %s", cmd, maxWait)
		}
		if !sleepCtx(ctx, wait) {
			return nil, ctx.Err()
		}
		var next deviceCommand
		err := h.doJSON(ctx, cmd, http.MethodGet, h.deviceURL("commands", doc.ID), nil, &next, http.StatusOK)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if !retryable(statusOf(err)) {
				return nil, err
			}
			failures++
			continue
		}
		failures = 0
		doc = next
	}
	if doc.Status != "ok" {
		why := shownText(doc.Error)
		if why == "" {
			why = "no reason given"
		}
		return nil, fmt.Errorf("%s: %s (status %s, code %d)", cmd, why, shownText(doc.Status), doc.Code)
	}
	out := doc.Output
	var s string
	if len(out) > 0 && out[0] == '"' && json.Unmarshal(out, &s) == nil {
		out = json.RawMessage(s)
	}
	return out, nil
}
