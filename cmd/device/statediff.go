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
	"fmt"
	"log"
	"strings"

	"github.com/highercomve/jsondiff"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvr/libpvr"
	"gitlab.com/pantacor/pvr/utils/pvjson"
)

func CommandStateDiff() cli.Command {
	return cli.Command{
		Name:        "statediff",
		Aliases:     []string{"sd"},
		ArgsUsage:   "<NICK|ID> <REV1> <REV2>",
		Usage:       "pvr device statediff <NICK|ID> <REV1> <REV2>",
		Description: "Diff the state of two step revisions of a device trail.",
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

			if c.NArg() < 3 {
				return cli.NewExitError(errors.New("Usage: pvr device statediff <NICK|ID> <REV1> <REV2>. See --help"), 2)
			}

			info, err := resolveDevice(session, baseURL, c.Args()[0])
			if err != nil {
				return cli.NewExitError(err.Error(), 4)
			}

			rev1 := c.Args()[1]
			rev2 := c.Args()[2]
			format := c.String("format")

			stateA, err := getStepState(session, info.BaseURL, info.DeviceID, rev1)
			if err != nil {
				return cli.NewExitError(err.Error(), 4)
			}
			stateB, err := getStepState(session, info.BaseURL, info.DeviceID, rev2)
			if err != nil {
				return cli.NewExitError(err.Error(), 4)
			}

			jsonA, _ := pvjson.Marshal(stateA)
			jsonB, _ := pvjson.Marshal(stateB)

			switch format {
			case "jsondiff":
				patch, err := jsondiff.CompareJSON(jsonA, jsonB)
				if err != nil {
					return cli.NewExitError("Error computing jsondiff: "+err.Error(), 4)
				}
				if len(patch) == 0 {
					fmt.Println("[]")
				} else {
					out, _ := pvjson.Marshal(patch)
					fmt.Println(string(out))
				}
			default:
				diff := difflib.UnifiedDiff{
					A:        difflib.SplitLines(string(jsonA)),
					B:        difflib.SplitLines(string(jsonB)),
					FromFile: "rev " + rev1,
					ToFile:   "rev " + rev2,
					Context:  3,
				}
				text, err := difflib.GetUnifiedDiffString(diff)
				if err != nil {
					return cli.NewExitError("Error generating diff: "+err.Error(), 4)
				}
				if text == "" {
					fmt.Println("No differences in state between rev " + rev1 + " and rev " + rev2)
				} else {
					fmt.Print(text)
				}
			}
			return nil
		},
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "format,f",
				Usage: "Output format: unified (default) or jsondiff",
				Value: "unified",
			},
		},
	}
}
