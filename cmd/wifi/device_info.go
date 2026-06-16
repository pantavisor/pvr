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
			return wificlient.DeviceInfo(context.Background(), wificlient.DeviceInfoArgs{
				Address:        c.Args().First(),
				ConnectTimeout: deviceInfoConnectWait,
			})
		},
	}
	return cmd
}
