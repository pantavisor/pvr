//go:build go1.22

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

// CommandDeviceScan : pvr wifi device-scan
func CommandDeviceScan() cli.Command {
	var (
		scanTimeout time.Duration
		scanJSON    bool
	)

	cmd := cli.Command{
		Name:        "device-scan",
		Aliases:     []string{"ds"},
		Usage:       "Scan for Improv BLE devices",
		Description: "Scan for nearby BLE devices advertising the Improv Wi-Fi service.",
		Flags: []cli.Flag{
			cli.DurationFlag{
				Name:        "timeout",
				Usage:       "stop scanning after this duration (0 = forever)",
				Value:       15 * time.Second,
				Destination: &scanTimeout,
			},
			cli.BoolFlag{
				Name:        "json",
				Usage:       "emit one JSON object per device",
				Destination: &scanJSON,
			},
		},
		Action: func(c *cli.Context) error {
			return wificlient.Scan(context.Background(), wificlient.ScanArgs{
				Timeout: scanTimeout,
				JSON:    scanJSON,
			})
		},
	}
	return cmd
}
