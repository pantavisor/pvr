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

package libpvr

import (
	"os"
	"path/filepath"
	"testing"
)

// seedApp writes a src.json for appname under a fresh temp dir and returns a
// Pvr rooted there.
func seedApp(t *testing.T, appname, srcJSON string) *Pvr {
	t.Helper()
	dir := t.TempDir()
	appDir := filepath.Join(dir, appname)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, SRC_FILE), []byte(srcJSON), 0644); err != nil {
		t.Fatalf("write src.json: %v", err)
	}
	return &Pvr{Dir: dir}
}

func TestMergeAppSrcArgs_SetAndMerge(t *testing.T) {
	p := seedApp(t, "app", `{"#spec":"service-manifest-src@1","template":"builtin-lxc-docker","args":{"PV_RUNLEVEL":"app"},"config":{"existing":"1"}}`)

	err := p.MergeAppSrcArgs("app",
		map[string]interface{}{"PV_DEV_LOG": "false"},
		map[string]interface{}{"newcfg": true},
		nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	m, err := p.GetApplicationManifest("app")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.TemplateArgs["PV_DEV_LOG"]; got != "false" {
		t.Errorf("PV_DEV_LOG = %v, want false", got)
	}
	if got := m.TemplateArgs["PV_RUNLEVEL"]; got != "app" {
		t.Errorf("existing arg lost: PV_RUNLEVEL = %v", got)
	}
	if got := m.Config["newcfg"]; got != true {
		t.Errorf("newcfg = %v, want true", got)
	}
	if got := m.Config["existing"]; got != "1" {
		t.Errorf("existing config lost: existing = %v", got)
	}
}

func TestMergeAppSrcArgs_Unset(t *testing.T) {
	p := seedApp(t, "app", `{"#spec":"service-manifest-src@1","template":"builtin-lxc-docker","args":{"PV_DEV_LOG":"false","PV_RUNLEVEL":"app"},"config":{"a":"1","b":"2"}}`)

	err := p.MergeAppSrcArgs("app",
		map[string]interface{}{"PV_NEW": "x"}, nil,
		[]string{"PV_DEV_LOG"}, []string{"b"})
	if err != nil {
		t.Fatal(err)
	}

	m, err := p.GetApplicationManifest("app")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.TemplateArgs["PV_DEV_LOG"]; ok {
		t.Errorf("PV_DEV_LOG should be removed, got %v", m.TemplateArgs)
	}
	if m.TemplateArgs["PV_RUNLEVEL"] != "app" {
		t.Errorf("untouched arg PV_RUNLEVEL lost")
	}
	if m.TemplateArgs["PV_NEW"] != "x" {
		t.Errorf("PV_NEW not set")
	}
	if _, ok := m.Config["b"]; ok {
		t.Errorf("config b should be removed")
	}
	if m.Config["a"] != "1" {
		t.Errorf("untouched config a lost")
	}
}

func TestMergeAppSrcArgs_UnsetWinsOverSet(t *testing.T) {
	p := seedApp(t, "app", `{"#spec":"service-manifest-src@1","template":"builtin-lxc-docker","args":{"PV_DEV_LOG":"false"}}`)

	// same key set and unset in one call -> unset wins
	err := p.MergeAppSrcArgs("app",
		map[string]interface{}{"PV_DEV_LOG": "true"}, nil,
		[]string{"PV_DEV_LOG"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	m, err := p.GetApplicationManifest("app")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.TemplateArgs["PV_DEV_LOG"]; ok {
		t.Errorf("unset should win over set, got %v", m.TemplateArgs)
	}
}

func TestMergeAppSrcArgs_NoOpDoesNotTouchFile(t *testing.T) {
	seed := `{"#spec":"service-manifest-src@1","template":"builtin-lxc-docker","args":{"PV_RUNLEVEL":"app"}}`
	p := seedApp(t, "app", seed)
	srcPath := filepath.Join(p.Dir, "app", SRC_FILE)

	before, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	// nothing provided -> no-op, file left byte-for-byte unchanged
	if err := p.MergeAppSrcArgs("app", nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("no-op call rewrote src.json:\nbefore: %s\nafter:  %s", before, after)
	}
}

func TestMergeAppSrcArgs_InitialisesNilMaps(t *testing.T) {
	// src.json without args/config keys at all
	p := seedApp(t, "app", `{"#spec":"service-manifest-src@1","template":"builtin-lxc-docker"}`)

	err := p.MergeAppSrcArgs("app",
		map[string]interface{}{"PV_DEV_LOG": "false"},
		map[string]interface{}{"k": "v"},
		nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	m, err := p.GetApplicationManifest("app")
	if err != nil {
		t.Fatal(err)
	}
	if m.TemplateArgs["PV_DEV_LOG"] != "false" {
		t.Errorf("args not initialised/set: %v", m.TemplateArgs)
	}
	if m.Config["k"] != "v" {
		t.Errorf("config not initialised/set: %v", m.Config)
	}
}

func TestMergeAppSrcArgs_MissingApp(t *testing.T) {
	p := seedApp(t, "app", `{"#spec":"service-manifest-src@1"}`)
	// providing changes for a non-existent app should error (manifest read fails)
	err := p.MergeAppSrcArgs("does-not-exist", map[string]interface{}{"X": "1"}, nil, nil, nil)
	if err == nil {
		t.Errorf("expected error for missing app, got nil")
	}
}
