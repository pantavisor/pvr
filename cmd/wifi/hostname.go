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
	"os"
	"time"

	"github.com/urfave/cli"
)

// CommandHostname : pvr wifi hostname
func CommandHostname() cli.Command {
	var hostnameConnectWait time.Duration

	cmd := cli.Command{
		Name:    "hostname",
		Aliases: []string{"hn"},
		Usage:   "Get or set the hostname of an Improv BLE device",
		Description: "Without a name, prints the current hostname reported by the device. " +
			"With a name, asks the device to set its hostname to that value and prints the " +
			"value the device reports back.",
		ArgsUsage: "[address] [name]",
		Flags: []cli.Flag{
			cli.DurationFlag{
				Name:        "connect-timeout",
				Usage:       "time to wait for BLE connect",
				Value:       20 * time.Second,
				Destination: &hostnameConnectWait,
			},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() < 1 {
				return cli.NewExitError("hostname needs BLE address argument. See --help", 2)
			}
			name := ""
			if c.NArg() >= 2 {
				name = c.Args().Get(1)
			}
			return runHostname(c.Args().First(), name, hostnameConnectWait)
		},
	}
	return cmd
}

func runHostname(address, name string, connectTimeout time.Duration) error {
	ctx, cancel := signalContext(context.Background())
	defer cancel()

	client, err := connectAndSetup(ctx, address, connectTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect() }()

	var data []byte
	if name != "" {
		fmt.Fprintf(os.Stderr, "Setting hostname to %q...\n", name)
		data = EncodeStringList([]string{name})
	}
	if err := client.SendRPC(RPCPacket{Command: RPCHostname, Data: data}); err != nil {
		return err
	}
	pkt, err := awaitResult(ctx, client, RPCHostname, 5*time.Second)
	if err != nil {
		return err
	}
	items, err := DecodeStringList(pkt.Data)
	if err != nil {
		return fmt.Errorf("decode hostname: %w", err)
	}
	if len(items) == 0 {
		fmt.Println("(no hostname reported)")
		return nil
	}
	fmt.Println(items[0])
	return nil
}
