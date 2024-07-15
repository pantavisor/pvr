//go:build linux && go1.21 && experimental
// +build linux,go1.21,experimental

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

package fakeroot

import (
	"os"
	"os/exec"

	"github.com/urfave/cli"
	"lure.sh/fakeroot"
	"lure.sh/fakeroot/loginshell"
)

func CommandFakeroot() cli.Command {
	return cli.Command{
		Name:  "fakeroot",
		Usage: "fakeroot [commands...]",
		Action: func(c *cli.Context) error {

			var (
				cmd  string
				args []string
				err  error
			)
			if c.NArg() > 0 {
				cmd = c.Args().First()
				args = c.Args().Tail()
			} else {
				cmd, err = loginshell.Get(-1)
				if err != nil {
					return err
				}
			}

			fakeCmd, err := fakeroot.Command(cmd, args...)
			if err != nil {
				return err
			}

			fakeCmd.Stdin = os.Stdin
			fakeCmd.Stdout = os.Stdout
			fakeCmd.Stderr = os.Stderr

			err = fakeCmd.Run()
			if err, ok := err.(*exec.ExitError); ok {
				os.Exit(err.ExitCode())
			}

			if err != nil {
				return err
			}

			return nil
		},
	}
}
