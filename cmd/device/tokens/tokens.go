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

import "github.com/urfave/cli"

// CommandDevice : pvr device tokens command
func CommandDeviceTokens() cli.Command {
	cmd := cli.Command{
		Name:    "tokens",
		Aliases: []string{"tokens"},
		Subcommands: []cli.Command{
			CommandTokenCreate(),
			CommandTokenLS(),
			CommandTokenDisable(),
		},
		Usage:       "pvr device tokens <ls|create|disable>",
		Description: "\n1.Show Owned tokens\n 2.Create a new device token\n 3.Disable a given device token",
	}

	return cmd
}
