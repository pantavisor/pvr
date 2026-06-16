//
// Copyright 2017-2025  Pantacor Ltd.
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

package wifi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// connectAndSetup connects to the given BLE address, prints status to stderr,
// reads the Capabilities characteristic, and drains any replayed Current State
// notification. On success the caller owns the returned Client and must call
// Disconnect when done.
func connectAndSetup(ctx context.Context, address string, connectTimeout time.Duration) (*Client, error) {
	client, err := NewClient()
	if err != nil {
		return nil, err
	}

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	fmt.Fprintf(os.Stderr, "Connecting to %s...\n", address)
	if err := client.Connect(connectCtx, address); err != nil {
		return nil, err
	}
	fmt.Fprintln(os.Stderr, "Connected.")

	if caps, err := client.Capabilities(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read capabilities: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "Device capabilities: %s\n", strings.Join(capList(caps), ", "))
	}

	// Some stacks replay a stale state value on subscribe. Drain it.
	select {
	case s := <-client.StateEvents():
		fmt.Fprintf(os.Stderr, "Initial state: %s\n", stateName(s, true))
	case <-time.After(300 * time.Millisecond):
	}

	return client, nil
}

// signalContext returns a context cancelled by SIGINT/SIGTERM.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// awaitResult waits for an RPC Result matching wantCmd (or a non-zero error
// from the device) up to timeout. Used for single-response RPCs like
// Identify, Device Info, and Get/Set Hostname.
func awaitResult(ctx context.Context, client *Client, wantCmd byte, timeout time.Duration) (RPCPacket, error) {
	deadline := time.After(timeout)
	for {
		select {
		case <-ctx.Done():
			return RPCPacket{}, ctx.Err()
		case <-deadline:
			return RPCPacket{}, errors.New("timed out waiting for device response")
		case code := <-client.ErrorEvents():
			if code != ErrorNone {
				return RPCPacket{}, fmt.Errorf("device reported error 0x%02x (%s)", code, errorName(code))
			}
		case pkt := <-client.ResultEvents():
			if pkt.Command == wantCmd {
				return pkt, nil
			}
		}
	}
}

// errorName decodes Improv error bytes into human-readable strings.
func errorName(code byte) string {
	switch code {
	case ErrorNone:
		return "none"
	case ErrorInvalidRPC:
		return "invalid RPC packet"
	case ErrorUnknownRPC:
		return "unknown RPC command"
	case ErrorUnableConnect:
		return "unable to connect to Wi-Fi"
	case ErrorNotAuthorized:
		return "not authorized"
	case ErrorBadHostname:
		return "bad hostname"
	case ErrorUnknown:
		return "unknown error"
	}
	return fmt.Sprintf("0x%02x", code)
}
