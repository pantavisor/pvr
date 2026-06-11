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
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-resty/resty"
	"gitlab.com/pantacor/pvr/models"
	"gitlab.com/pantacor/pvr/utils/pvjson"
	"gopkg.in/cheggaaa/pb.v1"
)

var baseURL = "https://gitlab.com"

// GitLabPackage represents a GitLab package from the API
type GitLabPackage struct {
	ProjectPath string
	ID          int                 `json:"id"`
	Name        string              `json:"name"`
	Version     string              `json:"version"`
	PackageType string              `json:"package_type"`
	Status      string              `json:"status"`
	Files       []GitLabPackageFile `json:"package_files"`
}

// GitLabPackageFile represents a file within a GitLab package
type GitLabPackageFile struct {
	ARCH       string
	ID         int    `json:"id"`
	FileName   string `json:"file_name"`
	Size       int    `json:"size"`
	FileURL    string `json:"file_url"`
	FileSHA256 string `json:"file_sha256"`
}

// parseGitLabURL extracts project path and optional version from GitLab URL
func parseGitLabURL(from string) (string, string, error) {
	// Handle URLs like:
	// https://gitlab.com/pantacor/pvwificonnect
	// https://gitlab.com/pantacor/pvwificonnect@1.0.0

	parts := strings.Split(from, "@")
	gitlabURL := parts[0]
	version := ""

	if len(parts) > 1 {
		version = parts[1]
	}

	// Extract project path from URL
	u, err := url.Parse(gitlabURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid GitLab URL: %s", err)
	}

	if !strings.Contains(u.Host, "gitlab") {
		return "", "", fmt.Errorf("not a GitLab URL: %s", gitlabURL)
	}

	// Remove leading slash and get project path
	projectPath := strings.TrimPrefix(u.Path, "/")
	if projectPath == "" {
		return "", "", fmt.Errorf("empty project path in GitLab URL")
	}

	return projectPath, version, nil
}

// getGitLabPackages fetches packages for a GitLab project and populates their files using the specified API.
func getGitLabPackages(p *Pvr, projectPath string) ([]GitLabPackage, error) {
	encodedPath := url.PathEscape(projectPath)

	// Step 1: Fetch packages list (without including files initially, as per prompt's implication)
	packagesAPIURL := fmt.Sprintf("%s/api/v4/projects/%s/packages?order_by=created_at&sort=desc", baseURL, encodedPath)

	var resp *resty.Response
	var err error
	var packages []GitLabPackage

	fmt.Println("Fetching GitLab packages...")
	req := resty.NewRequest()
	resp, err = req.SetHeader("Accept", "application/json").Get(packagesAPIURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch GitLab packages list: %s", err)
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("GitLab API returned status %d fetching packages list: %s", resp.StatusCode(), resp.String())
	}
	if err := pvjson.Unmarshal(resp.Body(), &packages); err != nil {
		return nil, fmt.Errorf("failed to parse GitLab packages list response: %s", err)
	}

	for i := range packages {
		packages[i].ProjectPath = encodedPath
	}

	return packages, nil
}

// findBestPackage finds the best matching package for the given version or latest.
// It now operates on a slice of GitLabPackage where each package's Files field is already populated
// by getGitLabPackages according to the new API method.
func findBestPackage(packages []GitLabPackage, requestedVersion string, platform string) (*GitLabPackage, error) {
	if len(packages) == 0 {
		return nil, errors.New("no packages found")
	}

	// Sort by version (semantic versioning aware)
	sort.Slice(packages, func(i, j int) bool {
		return compareVersions(packages[i].Version, packages[j].Version) > 0
	})

	var pkg *GitLabPackage
	// If specific version requested, find exact match
	if requestedVersion != "" && requestedVersion != "latest" {
		for _, p := range packages {
			if p.Version == requestedVersion {
				pkg = &p
				break
			}
		}
		if pkg == nil {
			return nil, fmt.Errorf("version %s not found", requestedVersion)
		}
	} else {
		pkg = &packages[0]
	}

	fmt.Printf("Fetching files for package version %s...\n", pkg.Version)
	filesAPIURL := fmt.Sprintf("%s/api/v4/projects/%s/packages/%d/package_files?order_by=created_at&sort=desc", baseURL, pkg.ProjectPath, pkg.ID)

	var filesResp *resty.Response
	var filesErr error
	req := resty.NewRequest()
	filesResp, filesErr = req.SetHeader("Accept", "application/json").Get(filesAPIURL)
	if filesErr != nil {
		return nil, fmt.Errorf("failed to fetch files for package %d (%s): %s", pkg.ID, pkg.Name, filesErr)
	}
	if filesResp.StatusCode() != http.StatusOK {
		return nil, fmt.Errorf("GitLab API returned status %d fetching files for package %d (%s): %s", filesResp.StatusCode(), pkg.ID, pkg.Name, filesResp.String())
	}

	files := []GitLabPackageFile{}
	pkg.Files = []GitLabPackageFile{}
	// Unmarshal the files response directly into the pkg.Files slice
	if err := pvjson.Unmarshal(filesResp.Body(), &files); err != nil {
		return nil, fmt.Errorf("failed to parse GitLab package files response for package %d (%s): %s", pkg.ID, pkg.Name, err)
	}

	for _, file := range files {
		if strings.HasSuffix(file.FileName, ".tgz") {
			// Extract ARCH from file.FileName
			trimmedFileName := strings.TrimSuffix(file.FileName, ".tgz")
			lastDotIndex := strings.LastIndex(trimmedFileName, ".")
			if lastDotIndex != -1 {
				file.ARCH = trimmedFileName[lastDotIndex+1:]
			}

			if platform != "" {
				if strings.Contains(file.FileName, "."+platform+".") {
					pkg.Files = append(pkg.Files, file)
					break
				}
			} else {
				pkg.Files = append(pkg.Files, file)
				break
			}
		}
	}

	return pkg, nil
}

