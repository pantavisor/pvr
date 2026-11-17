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

	"github.com/go-resty/resty"
	"github.com/pantavisor/pvr/libpvr"
	"github.com/pantavisor/pvr/utils/pvjson"
	"github.com/urfave/cli"
)

func getStepState(session *libpvr.Session, baseURL, deviceNick, rev string) (map[string]interface{}, error) {
	stepURL := baseURL + "/trails/" + deviceNick + "/steps/" + rev + "/state"
	response, err := session.DoAuthCall(false, libpvr.NoneCallMethod, func(req *resty.Request) error {
		req.Method = resty.MethodGet
		req.URL = stepURL
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error getting step %s: %w", rev, err)
	}
	if response.StatusCode() != 200 {
		return nil, fmt.Errorf("error getting step %s: %s", rev, string(response.Body()))
	}
	var state map[string]interface{}
	err = pvjson.Unmarshal(response.Body(), &state)
	if err != nil {
		return nil, fmt.Errorf("error parsing step %s state: %w", rev, err)
	}
	return state, nil
}

// getDeviceInfo fetches device details and returns the device ID and running revision.
func getDeviceInfo(session *libpvr.Session, baseURL, deviceNick string) (id string, rev string, err error) {
	deviceResponse, err := session.GetDevice(baseURL, deviceNick, "")
	if err != nil {
		return "", "", fmt.Errorf("error getting device: %w", err)
	}
	var device map[string]interface{}
	err = pvjson.Unmarshal(deviceResponse.Body(), &device)
	if err != nil {
		return "", "", fmt.Errorf("error parsing device response: %w", err)
	}

	idVal, ok := device["id"]
	if !ok {
		return "", "", errors.New("id not found in device response")
	}
	id, _ = idVal.(string)

	deviceMeta, ok := device["device-meta"].(map[string]interface{})
	if !ok {
		return id, "", nil
	}
	revVal, ok := deviceMeta["pantavisor.revision"]
	if !ok {
		return id, "", nil
	}
	rev, ok = revVal.(string)
	if !ok {
		revBytes, _ := pvjson.Marshal(revVal, pvjson.MarshalOptions{Compact: true})
		rev = string(revBytes)
	}
	return id, rev, nil
}

func getRunningRev(session *libpvr.Session, baseURL, deviceNick string) (string, error) {
	_, rev, err := getDeviceInfo(session, baseURL, deviceNick)
	if err != nil {
		return "", err
	}
	if rev == "" {
		return "", errors.New("pantavisor.revision not found in device-meta")
	}
	return rev, nil
}

func completeDeviceNick(c *cli.Context) {
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
}

func CommandSteps() cli.Command {
	return cli.Command{
		Name:         "steps",
		Aliases:      []string{"step"},
		ArgsUsage:    "<NICK|ID>",
		Usage:        "pvr device steps <NICK|ID> [--rev=N]",
		Description:  "List steps of a device trail. Without --rev lists all steps, with --rev shows a specific step.",
		BashComplete: completeDeviceNick,
		Subcommands: []cli.Command{
			CommandStepsCancel(),
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

			var stepsURL string
			if rev != "" {
				stepsURL = info.BaseURL + "/trails/" + info.DeviceID + "/steps/" + rev
			} else {
				stepsURL = info.BaseURL + "/trails/" + info.DeviceID + "/steps?progress.status="
			}

			response, err := session.DoAuthCall(false, libpvr.NoneCallMethod, func(req *resty.Request) error {
				req.Method = resty.MethodGet
				req.URL = stepsURL
				return nil
			})
			if err != nil {
				return cli.NewExitError("Error getting steps: "+err.Error(), 4)
			}
			if response.StatusCode() != 200 {
				return cli.NewExitError("Error getting steps: "+string(response.Body()), 4)
			}

			libpvr.LogPrettyJSON(response.Body())
			return nil
		},
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:   "rev,r",
				Usage:  "Show a specific step revision, e.g. --rev=4",
				EnvVar: "PVR_STEPS_REV",
			},
		},
	}
}

// CommandStepsCancel : pvr device steps cancel
func CommandStepsCancel() cli.Command {
	return cli.Command{
		Name:         "cancel",
		ArgsUsage:    "<NICK|ID|URL>",
		Usage:        "pvr device steps cancel <NICK|ID|URL> --rev=N",
		Description:  "Cancel a pending step of a device trail. The Hub marks the step CANCEL if it is still NEW, QUEUED, DOWNLOADING or INPROGRESS; the device drops the update and keeps its current revision.",
		BashComplete: completeDeviceNick,
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
			if rev == "" {
				rev = info.Rev
			}
			if rev == "" {
				return cli.NewExitError(errors.New("Step revision is required: pass --rev=N or a pvr URL ending in the revision. See --help"), 2)
			}

			cancelURL := info.BaseURL + "/trails/" + info.DeviceID + "/steps/" + rev + "/cancel"
			response, err := session.DoAuthCall(false, libpvr.NoneCallMethod, func(req *resty.Request) error {
				req.Method = resty.MethodPut
				req.URL = cancelURL
				return nil
			})
			if err != nil {
				return cli.NewExitError("Error cancelling step "+rev+": "+err.Error(), 4)
			}
			if response.StatusCode() != 200 {
				return cli.NewExitError("Error cancelling step "+rev+": "+string(response.Body()), 4)
			}

			libpvr.LogPrettyJSON(response.Body())
			return nil
		},
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:   "rev,r",
				Usage:  "Step revision to cancel, e.g. --rev=4",
				EnvVar: "PVR_STEPS_REV",
			},
		},
	}
}
