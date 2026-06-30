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

package app

import (
	"errors"
	"strings"

	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvr/libpvr"
	"gitlab.com/pantacor/pvr/utils/pvjson"
)

// TemplateArgConfigFlags are the shared flags used to inject template args and
// config values into an app's src.json. They are reused by app add, update and
// install so the semantics stay identical across commands.
func TemplateArgConfigFlags() []cli.Flag {
	return []cli.Flag{
		cli.StringSliceFlag{
			Name:   "arg",
			Usage:  "Template argument as KEY=VALUE (repeatable); merged into src.json args",
			EnvVar: "PVR_TEMPLATE_ARG",
		},
		cli.StringFlag{
			Name:   "arg-json",
			Usage:  "Template arguments as json file; merged into src.json args",
			EnvVar: "PVR_TEMPLATE_ARG",
		},
		cli.StringSliceFlag{
			Name:   "config",
			Usage:  "Config value as KEY=VALUE (repeatable); merged into src.json config",
			EnvVar: "PVR_CONFIG_JSON",
		},
		cli.StringSliceFlag{
			Name:  "unset-arg",
			Usage: "Template argument KEY to remove from src.json args (repeatable); applied after --arg",
		},
		cli.StringSliceFlag{
			Name:  "unset-config",
			Usage: "Config KEY to remove from src.json config (repeatable); applied after --config",
		},
	}
}

// ParseTemplateArgs builds the template args map from the --arg / --arg-json flags.
func ParseTemplateArgs(c *cli.Context) (map[string]interface{}, error) {
	if len(c.StringSlice("arg")) > 0 && c.String("arg-json") != "" {
		return nil, errors.New("--arg-json and --arg can't be set at the same time")
	}

	templateArgs := map[string]interface{}{}
	if c.String("arg-json") != "" {
		var err error
		templateArgs, err = libpvr.ReadArgsFile(c.String("arg-json"))
		if err != nil {
			return nil, err
		}
	}

	for _, v := range c.StringSlice("arg") {
		va := strings.SplitN(v, "=", 2)
		if len(va) == 2 {
			templateArgs[va[0]] = va[1]
		} else {
			templateArgs[va[0]] = ""
		}
	}

	return templateArgs, nil
}

// ParseConfig builds the config map from the --config flags. Values are parsed
// as JSON when possible (so numbers/booleans/arrays keep their type) and fall
// back to the raw string otherwise.
func ParseConfig(c *cli.Context) map[string]interface{} {
	config := map[string]interface{}{}
	for _, v := range c.StringSlice("config") {
		va := strings.SplitN(v, "=", 2)
		if len(va) == 2 {
			var value interface{}
			if err := pvjson.Unmarshal([]byte(va[1]), &value); err != nil {
				value = va[1]
			}
			config[va[0]] = value
		} else {
			config[va[0]] = ""
		}
	}
	return config
}
