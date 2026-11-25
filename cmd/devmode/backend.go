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
