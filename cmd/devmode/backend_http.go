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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultHTTPPort is where the web server of the pvr-sdk container listens.
const DefaultHTTPPort = 12368

// cgiPrefix is the CGI of the pvr-sdk container that passes calls to pv-ctrl.
const cgiPrefix = "/cgi-bin/pvctrl"

// HTTPBackend talks to pv-ctrl through the pass-through CGI of the pvr-sdk
// container.
type HTTPBackend struct {
	Base   string // http://host:port, no trailing slash
	Client *http.Client
}

var _ Backend = (*HTTPBackend)(nil)

// ParseHTTPTarget turns http://host[:port] into the base URL of the CGI,
// adding the default port.
func ParseHTTPTarget(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("bad device address %q: want http://host[:port]", s)
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.User != nil {
		return "", fmt.Errorf("bad device address %q: give only the host and port", s)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), strconv.Itoa(DefaultHTTPPort))
	}
	return u.Scheme + "://" + host, nil
}

func NewHTTPBackend(target string) (*HTTPBackend, error) {
	base, err := ParseHTTPTarget(target)
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	return &HTTPBackend{Base: base, Client: &http.Client{Transport: tr}}, nil
}

func (h *HTTPBackend) Close() error {
	h.Client.CloseIdleConnections()
	return nil
}

// escapePath percent-encodes each segment of a container path.
func escapePath(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// bodyLength gives the size of a body when it is known, so the request has a
// Content-Length that pv-ctrl needs; an unknown body is read first.
func bodyLength(body io.Reader) (io.Reader, int64, error) {
	switch b := body.(type) {
	case nil:
		return nil, 0, nil
	case *os.File:
		st, err := b.Stat()
		if err != nil {
			return nil, 0, err
		}
		return b, st.Size(), nil
	case *bytes.Reader:
		return b, int64(b.Len()), nil
	case *strings.Reader:
		return b, int64(b.Len()), nil
	}
	all, err := io.ReadAll(body)
	if err != nil {
		return nil, 0, err
	}
	return bytes.NewReader(all), int64(len(all)), nil
}

// do sends one call. It returns the response when the status is 200; any
// other status becomes an error with the message of pv-ctrl.
func (h *HTTPBackend) do(ctx context.Context, method, path, query string, body io.Reader, hdr map[string]string) (*http.Response, error) {
	body, n, err := bodyLength(body)
	if err != nil {
		return nil, errf(KindGeneric, err.Error())
	}
	u := h.Base + cgiPrefix + path
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, errf(KindUsage, err.Error())
	}
	if body != nil {
		req.ContentLength = n
		if n == 0 {
			req.Body = http.NoBody
		}
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, errf(KindUnreachable, err.Error())
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return nil, httpError(resp.StatusCode, b)
}

// httpError turns a pv-ctrl error answer into an error.
func httpError(status int, body []byte) *Error {
	e := classify(string(body))
	if status == http.StatusBadRequest {
		e.Code = "HTTP_400"
	}
	if e.Kind == KindGeneric && strings.TrimSpace(string(body)) == "" {
		e.Msg = fmt.Sprintf("the device answered HTTP %d", status)
	}
	if status == http.StatusNotFound && strings.Contains(e.Msg, "not forwarded") {
		e.Msg = "the pvr-sdk web server does not pass this call to pv-ctrl (" + e.Msg + ")"
	}
	return e
}

func (h *HTTPBackend) json(ctx context.Context, method, path, query string, in, out interface{}) error {
	var body io.Reader
	hdr := map[string]string{}
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
		hdr["Content-Type"] = "application/json"
	}
	resp, err := h.do(ctx, method, path, query, body, hdr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return errf(KindUnreachable, err.Error())
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return errf(KindGeneric, "cannot read the answer of "+path+": "+err.Error())
	}
	return nil
}

func (h *HTTPBackend) Status(ctx context.Context) (Status, error) {
	var s Status
	err := h.json(ctx, "GET", "/devmode", "", nil, &s)
	return s, err
}

