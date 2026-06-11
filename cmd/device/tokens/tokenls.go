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
	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvr/libpvr"
	"gitlab.com/pantacor/pvr/utils/pvjson"
)

func CommandTokenLS() cli.Command {
	cmd := cli.Command{
		Name:        "list",
		Aliases:     []string{"ls"},
		ArgsUsage:   "",
		Usage:       "pvr device ls",
		Description: "Creates a new device token",
		Action: func(c *cli.Context) error {
			session, err := libpvr.NewSession(c.App)
			if err != nil {
				return cli.NewExitError(err, 4)
			}

			baseURL := c.App.Metadata["PVR_BASEURL"].(string)
			deviceTokens, err := session.TokenLs(baseURL)
			if err != nil {
				return cli.NewExitError("Error getting device tokens list: "+err.Error(), 4)
			}

			table := tablewriter.NewWriter(os.Stdout)
			table.SetBorder(false)
			table.SetHeaderLine(false)
			table.SetColumnSeparator(" ")
			table.SetHeader([]string{"id", "nick", "disabled", "user-meta"})

			for _, v := range deviceTokens {
				userMeta, _ := pvjson.Marshal(v.DefaultUserMeta, pvjson.MarshalOptions{Compact: true})
				table.Append([]string{
					v.ID,
					v.Nick,
					strconv.FormatBool(v.Disabled),
					string(userMeta),
				})
			}

			table.Render()

			return nil
		},
	}

	return cmd
}
