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

package devmode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Client is the pvcontrol backend: it runs pvcontrol on the device through a
// Transport.
type Client struct{ T Transport }

type ContainerInfo struct {
	Name         string `json:"name"`
	Inlay        string `json:"inlay,omitempty"`
	InlayStorage string `json:"inlay_storage,omitempty"`
}

// Status is the answer of `pvcontrol devmode status`.
type Status struct {
	Enabled    bool            `json:"enabled"`
	Active     bool            `json:"active"`
	Ready      bool            `json:"ready"`
	Revision   string          `json:"revision"`
	Base       string          `json:"base"`
	Containers []ContainerInfo `json:"containers"`
}

// pvcontrol runs `pvcontrol <args>`. stdout gets the output when w is set,
// otherwise it is returned.
func (c *Client) pvcontrol(ctx context.Context, stdin io.Reader, w io.Writer, args ...string) ([]byte, error) {
	var out bytes.Buffer
	var errb bytes.Buffer
	var stdout io.Writer = &out
	if w != nil {
		stdout = w
	}
	code, err := c.T.Run(ctx, Join(append([]string{"pvcontrol"}, args...)...), stdin, stdout, &errb)
	if err != nil {
		return nil, errf(KindUnreachable, err.Error())
	}
	if code != 0 {
		if code == 127 {
			return nil, errf(KindGeneric, "pvcontrol not found on the device")
		}
		e := classify(errb.String())
		if e.Kind == KindGeneric && strings.TrimSpace(errb.String()) == "" {
			e.Msg = fmt.Sprintf("pvcontrol %s failed with exit code %d", strings.Join(args, " "), code)
		}
		return nil, e
	}
	return out.Bytes(), nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	b, err := c.pvcontrol(ctx, nil, nil, "devmode", "status")
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, errf(KindGeneric, "cannot read devmode status: "+err.Error())
	}
	return s, nil
}

type EnterResult struct {
	Revision string `json:"revision"`
	Base     string `json:"base"`
	Resumed  bool   `json:"resumed"`
	Running  bool   `json:"running"`
	Reboot   bool   `json:"reboot"`
}

func (c *Client) Enter(ctx context.Context, base string, resume bool) (EnterResult, error) {
	var r EnterResult
	args := []string{"devmode", "create"}
	if base != "" {
		args = append(args, base)
	}
	if resume {
		args = append(args, "--resume")
	}
	b, err := c.pvcontrol(ctx, nil, nil, args...)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, errf(KindGeneric, "cannot read devmode create answer: "+err.Error())
	}
	return r, nil
}

func (c *Client) Manifest(ctx context.Context, container string) (Manifest, error) {
	var m Manifest
	b, err := c.pvcontrol(ctx, nil, nil, "devmode", "files", "ls", container)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, errf(KindGeneric, "cannot read file list of "+container+": "+err.Error())
	}
	return m, nil
}

func (c *Client) Get(ctx context.Context, container, path string, w io.Writer) error {
	_, err := c.pvcontrol(ctx, nil, w, "devmode", "files", "get", container, path)
	return err
}

type PutOpts struct {
	Mode     uint32
	UID, GID int
	Type     string // file (default), dir or symlink
	Target   string // for a symlink
}

func (c *Client) Put(ctx context.Context, container, path string, body io.Reader, o PutOpts) error {
	args := []string{"devmode", "files", "put", container, path}
	switch o.Type {
	case "dir":
		args = append(args, "--type", "dir")
		body = nil
	case "symlink":
		args = append(args, "--type", "symlink", "--target", o.Target)
		body = nil
	default:
		args = append(args, "-")
	}
	args = append(args, "--uid", strconv.Itoa(o.UID), "--gid", strconv.Itoa(o.GID))
	if o.Type != "symlink" {
		args = append(args, "--mode", fmt.Sprintf("%04o", o.Mode&0o7777))
	}
	_, err := c.pvcontrol(ctx, body, nil, args...)
	return err
}

func (c *Client) Remove(ctx context.Context, container, path string, recursive bool) error {
	args := []string{"devmode", "files", "rm", container, path}
	if recursive {
		args = append(args, "--recursive")
	}
	_, err := c.pvcontrol(ctx, nil, nil, args...)
	return err
}

func (c *Client) Reset(ctx context.Context, container string) error {
	_, err := c.pvcontrol(ctx, nil, nil, "devmode", "reset", container)
	return err
}

// Apply restarts a container; with reboot it reboots the device.
func (c *Client) Apply(ctx context.Context, container string, reboot bool) error {
	args := []string{"devmode", "apply", container}
	if reboot {
		args = append(args, "--reboot")
	}
	_, err := c.pvcontrol(ctx, nil, nil, args...)
	if e, ok := err.(*Error); ok && e.Kind == KindGeneric {
		e.Kind = KindApplyFailed
	}
	return err
}

// ContainerState is one row of `pvcontrol containers ls`.
type ContainerState struct {
	Name          string          `json:"name"`
	Status        string          `json:"status"`
	RestartPolicy string          `json:"restart_policy"`
	PID           json.RawMessage `json:"pid"`
	StartedAt     json.RawMessage `json:"started_at"`
	Restarts      json.RawMessage `json:"restarts"`
}

// Same tells if two readings are of one run of the container.
func (s ContainerState) Same(o ContainerState) bool {
	return string(s.PID) == string(o.PID) && string(s.StartedAt) == string(o.StartedAt) &&
		string(s.Restarts) == string(o.Restarts)
}

func (c *Client) Containers(ctx context.Context) (map[string]ContainerState, error) {
	b, err := c.pvcontrol(ctx, nil, nil, "containers", "ls")
	if err != nil {
		return nil, err
	}
	return ParseContainers(b)
}

// RestartPolicies maps container name to "container" or "system".
func (c *Client) RestartPolicies(ctx context.Context) (map[string]string, error) {
	m, err := c.Containers(ctx)
	if err != nil {
		return nil, err
	}
	p := map[string]string{}
	for n, s := range m {
		p[n] = s.RestartPolicy
	}
	return p, nil
}

func ParseContainers(b []byte) (map[string]ContainerState, error) {
	var list []ContainerState
	if err := json.Unmarshal(b, &list); err != nil {
		var wrap struct {
			Containers []ContainerState `json:"containers"`
		}
		if err2 := json.Unmarshal(b, &wrap); err2 != nil {
			return nil, errf(KindGeneric, "cannot read container list: "+err.Error())
		}
		list = wrap.Containers
	}
	m := map[string]ContainerState{}
	for _, i := range list {
		m[i.Name] = i
	}
	return m, nil
}

func ParsePolicies(b []byte) (map[string]string, error) {
	m, err := ParseContainers(b)
	if err != nil {
		return nil, err
	}
	p := map[string]string{}
	for n, s := range m {
		p[n] = s.RestartPolicy
	}
	return p, nil
}

// Leave runs the given committed revision again.
func (c *Client) Leave(ctx context.Context, rev string) error {
	_, err := c.pvcontrol(ctx, nil, nil, "cmd", "run", rev)
	return err
}

// ApplyAction is what apply does for a container.
func ApplyAction(policy string) string {
	switch policy {
	case "container":
		return "restart"
	case "system":
		return "reboot"
	}
	return "unknown"
}
