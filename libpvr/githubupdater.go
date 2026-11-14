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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// githubAPIBase is a var so tests can point it at a local server.
var githubAPIBase = "https://api.github.com/repos/pantavisor/pvr"

// snapshotRelease is the rolling prerelease rebuilt on every push to main.
const snapshotRelease = "main-snapshot"

type ghAsset struct {
	Name               string `json:"name"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Prerelease bool      `json:"prerelease"`
	Draft      bool      `json:"draft"`
	Assets     []ghAsset `json:"assets"`
}

// resolveGitHubReleaseTag maps a DistributionTag to a release tag:
// stable -> newest non-prerelease release, latest/master/develop/main ->
// the rolling snapshot, anything else -> that tag verbatim.
func resolveGitHubReleaseTag(tag string) (string, error) {
	switch tag {
	case "latest", "master", "develop", "main":
		return snapshotRelease, nil
	case "stable":
		var rels []ghRelease
		if err := githubGetJSON(githubAPIBase+"/releases?per_page=100", &rels); err != nil {
			return "", err
		}
		// GitHub lists newest first, so the first match is the newest release.
		for _, r := range rels {
			if !r.Prerelease && !r.Draft && releaseVersionRe.MatchString(r.TagName) {
				return r.TagName, nil
			}
		}
		return "", errors.New("no release found on GitHub")
	default:
		return tag, nil
	}
}

func githubGetJSON(u string, out any) error {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := packageHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// updateFromGitHub downloads and installs pvr from a GitHub release. Any
// error lets UpdatePvr fall back to GitLab packages and then docker.
func (pvr *Pvr) updateFromGitHub(silent, force bool) error {
	relTag, err := resolveGitHubReleaseTag(pvr.Session.Configuration.DistributionTag)
	if err != nil {
		return err
	}

	arch, ext, err := packageArchExt()
	if err != nil {
		return err
	}

	var rel ghRelease
	if err := githubGetJSON(githubAPIBase+"/releases/tags/"+url.PathEscape(relTag), &rel); err != nil {
		return err
	}

	fileName := fmt.Sprintf("%s.%s.%s.%s", packageName, relTag, arch, ext)
	var asset *ghAsset
	for i := range rel.Assets {
		if rel.Assets[i].Name == fileName {
			asset = &rel.Assets[i]
		}
	}
	if asset == nil {
		return fmt.Errorf("release %s has no asset %s", relTag, fileName)
	}

	installedPath := filepath.Join(pvr.Session.configDir, lastUpdateFile)
	prev, _ := ReadOrCreateFile(installedPath)
	prevSha := ""
	if prev != nil {
		prevSha = strings.TrimSpace(string(*prev))
	}
	wantSha := strings.TrimPrefix(asset.Digest, "sha256:")

	// The digest field avoids a download when nothing changed.
	if wantSha != "" && !force && prevSha == wantSha {
		if !silent {
			fmt.Fprintln(os.Stderr, "You already have the latest version of PVR :) \n\r")
		}
		return nil
	}

	fmt.Fprintf(os.Stderr, "Downloading pvr %s (%s) from GitHub releases ...\r\n", relTag, fileName)

	tmpDir, err := os.MkdirTemp("", "pvr-gh-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	tarPath := filepath.Join(tmpDir, fileName)
	if err := downloadFile(asset.BrowserDownloadURL, tarPath); err != nil {
		return err
	}

	gotSha, err := sha256File(tarPath)
	if err != nil {
		return err
	}
	if wantSha != "" && gotSha != wantSha {
		return fmt.Errorf("%s: sha256 %s does not match release digest %s", fileName, gotSha, wantSha)
	}
	if wantSha == "" && !force && prevSha == gotSha {
		if !silent {
			fmt.Fprintln(os.Stderr, "You already have the latest version of PVR :) \n\r")
		}
		return nil
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

	WriteTxtFile(installedPath, gotSha)
	if !silent {
		fmt.Fprintf(os.Stderr, "PVR has been updated to %s from GitHub releases! \r\n\r\n", relTag)
	}
	return nil
}