func (h *HTTPBackend) Enter(ctx context.Context, base string, resume bool) (EnterResult, error) {
	var r EnterResult
	in := map[string]interface{}{"resume": resume}
	if base != "" {
		in["base"] = base
	}
	err := h.json(ctx, "POST", "/devmode", "", in, &r)
	return r, err
}

func (h *HTTPBackend) Leave(ctx context.Context, rev string) error {
	return h.json(ctx, "POST", "/commands", "", map[string]string{"op": "LOCAL_RUN", "payload": rev}, nil)
}

func (h *HTTPBackend) Manifest(ctx context.Context, container string) (Manifest, error) {
	var m Manifest
	err := h.json(ctx, "GET", "/devmode/files/"+url.PathEscape(container), "", nil, &m)
	return m, err
}

func (h *HTTPBackend) Get(ctx context.Context, container, path string, w io.Writer) error {
	resp, err := h.do(ctx, "GET", "/devmode/files/"+url.PathEscape(container)+"/"+escapePath(path), "", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(w, resp.Body); err != nil {
		return errf(KindUnreachable, err.Error())
	}
	return nil
}

// Tree asks pv-ctrl for the folder as a tar.
func (h *HTTPBackend) Tree(ctx context.Context, container, p string) (io.ReadCloser, error) {
	p, err := treePath(p)
	if err != nil {
		return nil, errf(KindUsage, err.Error())
	}
	path := "/devmode/files/" + url.PathEscape(container)
	if p != "" {
		path += "/" + escapePath(p)
	}
	resp, err := h.do(ctx, "GET", path, "format=tar", nil, nil)
	if err != nil {
		// a pantavisor from before the tar format answers 400
		if e, ok := err.(*Error); ok && e.Code == "HTTP_400" {
			return nil, errNoTar(e.Msg)
		}
		return nil, err
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-tar") {
		resp.Body.Close()
		return nil, errNoTar("the answer is " + ct)
	}
	return resp.Body, nil
}

func (h *HTTPBackend) Put(ctx context.Context, container, path string, body io.Reader, o PutOpts) error {
	q := url.Values{}
	switch o.Type {
	case "dir":
		q.Set("type", "dir")
		body = nil
	case "symlink":
		q.Set("type", "symlink")
		q.Set("target", o.Target)
		body = nil
	default:
		if body == nil {
			body = bytes.NewReader(nil)
		}
	}
	q.Set("uid", strconv.Itoa(o.UID))
	q.Set("gid", strconv.Itoa(o.GID))
	if o.Type != "symlink" {
		q.Set("mode", fmt.Sprintf("%04o", o.Mode&0o7777))
	}
	resp, err := h.do(ctx, "PUT", "/devmode/files/"+url.PathEscape(container)+"/"+escapePath(path), q.Encode(), body, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (h *HTTPBackend) Remove(ctx context.Context, container, path string, recursive bool) error {
	q := ""
	if recursive {
		q = "recursive=1"
	}
	resp, err := h.do(ctx, "DELETE", "/devmode/files/"+url.PathEscape(container)+"/"+escapePath(path), q, nil, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Reset empties the inlay of a container: DELETE of the container with no
// path. With a path the same route removes one file.
func (h *HTTPBackend) Reset(ctx context.Context, container string) error {
	return h.json(ctx, "DELETE", "/devmode/files/"+url.PathEscape(container), "", nil, nil)
}

func (h *HTTPBackend) Apply(ctx context.Context, container string, reboot bool) error {
	q := ""
	if reboot {
		q = "reboot=1"
	}
	err := h.json(ctx, "PUT", "/containers/"+url.PathEscape(container), q, map[string]string{"action": "restart"}, nil)
	if e, ok := err.(*Error); ok && e.Kind == KindGeneric {
		e.Kind = KindApplyFailed
	}
	return err
}

func (h *HTTPBackend) Containers(ctx context.Context) (map[string]ContainerState, error) {
	resp, err := h.do(ctx, "GET", "/containers", "", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errf(KindUnreachable, err.Error())
	}
	return ParseContainers(b)
}

func (h *HTTPBackend) RestartPolicies(ctx context.Context) (map[string]string, error) {
	return policiesOf(h.Containers(ctx))
}

// policiesOf maps container name to restart policy.
func policiesOf(m map[string]ContainerState, err error) (map[string]string, error) {
	if err != nil {
		return nil, err
	}
	p := map[string]string{}
	for n, s := range m {
		p[n] = s.RestartPolicy
	}
	return p, nil
}

// Exec uses POST /containers/<name>/exec of pv-ctrl.
func (h *HTTPBackend) Exec(ctx context.Context, container string, argv []string, o ExecOpts) (ExecResult, error) {
	var r ExecResult
	secs := o.seconds()
	in := map[string]interface{}{"argv": argv, "timeout_s": secs}
	if o.MaxOutput > 0 {
		in["max_output"] = o.MaxOutput
	}
	if o.Cwd != "" {
		in["cwd"] = o.Cwd
	}
	if len(o.Env) > 0 {
		in["env"] = o.Env
	}
	if len(o.Stdin) > 0 {
		in["stdin_b64"] = base64.StdEncoding.EncodeToString(o.Stdin)
	}
	// the command is stopped after timeout_s, plus the time pv-ctrl needs to end it
	ctx, cancel := context.WithTimeout(ctx, time.Duration(secs+15)*time.Second)
	defer cancel()
	var out struct {
		ExitCode  *int   `json:"exit_code"`
		Signal    *int   `json:"signal"`
		TimedOut  bool   `json:"timed_out"`
		StdoutB64 string `json:"stdout_b64"`
		StderrB64 string `json:"stderr_b64"`
		Truncated struct {
			Stdout bool `json:"stdout"`
			Stderr bool `json:"stderr"`
		} `json:"truncated"`
		DurationMS int64 `json:"duration_ms"`
	}
	if err := h.json(ctx, "POST", "/containers/"+url.PathEscape(container)+"/exec", "", in, &out); err != nil {
		return r, err
	}
	so, err1 := base64.StdEncoding.DecodeString(out.StdoutB64)
	se, err2 := base64.StdEncoding.DecodeString(out.StderrB64)
	if err1 != nil || err2 != nil {
		return r, errf(KindGeneric, "the device sent command output that is not base64")
	}
	return ExecResult{ExitCode: out.ExitCode, Signal: out.Signal, TimedOut: out.TimedOut, Stdout: string(so), Stderr: string(se),
		TruncStdout: out.Truncated.Stdout, TruncStderr: out.Truncated.Stderr, DurationMS: out.DurationMS}, nil
}

// Logs uses the logs CGI of the pvr-sdk container. The revision is given
// explicitly, because the CGI would read the committed one.
func (h *HTTPBackend) Logs(ctx context.Context, container string, tail int) ([]string, error) {
	if err := logName("container", container); err != nil {
		return nil, err
	}
	rev, err := runningRevision(ctx, h)
	if err != nil {
		return nil, err
	}
	if err := logName("revision", rev); err != nil {
		return nil, err
	}
	// the CGI does not decode the query, so the slashes stay as they are
	q := "rev=" + rev + "&source=" + consoleLogSource(container) + "&tail=1"
	if tail > 0 {
		q += "&n=" + strconv.Itoa(tail)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", h.Base+"/cgi-bin/logs?"+q, nil)
	if err != nil {
		return nil, errf(KindUsage, err.Error())
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, errf(KindUnreachable, err.Error())
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, errf(KindUnreachable, err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errf(KindGeneric, fmt.Sprintf("cannot read the console log of %s in %s (HTTP %d): %s", container, rev, resp.StatusCode, firstTextLine(string(b), "no answer text")))
	}
	return logLines(string(b)), nil
}
