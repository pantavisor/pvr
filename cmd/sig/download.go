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

package sig

import (
	"fmt"
	"os"

	"github.com/pantavisor/pvr/libpvr"
	"github.com/urfave/cli"
)

func CommandSigDownload() cli.Command {
	return cli.Command{
		Name:      "download",
		Aliases:   []string{"d"},
		ArgsUsage: "",
		Usage:     "download certificates",
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:   "url",
				EnvVar: "PVR_SIG_CERTS_URL",
				Usage:  "url to download certificates from",
				Value:  defaultCertsDownloadUrl,
			},
		},
		Action: func(c *cli.Context) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}

			session, err := libpvr.NewSession(c.App)
			if err != nil {
				return cli.NewExitError(err, 4)
			}

			pvr, err := libpvr.NewPvr(session, wd)
			if err != nil {
				return cli.NewExitError(err, 2)
			}

			certsToDownload := []string{
				libpvr.SigCacertFilename,
				libpvr.SigKeyFilename,
				libpvr.SigX5cFilename,
			}
			certs := []string{}
			for _, certToDownload := range certsToDownload {
				cert, err := libpvr.GetFromConfigPvs(
					c.String("url"),
					pvr.Session.GetConfigDir(),
					certToDownload,
					true,
				)
				if err != nil {
					return cli.NewExitError(err, 127)
				}
				certs = append(certs, cert)
			}

			fmt.Printf("Downloaded certificates:\n")
			for _, cert := range certs {
				fmt.Printf("%s\n", cert)
			}
			return nil
		},
	}
}
