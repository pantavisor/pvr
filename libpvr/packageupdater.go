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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Transition source: pvr is published to the GitLab generic package registry by the
// package-deploy CI job, keyed by version (master, develop, 051, ...). These
// are plain binary tarballs, publicly downloadable with no auth - a far
// simpler and more robust upgrade source than parsing docker manifests.
const packageName = "pvr"

// vars so tests can point them at a local server.
var (
	gitlabProjectAPI  = "https://gitlab.com/api/v4/projects/pantacor%2Fpvr"
	gitlabGenericBase = gitlabProjectAPI + "/packages/generic/" + packageName
	gitlabPackageList = gitlabProjectAPI + "/packages?package_name=" + packageName +
		"&order_by=created_at&sort=desc&per_page=100"
)

// releaseVersionRe matches release versions (050, 051, 041-rev001) but not
// release candidates (049-rc1), branches (master) or long versions (050-abc123).
var releaseVersionRe = regexp.MustCompile(`^[0-9]+(-rev[0-9]+)?$`)

var packageHTTPClient = &http.Client{Timeout: 5 * time.Minute}

type glPackage struct {
	ID        int    `json:"id"`
	Version   string `json:"version"`
	CreatedAt string `json:"created_at"`
}

type glPackageFile struct {
	FileName   string `json:"file_name"`
	FileSha256 string `json:"file_sha256"`
	CreatedAt  string `json:"created_at"`
}

// resolveDistributionVersion maps a DistributionTag to a package version:
// latest -> master, stable -> newest release, anything else -> verbatim.
func (pvr *Pvr) resolveDistributionVersion(tag string) (string, error) {
	switch tag {
	case "latest", "main":
		return "master", nil
	case "stable":
		return latestReleaseVersion()
	default:
		return tag, nil
	}
}

func latestReleaseVersion() (string, error) {
	pkgs, err := fetchPackages()
	if err != nil {
		return "", err
	}
	// list is sorted by created_at desc, so the first release match is newest.
	for _, p := range pkgs {
		if releaseVersionRe.MatchString(p.Version) {
			return p.Version, nil
		}
	}
	return "", errors.New("no release package found in registry")
}

func fetchPackages() ([]glPackage, error) {
	var pkgs []glPackage
	if err := getJSON(gitlabPackageList, &pkgs); err != nil {
		return nil, err
	}
	return pkgs, nil
}

// packageArchExt maps the running platform to the package arch string and
// extension used in the package file name (e.g. linux.amd64 / tar.gz).
func packageArchExt() (string, string, error) {
	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "amd64", "arm64":
			return "linux." + runtime.GOARCH, "tar.gz", nil
		case "arm":
			return "linux.arm", "tar.gz", nil
		}
	case "darwin":
		switch runtime.GOARCH {
		case "amd64", "arm64":
			return "darwin." + runtime.GOARCH, "tar.gz", nil
		}
	}
	// ponytail: windows ships as .zip and replacing a running .exe is fiddly;
	// let the docker fallback handle those platforms.
	return "", "", fmt.Errorf("no package build for %s/%s", runtime.GOOS, runtime.GOARCH)
}

// packageFileSha returns the sha256 of the newest file matching fileName in the
// given package version, used as a freshness check before downloading.
func packageFileSha(version, fileName string) (string, error) {
	id, err := findPackageID(version)
	if err != nil {
		return "", err
	}

	var files []glPackageFile
	url := fmt.Sprintf("%s/packages/%d/package_files", gitlabProjectAPI, id)
	if err := getJSON(url, &files); err != nil {
		return "", err
	}

	var newest glPackageFile
	for _, f := range files {
		if f.FileName == fileName && f.CreatedAt > newest.CreatedAt {
			newest = f
		}
	}
	if newest.FileSha256 == "" {
		return "", fmt.Errorf("no file %s in package %s", fileName, version)
	}
	return newest.FileSha256, nil
}

func findPackageID(version string) (int, error) {
	pkgs, err := fetchPackages()
	if err != nil {
		return 0, err
	}
	for _, p := range pkgs {
		if p.Version == version {
			return p.ID, nil
		}
	}
	return 0, fmt.Errorf("package version %q not found", version)
}

// updateFromPackage downloads and installs pvr from the GitLab generic package
// registry. Returns an error (so UpdatePvr can fall back to docker) for any
// unsupported platform, network, or extraction failure.
func (pvr *Pvr) updateFromPackage(silent, force bool) error {
	tag := pvr.Session.Configuration.DistributionTag

	version, err := pvr.resolveDistributionVersion(tag)
	if err != nil {
		return err
	}

	arch, ext, err := packageArchExt()
	if err != nil {
		return err
	}

	fileName := fmt.Sprintf("%s.%s.%s.%s", packageName, version, arch, ext)
	downloadURL := fmt.Sprintf("%s/%s/%s", gitlabGenericBase, version, fileName)
	installedPath := filepath.Join(pvr.Session.configDir, lastUpdateFile)

	// Freshness check is best-effort: if the sha lookup fails we just download.
	newSha, shaErr := packageFileSha(version, fileName)
	if shaErr == nil && !force {
		prev, _ := ReadOrCreateFile(installedPath)
		if prev != nil && strings.TrimSpace(string(*prev)) == newSha {
			if !silent {
				fmt.Fprintln(os.Stderr, "You already have the latest version of PVR :) \n\r")
			}
			return nil
		}
	}

	fmt.Fprintf(os.Stderr, "Downloading pvr %s (%s) ...\r\n", version, fileName)

	tmpDir, err := os.MkdirTemp("", "pvr-pkg-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	tarPath := filepath.Join(tmpDir, fileName)
	if err := downloadFile(downloadURL, tarPath); err != nil {
		return err
	}

	extractPath := filepath.Join(tmpDir, "extract")
	if err := CreateFolder(extractPath); err != nil {
		return err
	}
	if err := Untar(extractPath, tarPath, []string{}); err != nil {
		return err
	}

	if err := installBinary(filepath.Join(extractPath, pvrCmd)); err != nil {
		return err
	}

	if newSha != "" {
		WriteTxtFile(installedPath, newSha)
	}
	if !silent {
		fmt.Fprintf(os.Stderr, "PVR has been updated to %s from GitLab packages! \r\n\r\n", version)
	}
	return nil
}

func downloadFile(url, dst string) error {
	resp, err := packageHTTPClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func getJSON(url string, out any) error {
	resp, err := packageHTTPClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
