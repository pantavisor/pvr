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

package devmode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultUser = "_pv_"
	DefaultPort = 8222
)

// Target is where an ssh connection goes.
type Target struct {
	User string
	Host string
	Port int
}

func (t Target) String() string {
	return fmt.Sprintf("%s@%s:%d", t.User, t.Host, t.Port)
}

// ParseTarget understands user@host[:port], host[:port] and [v6]:port.
func ParseTarget(s string) (Target, error) {
	t := Target{User: DefaultUser, Port: DefaultPort}
	if s == "" {
		return t, fmt.Errorf("empty device target")
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		if i == 0 {
			return t, fmt.Errorf("empty user in %q", s)
		}
		t.User, s = s[:i], s[i+1:]
	}
	host, port := s, ""
	switch {
	case strings.HasPrefix(s, "["):
		end := strings.Index(s, "]")
		if end < 0 {
			return t, fmt.Errorf("bad address %q", s)
		}
		host = s[1:end]
		rest := s[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return t, fmt.Errorf("bad address %q", s)
			}
			port = rest[1:]
		}
	case strings.Count(s, ":") == 1:
		i := strings.Index(s, ":")
		host, port = s[:i], s[i+1:]
	}
	if host == "" {
		return t, fmt.Errorf("empty host in %q", s)
	}
	if port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return t, fmt.Errorf("bad port %q", port)
		}
		t.Port = p
	}
	t.Host = host
	return t, nil
}

// Alias is a saved device. Exactly one of Target and Exec is set.
type Alias struct {
	Target string `json:"target,omitempty"`
	Exec   string `json:"exec,omitempty"`
}

type aliasFile struct {
	Schema  int              `json:"schema"`
	Devices map[string]Alias `json:"devices"`
}

// AliasStore keeps aliases in <config dir>/devmode-devices.json.
type AliasStore struct{ Path string }

func NewAliasStore(configDir string) *AliasStore {
	return &AliasStore{Path: filepath.Join(configDir, "devmode-devices.json")}
}

func (s *AliasStore) load() (aliasFile, error) {
	f := aliasFile{Schema: 1, Devices: map[string]Alias{}}
	b, err := os.ReadFile(s.Path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("%s: %w", s.Path, err)
	}
	if f.Devices == nil {
		f.Devices = map[string]Alias{}
	}
	return f, nil
}

func (s *AliasStore) List() (map[string]Alias, error) {
	f, err := s.load()
	return f.Devices, err
}

func (s *AliasStore) Get(name string) (Alias, bool, error) {
	f, err := s.load()
	a, ok := f.Devices[name]
	return a, ok, err
}

func (s *AliasStore) Set(name string, a Alias) error {
	if name == "" || strings.ContainsAny(name, "@:/ \t[]") {
		return fmt.Errorf("bad alias name %q", name)
	}
	f, err := s.load()
	if err != nil {
		return err
	}
	f.Schema = 1
	f.Devices[name] = a
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

func (s *AliasStore) Names() ([]string, error) {
	m, err := s.List()
	n := make([]string, 0, len(m))
	for k := range m {
		n = append(n, k)
	}
	sort.Strings(n)
	return n, err
}
