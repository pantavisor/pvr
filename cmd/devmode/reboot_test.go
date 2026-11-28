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

package devmode

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// rebootDev is a device that answers status from a script, one answer per call.
type rebootDev struct {
	Backend
	status []interface{} // Status or error
	calls  int
	enters int
	reboot bool
}

func (d *rebootDev) Status(context.Context) (Status, error) {
	i := d.calls
	if i >= len(d.status) {
		i = len(d.status) - 1
	}
	d.calls++
	switch v := d.status[i].(type) {
	case error:
		return Status{}, v
	case Status:
		return v, nil
	}
	return Status{}, nil
}
func (d *rebootDev) Enter(context.Context, string, bool) (EnterResult, error) {
	d.enters++
	return EnterResult{Revision: "devmode/r/1", Base: "1", Reboot: d.reboot}, nil
}
func (d *rebootDev) Leave(context.Context, string) error { return nil }

func rebootEnv(d *rebootDev, wait time.Duration) *Env {
	return &Env{C: d, Out: io.Discard, Err: io.Discard, Poll: time.Millisecond, WaitTimeout: wait}
}

func TestEnterWaitsUntilDevmodeRuns(t *testing.T) {
	up := Status{Enabled: true, Active: true, Ready: true}
	d := &rebootDev{reboot: true, status: []interface{}{
		Status{Enabled: true},
		errors.New("exit status 255"), // rebooting
		Status{Enabled: true, Active: true},
		up,
	}}
	r, waited, err := rebootEnv(d, 5*time.Second).EnterWait(context.Background(), "", false)
	if err != nil || !waited || !r.Running {
		t.Fatalf("r=%+v waited=%v err=%v", r, waited, err)
	}
	if d.calls != 4 {
		t.Errorf("status polled %d times, want 4", d.calls)
	}
}

func TestEnterWaitTimesOut(t *testing.T) {
	d := &rebootDev{reboot: true, status: []interface{}{errors.New("connection refused")}}
	_, _, err := rebootEnv(d, 30*time.Millisecond).EnterWait(context.Background(), "", false)
	e, ok := err.(*Error)
	if !ok || e.Kind != KindWaitTimeout || !strings.Contains(e.Msg, "connection refused") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnterNoWaitReturnsAtOnce(t *testing.T) {
	d := &rebootDev{reboot: true, status: []interface{}{Status{}}}
	e := rebootEnv(d, time.Second)
	e.NoWait = true
	r, waited, err := e.EnterWait(context.Background(), "", false)
	if err != nil || waited || r.Running || d.calls != 0 {
		t.Fatalf("r=%+v waited=%v calls=%d err=%v", r, waited, d.calls, err)
	}
}

func TestEnterWithoutRebootDoesNotWait(t *testing.T) {
	d := &rebootDev{status: []interface{}{Status{}}}
	if _, waited, err := rebootEnv(d, time.Second).EnterWait(context.Background(), "", true); err != nil || waited || d.calls != 0 {
		t.Fatalf("waited=%v calls=%d err=%v", waited, d.calls, err)
	}
}

func TestLeaveWaitsUntilDevmodeStops(t *testing.T) {
	d := &rebootDev{status: []interface{}{
		Status{Enabled: true, Active: true, Ready: true},
		errors.New("exit status 255"),
		Status{Enabled: true},
	}}
	waited, err := rebootEnv(d, 5*time.Second).LeaveWait(context.Background(), "1")
	if err != nil || !waited || d.calls != 3 {
		t.Fatalf("waited=%v calls=%d err=%v", waited, d.calls, err)
	}
}
