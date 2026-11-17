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
	"context"
	"io"
	"time"
)

// Backend is what a devmode command needs from a device. The pvcontrol,
// http and hub transports each implement it.
type Backend interface {
	Status(ctx context.Context) (Status, error)
	Enter(ctx context.Context, base string, resume bool) (EnterResult, error)
	Leave(ctx context.Context, rev string) error
	Manifest(ctx context.Context, container string) (Manifest, error)
	Get(ctx context.Context, container, path string, w io.Writer) error
	Put(ctx context.Context, container, path string, body io.Reader, o PutOpts) error
	Remove(ctx context.Context, container, path string, recursive bool) error
	Reset(ctx context.Context, container string) error
	// Apply restarts a container; with reboot it reboots the device.
	Apply(ctx context.Context, container string, reboot bool) error
	Containers(ctx context.Context) (map[string]ContainerState, error)
	RestartPolicies(ctx context.Context) (map[string]string, error)
	// Exec runs a command in a running container, without a shell.
	Exec(ctx context.Context, container string, argv []string, o ExecOpts) (ExecResult, error)
	// Logs returns the last lines of the console log of a container. A
	// transport that cannot read logs answers with KindNotAvailable.
	Logs(ctx context.Context, container string, tail int) ([]string, error)
}

// DefaultExecTimeout is how long a command may run when no timeout is given.
const DefaultExecTimeout = 30 * time.Second

// ExecOpts are the settings of one exec.
type ExecOpts struct {
	Stdin     []byte
	Timeout   time.Duration // whole seconds, DefaultExecTimeout when zero
	MaxOutput int           // bytes kept of each of stdout and stderr, the device default when zero
	Cwd       string
	Env       map[string]string
}

func (o ExecOpts) seconds() int {
	t := o.Timeout
	if t <= 0 {
		t = DefaultExecTimeout
	}
	if s := int((t + time.Second - 1) / time.Second); s > 0 {
		return s
	}
	return 1
}

// ExecResult is how a command ended. ExitCode is nil when a signal ended it.
type ExecResult struct {
	ExitCode    *int   `json:"exit_code"`
	Signal      *int   `json:"signal"`
	TimedOut    bool   `json:"timed_out"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	TruncStdout bool   `json:"stdout_truncated"`
	TruncStderr bool   `json:"stderr_truncated"`
	DurationMS  int64  `json:"duration_ms,omitempty"`
}

// Code is the exit code pvr gives back for the command: 124 after a
// timeout, 128 plus the signal number when a signal ended it, else its own.
func (r ExecResult) Code() int {
	switch {
	case r.TimedOut:
		return 124
	case r.ExitCode != nil:
		return *r.ExitCode
	case r.Signal != nil:
		return 128 + *r.Signal
	}
	return 1
}

func notAvailable(what string) *Error {
	return errf(KindNotAvailable, what+" is not available over this transport; use a hub:<device> target")
}

// RequireActive fails with not_allowed or not_active unless a devmode
// revision runs.
func RequireActive(ctx context.Context, b Backend) (Status, error) {
	s, err := b.Status(ctx)
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

var _ Backend = (*Client)(nil)
