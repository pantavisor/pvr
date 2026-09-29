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

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const testDeviceID = "0123456789abcdef01234567"

// httpCaller makes the hub calls with a plain HTTP client (the tests'
// stand-in for pvr's authenticated session).
func httpCaller(c *http.Client) hubCaller {
	return func(ctx context.Context, method, u string, body interface{}) (int, []byte, error) {
		var r io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return 0, nil, err
			}
			r = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, r)
		if err != nil {
			return 0, nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		return resp.StatusCode, data, err
	}
}

// fakeHub records the calls it gets; handlers answer them by
// "METHOD /path-after-/devices/<id>" (the path's session id replaced by
// {sid}).
type fakeHub struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	calls    []string
	bodies   map[string][]byte
	handlers map[string]http.HandlerFunc
}

func newFakeHub(t *testing.T) *fakeHub {
	h := &fakeHub{t: t, bodies: map[string][]byte{}, handlers: map[string]http.HandlerFunc{}}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHub) handle(route string, fn http.HandlerFunc) { h.handlers[route] = fn }

func (h *fakeHub) client() *hubClient {
	return &hubClient{baseURL: h.srv.URL, deviceID: testDeviceID, call: httpCaller(h.srv.Client())}
}

func (h *fakeHub) route(r *http.Request) string {
	p := strings.TrimPrefix(r.URL.Path, "/devices/"+testDeviceID)
	parts := strings.Split(p, "/")
	// /log-sessions/<sid>/... and /ssh-sessions/<sid>/... and /commands/<cid>
	if len(parts) > 2 {
		parts[2] = "{sid}"
	}
	return r.Method + " " + strings.Join(parts, "/")
}

func (h *fakeHub) serve(w http.ResponseWriter, r *http.Request) {
	route := h.route(r)
	body, _ := io.ReadAll(r.Body)
	h.mu.Lock()
	h.calls = append(h.calls, route)
	h.bodies[route] = body
	fn := h.handlers[route]
	h.mu.Unlock()
	if fn == nil {
		h.t.Errorf("unexpected call %s", route)
		http.Error(w, `{"msg":"unexpected"}`, http.StatusTeapot)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	fn(w, r)
}

func (h *fakeHub) count(route string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.calls {
		if c == route {
			n++
		}
	}
	return n
}

func (h *fakeHub) body(route string) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bodies[route]
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