// compareVersions compares two version strings (basic semantic versioning)
func compareVersions(v1, v2 string) int {
	// Simple version comparison - convert to numeric values
	normalize := func(v string) []int {
		// Remove leading 'v' if present
		v = strings.TrimPrefix(v, "v")

		// Split by dots and convert to integers
		parts := strings.Split(v, ".")
		nums := make([]int, 0, 3)

		for _, part := range parts {
			// Extract numeric part (ignore non-numeric suffixes)
			re := regexp.MustCompile(`^(\d+)`)
			match := re.FindStringSubmatch(part)
			if len(match) > 1 {
				if num, err := strconv.Atoi(match[1]); err == nil {
					nums = append(nums, num)
				}
			}
		}

		// Pad to at least 3 components
		for len(nums) < 3 {
			nums = append(nums, 0)
		}

		return nums
	}

	nums1 := normalize(v1)
	nums2 := normalize(v2)

	for i := 0; i < len(nums1) && i < len(nums2); i++ {
		if nums1[i] > nums2[i] {
			return 1
		} else if nums1[i] < nums2[i] {
			return -1
		}
	}

	return 0
}

func downloadPackage(p *Pvr, pkg *GitLabPackage, destPath string, platform string, projectPath string) (string, error) {
	var tgzFile *GitLabPackageFile
	var genericTgzFile *GitLabPackageFile

	// Find the .tgz file

	for i := range pkg.Files {
		if strings.HasSuffix(pkg.Files[i].FileName, ".tgz") {
			if platform != "" && strings.Contains(pkg.Files[i].FileName, "."+platform+".") {
				tgzFile = &pkg.Files[i]
				break
			}
			if genericTgzFile == nil {
				genericTgzFile = &pkg.Files[i]
			}
		}
	}

	if tgzFile == nil {
		tgzFile = genericTgzFile
	}

	if tgzFile == nil {
		return "", errors.New("no .tgz file found in package")
	}

	fmt.Printf("Downloading file: %s\n", tgzFile.FileName)
	// Download the file
	downloadURL := fmt.Sprintf("https://gitlab.com/%s/-/package_files/%d/download", projectPath, tgzFile.ID)

	client := resty.New()
	client.SetDoNotParseResponse(true)

	resp, err := client.R().Get(downloadURL)
	if err != nil {
		return "", fmt.Errorf("failed to download package: %s", err)
	}
	defer resp.RawResponse.Body.Close()

	if resp.StatusCode() != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode())
	}

	// Write to destination
	downloadPath := filepath.Join(destPath, tgzFile.FileName)
	out, err := os.Create(downloadPath)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %s", err)
	}
	defer out.Close()

	bar := pb.New(tgzFile.Size).SetUnits(pb.U_BYTES)
	bar.Start()

	rd := bar.NewProxyReader(resp.RawResponse.Body)

	_, err = io.Copy(out, rd)
	if err != nil {
		return "", fmt.Errorf("failed to write downloaded file: %s", err)
	}

	bar.Finish()

	fmt.Println("Verifying sha256...")
	downloadedFileSha256, err := calculateSHA256(downloadPath)
	if err != nil {
		os.Remove(downloadPath)
		return "", fmt.Errorf("failed to calculate sha256 of downloaded file: %s", err)
	}

	fmt.Printf("Downloaded file sha256: %s\n", downloadedFileSha256)
	fmt.Printf("Package sha256: %s\n", tgzFile.FileSHA256)

	if tgzFile.FileSHA256 != downloadedFileSha256 {
		os.Remove(downloadPath)
		return "", fmt.Errorf("downloaded file sha256 does not match expected sha256")
	}

	fmt.Println("Downloaded file sha256 matches expected sha256")
	fmt.Println("Done!")

	return downloadPath, nil

}

// extractTgz extracts a .tgz file to the destination directory
func extractTgz(tgzPath, destDir string) error {
	file, err := os.Open(tgzPath)
	if err != nil {
		return fmt.Errorf("failed to open .tgz file: %s", err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %s", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading tar: %s", err)
		}

		target := filepath.Join(destDir, header.Name)

		// Ensure target is within destDir (security check)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("invalid file path: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to create directory: %s", err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory: %s", err)
			}

			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file: %s", err)
			}

			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("failed to extract file content: %s", err)
			}
			f.Close()
		}
	}

	return nil
}

