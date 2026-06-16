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
	"strings"
	"time"

	"github.com/urfave/cli"
	"golang.org/x/term"
)

// CommandProvision : pvr wifi provision
func CommandProvision() cli.Command {
	var (
		provSSID        string
		provPassword    string
		provConnectWait time.Duration
		provStateWait   time.Duration
	)

	cmd := cli.Command{
		Name:    "provision",
		Aliases: []string{"pr"},
		Usage:   "Provision an Improv BLE device with Wi-Fi credentials",
		Description: "Connect to an Improv device over BLE and send Wi-Fi credentials using " +
			"the Improv Wi-Fi RPC protocol.",
		ArgsUsage: "[address]",
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:        "ssid",
				Usage:       "Wi-Fi SSID to configure",
				Destination: &provSSID,
			},
			cli.StringFlag{
				Name:        "password",
				Usage:       "Wi-Fi password (prefer interactive prompt; this flag may leak credentials to shell history)",
				Destination: &provPassword,
			},
			cli.DurationFlag{
				Name:        "connect-timeout",
				Usage:       "time to wait for BLE connect",
				Value:       20 * time.Second,
				Destination: &provConnectWait,
			},
			cli.DurationFlag{
				Name:        "state-timeout",
				Usage:       "time to wait for Wi-Fi connection to complete",
				Value:       60 * time.Second,
				Destination: &provStateWait,
			},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() < 1 {
				return cli.NewExitError("provision needs BLE address argument. See --help", 2)
			}
			if provSSID == "" {
				return cli.NewExitError("--ssid is required", 2)
			}
			return runProvision(c.Args().First(), provSSID, provPassword, provConnectWait, provStateWait)
		},
	}
	return cmd
}

func runProvision(address, ssid, password string, connectTimeout, stateTimeout time.Duration) error {
	ctx, cancel := signalContext(context.Background())
	defer cancel()

	if password == "" {
		p, err := readPasswordPrompt(fmt.Sprintf("Password for %q: ", ssid))
		if err != nil {
			return err
		}
		password = p
	}

	client, err := connectAndSetup(ctx, address, connectTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect() }()

	fmt.Fprintf(os.Stderr, "Sending Wi-Fi credentials for %q...\n", ssid)
	if err := client.SendRPC(RPCPacket{
		Command: RPCSendWiFi,
		Data:    EncodeSendWiFi(ssid, password),
	}); err != nil {
		return err
	}

	return watchProvisioning(ctx, client, stateTimeout)
}

// watchProvisioning listens for state transitions after sending Wi-Fi creds.
func watchProvisioning(ctx context.Context, client *Client, stateTimeout time.Duration) error {
	deadline := time.NewTimer(stateTimeout)
	defer deadline.Stop()

	var lastErr byte
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("timed out waiting for device to finish provisioning")
		case code := <-client.ErrorEvents():
			if code != ErrorNone {
				lastErr = code
				fmt.Fprintf(os.Stderr, "Device error: 0x%02x (%s)\n", code, errorName(code))
			}
		case s := <-client.StateEvents():
			fmt.Fprintf(os.Stderr, "State: %s\n", stateName(s, true))
			switch s {
			case StateProvisioned:
				return printProvisionedResult(ctx, client)
			case StateAuthorized:
				if lastErr != ErrorNone {
					return fmt.Errorf("provisioning failed: %s", errorName(lastErr))
				}
			}
		case pkt := <-client.ResultEvents():
			if pkt.Command == RPCSendWiFi {
				return printRedirect(pkt)
			}
		}
	}
}

func printProvisionedResult(ctx context.Context, client *Client) error {
	// The redirect URL is delivered via RPC Result; wait briefly for it.
	wait := time.After(3 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-wait:
			fmt.Println("Device reports Provisioned. No redirect URL received.")
			return nil
		case pkt := <-client.ResultEvents():
			if pkt.Command == RPCSendWiFi {
				return printRedirect(pkt)
			}
		}
	}
}

func printRedirect(pkt RPCPacket) error {
	items, err := DecodeStringList(pkt.Data)
	if err != nil {
		return err
	}
	url := ""
	if len(items) > 0 {
		url = items[0]
	}
	if url != "" {
		fmt.Printf("Provisioned. Device captive portal: %s\n", url)
	} else {
		fmt.Println("Provisioned.")
	}
	return nil
}

func readPasswordPrompt(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		var line string
		if _, err := fmt.Fscanln(os.Stdin, &line); err != nil {
			return "", err
		}
		return line, nil
	}
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	// term.ReadPassword already strips the trailing newline; don't TrimSpace
	// because passwords may legitimately contain leading/trailing whitespace.
	return strings.TrimRight(string(b), "\r"), nil
}
