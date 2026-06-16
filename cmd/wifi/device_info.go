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
	"fmt"
	"time"

	"github.com/urfave/cli"
)

// CommandDeviceInfo : pvr wifi device-info
func CommandDeviceInfo() cli.Command {
	var deviceInfoConnectWait time.Duration

	cmd := cli.Command{
		Name:    "device-info",
		Aliases: []string{"di"},
		Usage:   "Read firmware and device information from an Improv BLE device",
		Description: "Send an Improv Device Info RPC and print the firmware name, firmware " +
			"version, hardware variant, and device name reported by the device.",
		ArgsUsage: "[address]",
		Flags: []cli.Flag{
			cli.DurationFlag{
				Name:        "connect-timeout",
				Usage:       "time to wait for BLE connect",
				Value:       20 * time.Second,
				Destination: &deviceInfoConnectWait,
			},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() < 1 {
				return cli.NewExitError("device-info needs BLE address argument. See --help", 2)
			}
			return runDeviceInfo(c.Args().First(), deviceInfoConnectWait)
		},
	}
	return cmd
}

func runDeviceInfo(address string, connectTimeout time.Duration) error {
	ctx, cancel := signalContext(context.Background())
	defer cancel()

	client, err := connectAndSetup(ctx, address, connectTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect() }()

	if err := client.SendRPC(RPCPacket{Command: RPCDeviceInfo}); err != nil {
		return err
	}
	pkt, err := awaitResult(ctx, client, RPCDeviceInfo, 5*time.Second)
	if err != nil {
		return err
	}
	items, err := DecodeStringList(pkt.Data)
	if err != nil {
		return fmt.Errorf("decode device-info: %w", err)
	}
	labels := []string{"firmware", "version", "hardware", "device-name"}
	for i, label := range labels {
		val := ""
		if i < len(items) {
			val = items[i]
		}
		fmt.Printf("%-12s %s\n", label+":", val)
	}
	return nil
}
