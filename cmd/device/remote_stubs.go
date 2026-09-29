//go:build !go1.22

//
// Copyright 2026  Pantacor Ltd.
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

import "github.com/urfave/cli"

// live-logs and ssh depend on gorilla/websocket and a recent x/crypto, which
// the Go 1.21 build (go1.21.mod) does not carry.

func CommandLiveLogs() cli.Command {
	return cli.Command{
		Name:   "live-logs",
		Hidden: true,
		Action: func(c *cli.Context) error {
			return cli.NewExitError("pvr device live-logs is only available in Go 1.22+ builds", 1)
		},
	}
}

func CommandSSH() cli.Command {
	return cli.Command{
		Name:   "ssh",
		Hidden: true,
		Action: func(c *cli.Context) error {
			return cli.NewExitError("pvr device ssh is only available in Go 1.22+ builds", 1)
		},
	}
}
