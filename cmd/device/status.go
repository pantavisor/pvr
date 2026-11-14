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

package device

import (
	"errors"
	"log"
	"strings"

	"github.com/go-resty/resty"
	"github.com/pantavisor/pvr/libpvr"
	"github.com/pantavisor/pvr/utils/pvjson"
	"github.com/urfave/cli"
)

func CommandStatus() cli.Command {
	return cli.Command{
		Name:        "status",
		Aliases:     []string{"st"},
		ArgsUsage:   "<NICK|ID>",
		Usage:       "pvr device status <NICK|ID> [--rev=N]",
		Description: "Show status of a device. Without --rev shows the step for the current running revision (from device-meta pantavisor.revision). With --rev shows the step for that revision.",
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
				session.SuggestDeviceNicks(splits[0], searchTerm, baseURL)
			}
		},
		Action: func(c *cli.Context) error {
			session, err := libpvr.NewSession(c.App)
			if err != nil {
				return cli.NewExitError(err, 4)
			}
			baseURL := c.App.Metadata["PVR_BASEURL"].(string)

			if c.NArg() < 1 {
				return cli.NewExitError(errors.New("Device ID or Nick is required. See --help"), 2)
			}

			info, err := resolveDevice(session, baseURL, c.Args()[0])
			if err != nil {
				return cli.NewExitError(err.Error(), 4)
			}

			rev := c.String("rev")
			if rev == "" && info.Rev != "" {
				rev = info.Rev
			}
			if rev == "" {
				rev, err = getRunningRev(session, info.BaseURL, info.DeviceID)
				if err != nil {
					return cli.NewExitError(err.Error(), 4)
				}
			}

			stepURL := info.BaseURL + "/trails/" + info.DeviceID + "/steps/" + rev
			response, err := session.DoAuthCall(false, libpvr.NoneCallMethod, func(req *resty.Request) error {
				req.Method = resty.MethodGet
				req.URL = stepURL
				return nil
			})
			if err != nil {
				return cli.NewExitError("Error getting step status: "+err.Error(), 4)
			}
			if response.StatusCode() != 200 {
				return cli.NewExitError("Error getting step status: "+string(response.Body()), 4)
			}

			var step map[string]interface{}
			err = pvjson.Unmarshal(response.Body(), &step)
			if err != nil {
				return cli.NewExitError("Error parsing step response: "+err.Error(), 4)
			}

			delete(step, "state")

			filtered, err := pvjson.Marshal(step, pvjson.MarshalOptions{Compact: true})
			if err != nil {
				return cli.NewExitError("Error marshaling response: "+err.Error(), 4)
			}

			libpvr.LogPrettyJSON(filtered)
			return nil
		},
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:   "rev,r",
				Usage:  "Show status for a specific revision, e.g. --rev=4. Default: current running revision.",
				EnvVar: "PVR_STATUS_REV",
			},
		},
	}
}
