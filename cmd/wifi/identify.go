//go:build go1.24

//
// Copyright 2017-2026  Pantacor Ltd.
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
	"os"
	"time"

	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvwificonnect/modules/ble"
	wificlient "gitlab.com/pantacor/pvwificonnect/pkg/client"
)

// CommandIdentify : pvr wifi identify
func CommandIdentify() cli.Command {
	var identifyConnectWait time.Duration

	cmd := cli.Command{
		Name:    "identify",
		Aliases: []string{"id"},
		Usage:   "Ask an Improv BLE device to identify itself",
		Description: "Send an Improv Identify RPC so the target device signals its physical " +
			"location (e.g. blinking an LED or beeping).",
		ArgsUsage: "[address]",
		Flags: []cli.Flag{
			cli.DurationFlag{
				Name:        "connect-timeout",
				Usage:       "time to wait for BLE connect",
				Value:       20 * time.Second,
				Destination: &identifyConnectWait,
			},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() < 1 {
				return cli.NewExitError("identify needs BLE address argument. See --help", 2)
			}
			return runIdentify(c.Args().First(), identifyConnectWait)
		},
	}
	return cmd
}

func runIdentify(address string, connectTimeout time.Duration) error {
	ctx, cancel := wificlient.SignalContext(context.Background())
	defer cancel()

	client, err := wificlient.ConnectAndSetup(ctx, address, connectTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect() }()

	fmt.Fprintln(os.Stderr, "Sending Identify...")
	if err := client.SendRPC(ble.RPCPacket{Command: ble.RPCIdentify}); err != nil {
		return err
	}
	if _, err := wificlient.AwaitResult(ctx, client, ble.RPCIdentify, 5*time.Second); err != nil {
		return err
	}
	fmt.Println("Identify acknowledged.")
	return nil
}
