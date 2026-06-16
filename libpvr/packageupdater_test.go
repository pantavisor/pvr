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
	"runtime"
	"testing"
)

func TestResolveDistributionVersion(t *testing.T) {
	pvr := &Pvr{}
	cases := map[string]string{
		"latest":  "master",
		"051":     "051",
		"develop": "develop",
		"master":  "master",
	}
	for tag, want := range cases {
		got, err := pvr.resolveDistributionVersion(tag)
		if err != nil {
			t.Fatalf("resolveDistributionVersion(%q) error: %v", tag, err)
		}
		if got != want {
			t.Errorf("resolveDistributionVersion(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestReleaseVersionRe(t *testing.T) {
	releases := []string{"050", "051", "041-rev001"}
	for _, v := range releases {
		if !releaseVersionRe.MatchString(v) {
			t.Errorf("expected %q to match as a release", v)
		}
	}
	nonReleases := []string{"049-rc1", "master", "develop", "050-712326cb", ""}
	for _, v := range nonReleases {
		if releaseVersionRe.MatchString(v) {
			t.Errorf("expected %q NOT to match as a release", v)
		}
	}
}

func TestPackageArchExt(t *testing.T) {
	arch, ext, err := packageArchExt()

	supported := (runtime.GOOS == "linux" || runtime.GOOS == "darwin") &&
		(runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" ||
			(runtime.GOOS == "linux" && runtime.GOARCH == "arm"))

	if !supported {
		if err == nil {
			t.Fatalf("expected error for unsupported %s/%s, got %q/%q", runtime.GOOS, runtime.GOARCH, arch, ext)
		}
		return
	}

	if err != nil {
		t.Fatalf("packageArchExt() error on supported platform %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
	}
	if want := runtime.GOOS + "." + runtime.GOARCH; arch != want {
		t.Errorf("arch = %q, want %q", arch, want)
	}
	if ext != "tar.gz" {
		t.Errorf("ext = %q, want tar.gz", ext)
	}
}
