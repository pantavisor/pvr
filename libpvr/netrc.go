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

package libpvr

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/bgentry/go-netrc/netrc"
)

// ReadUserPasswdFromNetrc reads the username and password for the given
// host from the .netrc file located in the current directory, falling back
// to $HOME/.netrc.
func ReadUserPasswdFromNetrc(host string) (string, string, error) {
	path := ".netrc"
	_, err := os.Stat(path)
	if err != nil {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		path = filepath.Join(homeDir, ".netrc")
		_, err = os.Stat(path)
		if err != nil {
			return "", "", err
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	file, err := netrc.Parse(f)
	if err != nil {
		return "", "", err
	}

	machine := file.FindMachine(host)
	if machine == nil {
		return "", "", errors.New("no matching machine found in .netrc for host " + host)
	}

	return machine.Login, machine.Password, nil
}
