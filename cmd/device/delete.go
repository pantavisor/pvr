//
// Copyright 2017-2024  Pantacor Ltd.
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

package device

import (
	"os"

	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvr/libpvr"
)

func CommandDeviceDelete() cli.Command {
	return cli.Command{
		Name:        "delete",
		Aliases:     []string{"del"},
		ArgsUsage:   "<device-endpoint> - Endpoint for device to delete,\n                    e.g. https://api.pantahub.com/devices/xxxxxxxxxx",
		Usage:       "Delete a device",
		Description: "Delete a device from hub. Please, use with caution",
		Flags: []cli.Flag{
			cli.BoolFlag{Name: "y, yes", Usage: "Confirm deletion"},
		},
		Action: func(c *cli.Context) error {
			wd, err := os.Getwd()
			if err != nil {
				return cli.NewExitError(err, 1)
			}

			session, err := libpvr.NewSession(c.App)

			if err != nil {
				return cli.NewExitError(err, 4)
			}

			pvr, err := libpvr.NewPvr(session, wd)
			if err != nil {
				return cli.NewExitError(err, 2)
			}

			var deviceEndpoint string

			if c.NArg() != 1 {
				return cli.NewExitError("You must specify a device to delete. See --help", 3)
			} else {
				deviceEndpoint = c.Args()[0]
			}

			// if no scheme, we assume its device-id/nick and resolve
			// it against the configured base URL
			deviceEndpoint, err = libpvr.ResolveDeviceEndpoint(c.App.Metadata["PVR_BASEURL"].(string), deviceEndpoint)
			if err != nil {
				return cli.NewExitError("Error resolving device endpoint: "+err.Error(), 3)
			}

			err = pvr.DoDelete(deviceEndpoint, c.Bool("yes"))

			if err != nil {
				return cli.NewExitError("Error deleting device: "+err.Error(), 4)
			}

			return nil
		},
	}
}