// calculateSHA256 calculates SHA256 hash of a file
func calculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func dockerToGitLabPlatform(dockerPlatform string) string {
	switch {
	case strings.HasPrefix(dockerPlatform, "linux/arm/v5"):
		return "arm32v5"
	case strings.HasPrefix(dockerPlatform, "linux/arm/v6"):
		return "arm32v6"
	case strings.HasPrefix(dockerPlatform, "linux/arm/v7"):
		return "arm32v7"
	case strings.HasPrefix(dockerPlatform, "linux/arm64"):
		return "arm64v8"
	case strings.HasPrefix(dockerPlatform, "linux/arm"):
		return "arm32v6"
	case strings.HasPrefix(dockerPlatform, "linux/386"):
		return "386"
	case strings.HasPrefix(dockerPlatform, "linux/amd64"):
		return "amd64"
	case strings.HasPrefix(dockerPlatform, "linux/mips64le"):
		return "mips64le"
	case strings.HasPrefix(dockerPlatform, "linux/mipsle"):
		return "mipsle"
	case strings.HasPrefix(dockerPlatform, "linux/mips64"):
		return "mips64"
	case strings.HasPrefix(dockerPlatform, "linux/mips"):
		return "mips"
	case strings.HasPrefix(dockerPlatform, "linux/riscv64"):
		return "riscv64"
	default:
		return dockerPlatform
	}
}

// gitLabToDockerPlatform inverts the dockerToGitLabPlatform mapping.
func gitLabToDockerPlatform(gitLabPlatform string) string {
	switch gitLabPlatform {
	case "arm32v5":
		return "linux/arm/v5"
	case "arm32v6":
		// This can map to either "linux/arm/v6" or "linux/arm".
		// We prioritize the more specific variant.
		return "linux/arm/v6"
	case "arm32v7":
		return "linux/arm/v7"
	case "arm64v8":
		return "linux/arm64"
	case "386":
		return "linux/386"
	case "amd64":
		return "linux/amd64"
	case "mips64le":
		return "linux/mips64le"
	case "mipsle":
		return "linux/mipsle"
	case "mips64":
		return "linux/mips64"
	case "mips":
		return "linux/mips"
	case "riscv64":
		return "linux/riscv64"
	default:
		// If no specific mapping is found, return the input as is.
		return gitLabPlatform
	}
}

func GetGitlabPkgApp(p *Pvr, app *AppData) (string, error) {
	if app.From == "" {
		return "", ErrEmptyFrom
	}

	// Parse GitLab URL and version
	projectPath, version, err := parseGitLabURL(app.From)
	if err != nil {
		return "", err
	}

	// Fetch available packages
	packages, err := getGitLabPackages(p, projectPath)
	if err != nil {
		return "", err
	}

	// Convert platform name
	gitlabPlatform := dockerToGitLabPlatform(app.GetPlatform(p))

	// Find best matching package
	if version != "" && version != "latest" {
		fmt.Printf("Determining best matching package for version %s...\n", version)
	} else {
		fmt.Println("Determining best matching package (latest)...")
	}
	selectedPkg, err := findBestPackage(packages, version, gitlabPlatform)
	if err != nil {
		return "", err
	}

	// Create temporary directory for download
	tempDir, err := os.MkdirTemp("", "pvr-gitlabpkg-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %s", err)
	}

	// Download package
	downloadedPath, err := downloadPackage(p, selectedPkg, tempDir, gitlabPlatform, projectPath)
	if err != nil {
		return "", err
	}

	return downloadedPath, nil
}

// AddGitLabPkgApp adds an application from a GitLab package
func AddGitLabPkgApp(p *Pvr, app *AppData) error {
	downloadedPath, err := GetGitlabPkgApp(p, app)
	if err != nil {
		return err
	}
	defer os.Remove(downloadedPath)

	getUrl := p.Pvrconfig.DefaultGetUrl
	_, err = p.GetRepo(downloadedPath, true, true, nil)
	if err != nil {
		return err
	}

	p.Pvrconfig.DefaultGetUrl = getUrl
	err = p.SaveConfig()
	if err != nil {
		return err
	}

	return p.Reset(true)
}

// UpdateGitLabPkgApp updates an application from a GitLab package

func UpdateGitLabPkgApp(p *Pvr, app *AppData, appManifest *Source) error {
	if packageManifest, err := p.GetPackageManifest(app.Appname); err == nil {
		app.SourceType = models.SourceTypeGitLabPkg
		app.From = packageManifest.PackageURL
		app.Platform = gitLabToDockerPlatform(packageManifest.Arch)
	}
	return AddGitLabPkgApp(p, app)
}

// InstallGitLabPkgApp installs a GitLab package application
func InstallGitLabPkgApp(p *Pvr, app *AppData, appManifest *Source) error {
	// Nothing to do here. The user needs to run `pvr checkout` to get the files.
	return nil
}
