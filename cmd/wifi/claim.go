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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/urfave/cli"
)

// CommandClaimInfo : pvr wifi claim-info
func CommandClaimInfo() cli.Command {
	var (
		claimConnectWait   time.Duration
		claimRetryWait     time.Duration
		claimRetryInterval time.Duration
		claimJSON          bool
	)

	cmd := cli.Command{
		Name:    "claim-info",
		Aliases: []string{"ci"},
		Usage:   "Read Pantavisor claim material from a pvwificonnect BLE device",
		Description: "Send the Pantavisor Claim RPC (pvwificonnect extension) and print the " +
			"device-id and claim challenge needed to claim the device on Pantacor Hub. " +
			"The device only has claim material after its first rendezvous with the hub; " +
			"until then it reports an error and this command retries up to --retry-timeout.",
		ArgsUsage: "[address]",
		Flags: []cli.Flag{
			cli.DurationFlag{
				Name:        "connect-timeout",
				Usage:       "time to wait for BLE connect",
				Value:       20 * time.Second,
				Destination: &claimConnectWait,
			},
			cli.DurationFlag{
				Name:        "retry-timeout",
				Usage:       "total time to keep retrying while the device has no claim material yet",
				Value:       60 * time.Second,
				Destination: &claimRetryWait,
			},
			cli.DurationFlag{
				Name:        "retry-interval",
				Usage:       "delay between retries",
				Value:       5 * time.Second,
				Destination: &claimRetryInterval,
			},
			cli.BoolFlag{
				Name:        "json",
				Usage:       "print result as JSON",
				Destination: &claimJSON,
			},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() < 1 {
				return cli.NewExitError("claim-info needs BLE address argument. See --help", 2)
			}
			return runClaimInfo(c.Args().First(), claimConnectWait, claimRetryWait, claimRetryInterval, claimJSON)
		},
	}
	return cmd
}

func runClaimInfo(address string, connectTimeout, retryTimeout, retryInterval time.Duration, jsonOutput bool) error {
	ctx, cancel := signalContext(context.Background())
	defer cancel()

	client, err := connectAndSetup(ctx, address, connectTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect() }()

	if caps, err := client.Capabilities(); err == nil && caps&CapPantavisorClaim == 0 {
		return errors.New("device does not advertise the Pantavisor Claim capability")
	}

	items, err := fetchClaimInfo(ctx, client, retryTimeout, retryInterval)
	if err != nil {
		return err
	}
	return printClaimInfo(items, jsonOutput)
}

// fetchClaimInfo sends the Pantavisor Claim RPC and waits for its result,
// retrying while the device reports that the claim material is not available
// yet (ErrorUnknown — the device has not reached the hub since boot).
func fetchClaimInfo(ctx context.Context, client *Client, retryTimeout, retryInterval time.Duration) ([]string, error) {
	deadline := time.NewTimer(retryTimeout)
	defer deadline.Stop()

	for attempt := 1; ; attempt++ {
		if err := client.SendRPC(RPCPacket{Command: RPCPantavisorClaim}); err != nil {
			return nil, err
		}
		pkt, err := awaitResult(ctx, client, RPCPantavisorClaim, 5*time.Second)
		if err == nil {
			items, derr := DecodeStringList(pkt.Data)
			if derr != nil {
				return nil, fmt.Errorf("decode claim result: %w", derr)
			}
			if len(items) == 0 {
				return nil, errors.New("device returned an empty claim result")
			}
			return items, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		fmt.Fprintf(os.Stderr, "Claim material not ready (attempt %d): %v\n", attempt, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, errors.New("timed out waiting for claim material; has the device reached the hub yet?")
		case <-time.After(retryInterval):
		}
	}
}

func printClaimInfo(items []string, jsonOutput bool) error {
	deviceID := items[0]
	challenge := ""
	if len(items) > 1 {
		challenge = items[1]
	}
	claimed := challenge == ""

	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(struct {
			DeviceID  string `json:"device_id"`
			Challenge string `json:"challenge,omitempty"`
			Claimed   bool   `json:"claimed"`
		}{deviceID, challenge, claimed})
	}

	fmt.Printf("%-12s %s\n", "device-id:", deviceID)
	if claimed {
		fmt.Printf("%-12s %s\n", "status:", "already claimed")
	} else {
		fmt.Printf("%-12s %s\n", "challenge:", challenge)
		fmt.Printf("%-12s %s\n", "status:", "unclaimed")
	}
	return nil
}
