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
	"encoding/json"
	"errors"
	"strings"
)

var errUnterminated = errors.New("unterminated quote in command prefix")

// Kind classifies a failure; every kind has its own exit code.
type Kind string

const (
	KindGeneric      Kind = "error"
	KindUsage        Kind = "usage"
	KindUnreachable  Kind = "unreachable"
	KindNotAllowed   Kind = "not_allowed"
	KindNotActive    Kind = "not_active"
	KindRebootNeeded Kind = "reboot_needed"
	KindApplyFailed  Kind = "apply_failed"
	KindWaitTimeout  Kind = "wait_timeout"
	KindNotAvailable Kind = "not_available"
)

// ExitCode is the process exit code for a failure kind.
func (k Kind) ExitCode() int {
	switch k {
	case KindUsage:
		return 2
	case KindUnreachable:
		return 10
	case KindNotAllowed:
		return 11
	case KindNotActive:
		return 12
	case KindRebootNeeded:
		return 13
	case KindApplyFailed:
		return 14
	case KindWaitTimeout:
		return 15
	case KindNotAvailable:
		return 16
	}
	return 1
}

// Error is a failure with a kind.
type Error struct {
	Kind Kind
	Msg  string
	// Code is the error code of a Hub tool, when the Hub gave one.
	Code string
}

func (e *Error) Error() string { return e.Msg }

func errf(k Kind, msg string) *Error { return &Error{Kind: k, Msg: msg} }

// classify turns the stderr of a failed pvcontrol call into an error.
func classify(stderr string) *Error {
	msg := strings.TrimSpace(stderr)
	var j struct{ Error string }
	if json.Unmarshal([]byte(msg), &j) == nil && j.Error != "" {
		msg = j.Error
	}
	switch {
	case strings.Contains(msg, "Devmode is disabled"), strings.Contains(msg, "not sent from a mgmt platform"):
		return errf(KindNotAllowed, msg)
	case strings.Contains(msg, "Not running a devmode revision"):
		return errf(KindNotActive, msg)
	case strings.Contains(msg, "reboot needed"):
		return errf(KindRebootNeeded, msg)
	case strings.Contains(msg, "No such container"), strings.Contains(msg, "Cannot connect to the Docker daemon"):
		return errf(KindUnreachable, msg)
	}
	if msg == "" {
		msg = "command failed on the device"
	}
	return errf(KindGeneric, msg)
}
