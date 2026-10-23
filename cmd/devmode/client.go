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

// Client runs pvcontrol on the device through a Transport.
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

// RequireActive fails with not_allowed or not_active unless a devmode
// revision runs.
func (c *Client) RequireActive(ctx context.Context) (Status, error) {
	s, err := c.Status(ctx)
	if err != nil {
		return s, err
	}
	if !s.Enabled {
		return s, errf(KindNotAllowed, "Devmode is disabled, enable PV_DEVMODE_ALLOWED in pantavisor.config or the OEM config")
	}
	if !s.Active {
		return s, errf(KindNotActive, "Not running a devmode revision; run pvr devmode enter first")
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
}

func (c *Client) Put(ctx context.Context, container, path string, body io.Reader, o PutOpts) error {
	_, err := c.pvcontrol(ctx, body, nil, "devmode", "files", "put", container, path, "-",
		"--mode", fmt.Sprintf("%04o", o.Mode&0o7777),
		"--uid", strconv.Itoa(o.UID), "--gid", strconv.Itoa(o.GID))
	return err
}

func (c *Client) Remove(ctx context.Context, container, path string) error {
	_, err := c.pvcontrol(ctx, nil, nil, "devmode", "files", "rm", container, path)
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

// RestartPolicies maps container name to "container" or "system".
func (c *Client) RestartPolicies(ctx context.Context) (map[string]string, error) {
	b, err := c.pvcontrol(ctx, nil, nil, "containers", "ls")
	if err != nil {
		return nil, err
	}
	return ParsePolicies(b)
}

func ParsePolicies(b []byte) (map[string]string, error) {
	type item struct {
		Name          string `json:"name"`
		RestartPolicy string `json:"restart_policy"`
	}
	var list []item
	if err := json.Unmarshal(b, &list); err != nil {
		var wrap struct {
			Containers []item `json:"containers"`
		}
		if err2 := json.Unmarshal(b, &wrap); err2 != nil {
			return nil, errf(KindGeneric, "cannot read container list: "+err.Error())
		}
		list = wrap.Containers
	}
	m := map[string]string{}
	for _, i := range list {
		m[i.Name] = i.RestartPolicy
	}
	return m, nil
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
