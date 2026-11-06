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

package wifi

import (
	"github.com/urfave/cli"
)

// CommandWifi : pvr wifi command
func CommandWifi() cli.Command {
	cmd := cli.Command{
		Name:    "wifi",
		Aliases: []string{"wf"},
		Usage:   "Set up Pantavisor devices over Bluetooth LE (Improv Wi-Fi)",
		Description: "\n   Join a Pantavisor device to Wi-Fi over Bluetooth LE with the Improv Wi-Fi\n" +
			"   protocol, and read what you need to claim it. On the device end is\n" +
			"   pvwificonnect, which advertises while it waits for Wi-Fi. Needs Linux with\n" +
			"   BlueZ running and access to the adapter (the bluetooth group, or root).\n\n" +
			"   Typical flow:\n\n" +
			"     pvr wifi device-scan                               # find the device and its address\n" +
			"     pvr wifi provision <address> --ssid MyWiFi         # prompts for the password\n" +
			"     pvr wifi claim-info <address>                      # device-id and challenge\n" +
			"     pvr claim <device-id> --challenge <challenge>      # claim it on Pantacor Hub\n\n" +
			"   claim-info only reads the claim material; claiming is done on the hub, by\n" +
			"   pvr claim or the Pantacor Hub web app.",
		Subcommands: wifiSubcommands(),
	}
	return cmd
}
