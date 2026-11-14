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

package tokens

import (
	"os"
	"strconv"

	"github.com/olekukonko/tablewriter"
	"github.com/pantavisor/pvr/libpvr"
	"github.com/urfave/cli"
)

func CommandTokenDisable() cli.Command {
	cmd := cli.Command{
		Name:        "disable",
		Aliases:     []string{"d"},
		ArgsUsage:   "[tokenid]",
		Usage:       "pvr device disable [tokenid]",
		Description: "disable a token using the token id",
		Action: func(c *cli.Context) error {
			session, err := libpvr.NewSession(c.App)
			if err != nil {
				return cli.NewExitError(err, 4)
			}

			baseURL := c.App.Metadata["PVR_BASEURL"].(string)
			ids := []string{}
			if c.NArg() > 0 {
				for _, v := range c.Args() {
					ids = append(ids, v)
				}
			}

			devicesTokens := []*libpvr.PantahubDevicesJoinToken{}
			for _, id := range ids {
				v, err := session.TokenDisable(baseURL, id)
				if err != nil {
					return cli.NewExitError("Error creation device token: "+err.Error(), 4)
				}

				devicesTokens = append(devicesTokens, v)
			}

			table := tablewriter.NewWriter(os.Stdout)
			table.SetBorder(false)
			table.SetHeaderLine(false)
			table.SetColumnSeparator(" ")
			table.SetHeader([]string{"id", "disabled"})

			for _, v := range devicesTokens {
				table.Append([]string{
					v.ID,
					strconv.FormatBool(v.Disabled),
				})
			}

			table.Render()

			return nil
		},
	}

	return cmd
}
