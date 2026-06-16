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

// CommandWifiScan : pvr wifi wifi-scan
func CommandWifiScan() cli.Command {
	var wifiScanConnectWait time.Duration

	cmd := cli.Command{
		Name:    "wifi-scan",
		Aliases: []string{"ws"},
		Usage:   "Ask an Improv BLE device to scan for Wi-Fi networks",
		Description: "Send an Improv Scan RPC and print the networks the device reports. " +
			"The Improv protocol terminates the stream with an empty RPC Result.",
		ArgsUsage: "[address]",
		Flags: []cli.Flag{
			cli.DurationFlag{
				Name:        "connect-timeout",
				Usage:       "time to wait for BLE connect",
				Value:       20 * time.Second,
				Destination: &wifiScanConnectWait,
			},
		},
		Action: func(c *cli.Context) error {
			if c.NArg() < 1 {
				return cli.NewExitError("wifi-scan needs BLE address argument. See --help", 2)
			}
			return wificlient.WifiScan(context.Background(), wificlient.WifiScanArgs{
				Address:        c.Args().First(),
				ConnectTimeout: wifiScanConnectWait,
			})
		},
	}
	return cmd
}
