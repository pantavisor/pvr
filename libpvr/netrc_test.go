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
	"testing"
)

// chdirTemp switches the working directory to a fresh temp dir for the
// duration of the test and restores it afterwards.
func chdirTemp(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(orig)
	})
	return dir
}

func TestReadUserPasswdFromNetrcFindsMatchingMachine(t *testing.T) {
	dir := chdirTemp(t)

	netrc := "machine api.pantahub.com login alice password s3cr3t\n"
	if err := os.WriteFile(filepath.Join(dir, ".netrc"), []byte(netrc), 0600); err != nil {
		t.Fatalf("failed to write .netrc: %v", err)
	}

	user, pass, err := ReadUserPasswdFromNetrc("api.pantahub.com")
	if err != nil {
		t.Fatalf("ReadUserPasswdFromNetrc failed: %v", err)
	}
	if user != "alice" || pass != "s3cr3t" {
		t.Fatalf("expected alice/s3cr3t, got %s/%s", user, pass)
	}
}

func TestReadUserPasswdFromNetrcNoMatchingMachine(t *testing.T) {
	dir := chdirTemp(t)

	netrc := "machine other.pantahub.com login alice password s3cr3t\n"
	if err := os.WriteFile(filepath.Join(dir, ".netrc"), []byte(netrc), 0600); err != nil {
		t.Fatalf("failed to write .netrc: %v", err)
	}

	_, _, err := ReadUserPasswdFromNetrc("api.pantahub.com")
	if err == nil {
		t.Fatal("expected error for non-matching host, got nil")
	}
}

func TestReadUserPasswdFromNetrcNoFile(t *testing.T) {
	chdirTemp(t)
	t.Setenv("HOME", t.TempDir())

	_, _, err := ReadUserPasswdFromNetrc("api.pantahub.com")
	if err == nil {
		t.Fatal("expected error when no .netrc file exists, got nil")
	}
}
