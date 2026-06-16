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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfigurationDefaultTag ensures fresh installs default to "latest".
func TestLoadConfigurationDefaultTag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	config, err := LoadConfiguration(path)
	if err != nil {
		t.Fatalf("LoadConfiguration failed: %v", err)
	}

	if config.DistributionTag != "latest" {
		t.Fatalf("expected default DistributionTag %q, got %q", "latest", config.DistributionTag)
	}
}

// TestLoadConfigurationMigratesDevelop ensures existing configs tracking the
// deprecated "develop" tag are migrated to "latest" and persisted to disk.
func TestLoadConfigurationMigratesDevelop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	if err := os.WriteFile(path, []byte(`{"Spec":"1","AutoUpgrade":true,"DistributionTag":"develop"}`), 0600); err != nil {
		t.Fatalf("writing seed config failed: %v", err)
	}

	config, err := LoadConfiguration(path)
	if err != nil {
		t.Fatalf("LoadConfiguration failed: %v", err)
	}

	if config.DistributionTag != "latest" {
		t.Fatalf("expected migrated DistributionTag %q, got %q", "latest", config.DistributionTag)
	}

	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading persisted config failed: %v", err)
	}
	if strings.Contains(string(persisted), "develop") {
		t.Fatalf("expected develop tag to be persisted as latest, got: %s", persisted)
	}
}
