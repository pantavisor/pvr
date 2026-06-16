//go:build go1.22 && linux

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
	"time"

	"github.com/urfave/cli"
	wificlient "gitlab.com/pantacor/pvwificonnect/modules/client"
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
			return wificlient.Hostname(context.Background(), wificlient.HostnameArgs{
				Address:        c.Args().First(),
				Name:           name,
				ConnectTimeout: hostnameConnectWait,
			})
		},
	}
	return cmd
}
