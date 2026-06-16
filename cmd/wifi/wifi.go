//
// Copyright 2017-2025  Pantacor Ltd.
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

package wifi

import (
	"github.com/urfave/cli"
)

// CommandWifi : pvr wifi command
func CommandWifi() cli.Command {
	cmd := cli.Command{
		Name:    "wifi",
		Aliases: []string{"wf"},
		Usage:   "pvr wifi <device-scan|provision|identify|device-info|hostname|wifi-scan|claim-info>",
		Description: "\n1.Scan for Improv BLE devices\n" +
			"2.Provision an Improv BLE device with Wi-Fi credentials\n" +
			"3.Ask a device to identify itself\n" +
			"4.Read firmware and device information\n" +
			"5.Get or set the device hostname\n" +
			"6.Scan for Wi-Fi networks from the device\n" +
			"7.Read Pantavisor claim material",
		Subcommands: []cli.Command{
			CommandDeviceScan(),
			CommandProvision(),
			CommandIdentify(),
			CommandDeviceInfo(),
			CommandHostname(),
			CommandWifiScan(),
			CommandClaimInfo(),
		},
	}
	return cmd
}
