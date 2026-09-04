//
// Copyright 2026 Pantacor Ltd.
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
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvr/libpvr"
)

// CommandDeviceAcceptOwnership : pvr device accept-ownership
func CommandDeviceAcceptOwnership() cli.Command {
	cmd := cli.Command{
		Name:      "accept-ownership",
		Aliases:   []string{"accept"},
		ArgsUsage: "<NICK|ID> | <USER_NICK>/<NICK|ID>",
		Usage:     "pvr device accept-ownership <NICK|ID> | <USER_NICK>/<NICK|ID>",
		Description: `Accept ownership of a device that registered through a device token
   configured with "manual" owner verification. Until the owner accepts it,
   such a device only receives read-only credentials from hub.

   Run this logged in as the device owner. The same action is available on
   the device page in the Hub UI.`,
		BashComplete: func(c *cli.Context) {
			if c.GlobalString("baseurl") != "" {
				c.App.Metadata["PVR_BASEURL"] = c.GlobalString("baseurl")
			}
			session, err := libpvr.NewSession(c.App)
			if err != nil {
				log.Fatal(err.Error())
				return
			}
			if c.NArg() == 0 {
				return
			}
			searchTerm := c.Args()[c.NArg()-1]
			baseURL := c.App.Metadata["PVR_BASEURL"].(string)

			splits := strings.Split(searchTerm, "/")
			if len(splits) == 1 {
				session.SuggestDeviceNicks("", searchTerm, baseURL)
			} else {
				session.SuggestDeviceNicks(splits[1], searchTerm, baseURL)
			}
		},
		Action: func(c *cli.Context) error {
			session, err := libpvr.NewSession(c.App)
			if err != nil {
				return cli.NewExitError(err, 4)
			}
			baseURL := c.App.Metadata["PVR_BASEURL"].(string)

			if c.NArg() != 1 {
				return cli.NewExitError(errors.New("Device ID or Nick is required. See --help"), 2)
			}

			splits := strings.Split(c.Args()[0], "/")
			deviceNick := splits[0]
			ownerNick := ""
			if len(splits) > 1 {
				ownerNick = splits[0]
				deviceNick = splits[1]
			}

			status, err := session.AcceptDeviceOwnership(baseURL, deviceNick, ownerNick)
			if err != nil {
				return cli.NewExitError("Error accepting device ownership: "+err.Error(), 3)
			}

			fmt.Fprintf(os.Stdout, "Device %s ownership verification: mode=%s status=%s\n",
				c.Args()[0], status.Mode, status.Status)
			return nil
		},
	}

	return cmd
}
