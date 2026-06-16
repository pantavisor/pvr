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
			return wificlient.ClaimInfo(context.Background(), wificlient.ClaimInfoArgs{
				Address:        c.Args().First(),
				ConnectTimeout: claimConnectWait,
				RetryTimeout:   claimRetryWait,
				RetryInterval:  claimRetryInterval,
				JSON:           claimJSON,
			})
		},
	}
	return cmd
}
