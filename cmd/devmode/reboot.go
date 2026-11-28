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
	"fmt"
	"time"
)

// RebootWaitDefault is how long enter and leave wait for the device to come
// back, since a real board reboots slowly.
const RebootWaitDefault = 180 * time.Second

// waitDevmode polls the status until devmode runs (active) or stops (not
// active). The device is unreachable while it reboots, so failures to ask are
// not final until the timeout.
func (e *Env) waitDevmode(ctx context.Context, active bool) error {
	deadline := time.Now().Add(e.timeout())
	want := "devmode to stop"
	if active {
		want = "devmode to run"
	}
	e.progress("waiting for the device to reboot and %s (up to %s)", want, e.timeout())
	var lastErr error
	for {
		st, err := e.C.Status(ctx)
		lastErr = err
		if err == nil {
			if active && st.Active && st.Ready {
				return nil
			}
			if !active && !st.Active {
				return nil
			}
		}
		if time.Now().After(deadline) {
			msg := fmt.Sprintf("the device did not report %s within %s; check it with pvr devmode status", want, e.timeout())
			if lastErr != nil {
				msg += " (last error: " + lastErr.Error() + ")"
			}
			return errf(KindWaitTimeout, msg)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(e.poll()):
		}
	}
}

// EnterWait enters devmode and, unless NoWait, waits for the reboot.
func (e *Env) EnterWait(ctx context.Context, base string, resume bool) (r EnterResult, waited bool, err error) {
	err = e.retryBusy(ctx, func() error { var rerr error; r, rerr = e.C.Enter(ctx, base, resume); return rerr })
	if err != nil || !r.Reboot || e.NoWait {
		return r, false, err
	}
	if err := e.waitDevmode(ctx, true); err != nil {
		return r, false, err
	}
	r.Running = true
	return r, true, nil
}

// LeaveWait leaves devmode and, unless NoWait, waits until it has stopped.
func (e *Env) LeaveWait(ctx context.Context, rev string) (bool, error) {
	if err := e.retryBusy(ctx, func() error { return e.C.Leave(ctx, rev) }); err != nil {
		return false, err
	}
	if e.NoWait {
		return false, nil
	}
	return true, e.waitDevmode(ctx, false)
}
