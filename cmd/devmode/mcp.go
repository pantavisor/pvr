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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const mcpProtocolVersion = "2025-06-18"

// mcpClient calls the tools of the Hub's /mcp endpoint. The server keeps no
// session, so each call stands alone.
type mcpClient struct {
	url  string
	http *http.Client
	auth *hubAuth
	id   atomic.Int64
}

func newMCPClient(base string, auth *hubAuth) *mcpClient {
	return &mcpClient{url: strings.TrimRight(base, "/") + "/mcp", http: &http.Client{Timeout: 3 * time.Minute}, auth: auth}
}

// toolFailure is the error a Hub tool reports.
type toolFailure struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
	Details   json.RawMessage `json:"details,omitempty"`
}

// hubError turns a Hub tool error into a pvr error, keeping the message.
func hubError(f toolFailure) *Error {
	e := hubErrorKind(f)
	e.Code = f.Code
	return e
}

func hubErrorKind(f toolFailure) *Error {
	msg := f.Message
	if msg == "" {
		msg = f.Code
	}
	switch f.Code {
	case "DEV_MODE_NOT_ALLOWED":
		return errf(KindNotAllowed, msg)
	case "NOT_IN_DEV_MODE":
		return errf(KindNotActive, msg)
	case "REBOOT_NEEDED":
		return errf(KindRebootNeeded, msg)
	case "DEVICE_OFFLINE", "AGENT_NOT_AVAILABLE", "COMMAND_TIMEOUT":
		return errf(KindUnreachable, msg)
	case "APPLY_FAILED":
		return errf(KindApplyFailed, msg)
	}
	if f.Code != "" {
		msg = f.Code + ": " + msg
	}
	return errf(KindGeneric, msg)
}

type rpcReply struct {
	Result *struct {
		Content           []struct{ Type, Text string } `json:"content"`
		StructuredContent json.RawMessage               `json:"structuredContent"`
		IsError           bool                          `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// parseRPC reads a JSON answer or the data line of an event stream.
func parseRPC(ct string, b []byte) (rpcReply, error) {
	var r rpcReply
	if strings.HasPrefix(ct, "text/event-stream") {
		for _, line := range strings.Split(string(b), "\n") {
			if d, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "data:"); ok {
				b = []byte(strings.TrimSpace(d))
				break
			}
		}
	}
	err := json.Unmarshal(b, &r)
	return r, err
}

// Call runs a tool and returns what it answered.
func (m *mcpClient) Call(ctx context.Context, tool string, args map[string]interface{}) (map[string]interface{}, error) {
	if args == nil {
		args = map[string]interface{}{}
	}
	body, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": m.id.Add(1), "method": "tools/call",
		"params": map[string]interface{}{"name": tool, "arguments": args},
	})
	token, err := m.auth.Token(ctx)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", m.url, bytes.NewReader(body))
		if err != nil {
			return nil, errf(KindUsage, err.Error())
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("User-Agent", hubUserAgent)
		resp, err := m.http.Do(req)
		if err != nil {
			return nil, errf(KindUnreachable, "cannot reach the Hub: "+urlErrorText(err))
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if token, err = m.auth.Renew(ctx, token); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, hubErr("the Hub answered HTTP %d to %s", resp.StatusCode, tool)
		}
		return decodeTool(tool, resp.Header.Get("Content-Type"), b)
	}
}

func decodeTool(tool, ct string, b []byte) (map[string]interface{}, error) {
	r, err := parseRPC(ct, b)
	if err != nil {
		return nil, hubErr("the Hub answered something unexpected to %s", tool)
	}
	if r.Error != nil {
		return nil, hubErr("%s: %s", tool, r.Error.Message)
	}
	if r.Result == nil {
		return nil, hubErr("the Hub gave no result for %s", tool)
	}
	text := ""
	if len(r.Result.Content) > 0 {
		text = r.Result.Content[0].Text
	}
	if r.Result.IsError {
		var f toolFailure
		if json.Unmarshal([]byte(text), &f) == nil && (f.Code != "" || f.Message != "") {
			return nil, hubError(f)
		}
		return nil, errf(KindGeneric, strings.TrimSpace(text))
	}
	out := map[string]interface{}{}
	raw := []byte(r.Result.StructuredContent)
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte(text)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, hubErr("%s answered something that is not an object", tool)
	}
	return out, nil
}

// small readers for the loose JSON the tools answer with

func str(m map[string]interface{}, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprint(v)
	}
	return ""
}

func boolean(m map[string]interface{}, k string) bool {
	b, _ := m[k].(bool)
	return b
}

func list(m map[string]interface{}, k string) []map[string]interface{} {
	var out []map[string]interface{}
	l, _ := m[k].([]interface{})
	for _, i := range l {
		if e, ok := i.(map[string]interface{}); ok {
			out = append(out, e)
		}
	}
	return out
}
