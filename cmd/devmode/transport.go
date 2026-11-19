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
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Transport runs one shell command line on the device. A Hub transport can
// implement this later.
type Transport interface {
	// Run returns the exit code of the command. err is set only when the
	// command could not be run at all (the device is unreachable).
	Run(ctx context.Context, cmdline string, stdin io.Reader, stdout, stderr io.Writer) (int, error)
	Close() error
}

// SSHTransport uses the system ssh with a shared connection.
type SSHTransport struct {
	Target  Target
	Opts    []string // extra options, each passed as its own ssh argument
	Binary  string
	ctlDir  string
	started bool
}

func NewSSHTransport(t Target, opts []string) *SSHTransport {
	return &SSHTransport{Target: t, Opts: opts, Binary: "ssh"}
}

// baseArgs builds the ssh arguments up to the host.
func (s *SSHTransport) baseArgs() []string {
	a := []string{
		"-T",
		"-o", "ControlMaster=auto",
		"-o", "ControlPersist=60",
		"-o", "ControlPath=" + filepath.Join(s.ctlDir, "%C"),
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
		"-p", strconv.Itoa(s.Target.Port),
		"-l", s.Target.User,
	}
	a = append(a, s.Opts...)
	return append(a, s.Target.Host)
}

func (s *SSHTransport) Run(ctx context.Context, cmdline string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if s.ctlDir == "" {
		d, err := os.MkdirTemp("", "pvrdm")
		if err != nil {
			return 0, err
		}
		s.ctlDir = d
	}
	s.started = true
	args := append(s.baseArgs(), cmdline)
	return runLocal(ctx, s.Binary, args, stdin, stdout, stderr, true)
}

func (s *SSHTransport) Close() error {
	if s.ctlDir == "" {
		return nil
	}
	if s.started {
		args := append([]string{"-O", "exit"}, s.baseArgs()...)
		_ = exec.Command(s.Binary, args...).Run()
	}
	return os.RemoveAll(s.ctlDir)
}

// ExecTransport runs `<prefix> sh -c <cmd>`, e.g. prefix "docker exec -i box".
type ExecTransport struct{ Prefix []string }

func NewExecTransport(prefix string) (*ExecTransport, error) {
	f, err := SplitFields(prefix)
	if err != nil {
		return nil, err
	}
	if len(f) == 0 {
		return nil, errors.New("empty --exec command")
	}
	return &ExecTransport{Prefix: f}, nil
}

func (e *ExecTransport) Run(ctx context.Context, cmdline string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	args := append(append([]string{}, e.Prefix[1:]...), "sh", "-c", cmdline)
	return runLocal(ctx, e.Prefix[0], args, stdin, stdout, stderr, false)
}

func (e *ExecTransport) Close() error { return nil }

// runLocal runs a local program. For ssh, exit status 255 is a connection
// failure.
func runLocal(ctx context.Context, bin string, args []string, stdin io.Reader, stdout, stderr io.Writer, ssh bool) (int, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	var eb bytes.Buffer
	cmd.Stderr = io.MultiWriter(stderr, &eb)
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code := ee.ExitCode()
		if ssh && code == 255 {
			return 255, errors.New(strings.TrimSpace(firstLine(eb.String())))
		}
		return code, nil
	}
	return 0, err
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
