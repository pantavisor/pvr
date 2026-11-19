// Copyright 2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package devmode

import (
	"errors"
	"path/filepath"

	"github.com/pantavisor/pvr/libpvr"
	"github.com/urfave/cli"
)

// pvrLoginToken reads the token that pvr login saved for the Hub. It never
// opens a browser or asks for a password.
func pvrLoginToken(c *cli.Context, base string) LoginToken {
	return func(renew bool) (string, error) {
		if t, _ := c.App.Metadata["PVR_AUTH"].(string); t != "" && !renew {
			return t, nil // --access-token
		}
		cfg, err := libpvr.LoadConfig(filepath.Join(configDir(c), "auth.json"))
		if err != nil {
			return "", errf(KindGeneric, "cannot read the pvr login: "+err.Error())
		}
		authEp := base + "/auth"
		key := authEp + " realm=pantahub services"
		t, ok := cfg.Tokens[key]
		if !ok || (t.AccessToken == "" && t.RefreshToken == "") {
			return "", errf(KindGeneric, "not logged in to "+base+"; run pvr login")
		}
		if renew {
			if t.RefreshToken == "" {
				return "", errf(KindGeneric, "the pvr login ran out; run pvr login")
			}
			access, refresh, err := cfg.DoRefresh(authEp, t.RefreshToken)
			if err != nil || access == "" {
				return "", errors.New("the pvr login ran out; run pvr login")
			}
			t.AccessToken, t.RefreshToken = access, refresh
			cfg.Tokens[key] = t
			_ = cfg.Save()
		}
		return t.AccessToken, nil
	}
}
