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
			return wificlient.Provision(context.Background(), wificlient.ProvisionArgs{
				Address:        c.Args().First(),
				SSID:           provSSID,
				Password:       provPassword,
				ConnectTimeout: provConnectWait,
				StateTimeout:   provStateWait,
			})
		},
	}
	return cmd
}
