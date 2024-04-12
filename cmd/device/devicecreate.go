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
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"gitlab.com/pantacor/pvr/libpvr"
	"gitlab.com/pantacor/pvr/utils/pvjson"

	"github.com/urfave/cli"
)

func CommandDeviceCreate() cli.Command {
	cmd := cli.Command{
		Name:        "create",
		Aliases:     []string{"cr"},
		ArgsUsage:   "[DEVICE_NICK]",
		Usage:       "pvr device create [DEVICE_NICK]",
		Description: "Creates a new device",
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

			baseURL := c.App.Metadata["PVR_BASEURL"].(string)
			deviceNick := ""
			userNick := ""
			loggedUserNick, _ := pvr.Session.GetWhoami()

			if c.NArg() > 1 {
				return errors.New("device create can have at most 1 argument. See --help")
			} else if c.NArg() == 1 {
				userNick, deviceNick = getUserDeviceNickFromRepo(c.Args()[0])
			}

			if userNick != "" && userNick != loggedUserNick {
				return errors.New("you can't create a device in " + userNick + " account, you are login as " + loggedUserNick)
			}

			// Create device
			deviceResponse, err := session.CreateDevice(baseURL, deviceNick)
			if err != nil {
				return cli.NewExitError(err, 2)
			}

			responseData := map[string]interface{}{}
			err = pvjson.Unmarshal(deviceResponse.Body(), &responseData)
			if err != nil {
				return cli.NewExitError(err, 2)
			}

			state, err := libpvr.StructToMap(pvr.PristineJsonMap)
			if err != nil {
				return cli.NewExitError(err, 2)
			}

			fmt.Println("Device created!")
			fmt.Println("Starting uploading state and objects")

			err = pvr.PutObjects(baseURL+"/objects/", true)
			if err != nil {
				return cli.NewExitError(err, 3)
			}

			// Login device
			dToken, err := libpvr.LoginDevice(
				baseURL,
				responseData["prn"].(string),
				responseData["secret"].(string),
			)
			if err != nil {
				return cli.NewExitError(err, 2)
			}

			//Create trail
			_, err = libpvr.CreateTrail(baseURL, dToken, state)
			if err != nil {
				return cli.NewExitError(err, 2)
			}
			libpvr.LogPrettyJSON(deviceResponse.Body())

			fmt.Println("Device Created Successfully")
			return nil
		},
	}

	return cmd
}

func getUserDeviceNickFromRepo(repoPath string) (userNick, deviceNick string) {
	if !libpvr.IsValidUrl(repoPath) {
		pathParts := strings.Split(repoPath, "/")
		if len(pathParts) >= 2 {
			userNick = pathParts[0]
			deviceNick = pathParts[1]
		} else {
			deviceNick = pathParts[0]
		}

		return userNick, deviceNick
	}

	uri, err := url.Parse(repoPath)
	if err != nil {
		return "", ""
	}
	pathParts := strings.Split(uri.Path, "/")
	if len(pathParts) >= 2 {
		userNick = pathParts[0]
		deviceNick = pathParts[1]
	}

	return userNick, deviceNick
}
