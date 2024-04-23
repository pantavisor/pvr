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
	"encoding/json"
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvr/libpvr"
)

func CommandTokenCreate() cli.Command {
	cmd := cli.Command{
		Name:      "create",
		Aliases:   []string{"cr"},
		ArgsUsage: "[user-meta]",
		Usage:     "pvr device token [user-meta]",
		Description: `
Creates a new device token

[user-meta]: key1=value1 key2=value2
		`,
		Action: func(c *cli.Context) error {
			session, err := libpvr.NewSession(c.App)
			if err != nil {
				return cli.NewExitError(err, 4)
			}

			baseURL := c.App.Metadata["PVR_BASEURL"].(string)
			userMeta := map[string]interface{}{}
			if c.NArg() > 0 {
				for _, v := range c.Args() {
					meta := strings.Split(v, "=")
					userMeta[meta[0]] = meta[1]
				}
			}

			deviceToken, err := session.TokenCreate(baseURL, userMeta)
			if err != nil {
				return cli.NewExitError("Error creation device token: "+err.Error(), 4)
			}

			table := tablewriter.NewWriter(os.Stdout)
			table.SetBorder(false)
			table.SetHeaderLine(false)
			table.SetColumnSeparator(" ")
			table.SetHeader([]string{"id", "nick", "token", "user-meta"})

			userMetaString, _ := json.Marshal(userMeta)

			table.Append([]string{
				deviceToken.ID,
				deviceToken.Nick,
				deviceToken.Token,
				string(userMetaString),
			})

			table.Render()

			return nil
		},
	}

	return cmd
}
