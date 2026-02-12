//go:build go1.24

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
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	dockerclient "github.com/docker/docker/client"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"gitlab.com/pantacor/pvr/utils/pvjson"
)

const (
	MAKE_SQUASHFS_CMD              = "mksquashfs"
	SQUASH_FILE                    = "root.squashfs"
	SQUASH_OVL_FILE                = "root.ovl.squashfs"
	DOCKER_DIGEST_SUFFIX           = ".docker-digest"
	ROOTFS_DIGEST_SUFFIX           = ".rootfs-digest"
	DOCKER_DOMAIN                  = "docker.io"
	DOCKER_DOMAIN_URL              = "https://" + DOCKER_DOMAIN
	DOCKER_REGISTRY                = "https://index.docker.io/v1/"
	DOCKER_REGISTRY_SERVER_ADDRESS = "https://registry-1.docker.io"
)

var (
	ErrMakeSquashFSNotFound    = errors.New("mksquashfs not found in your PATH, please install before continue")
	ErrTarNotFound             = errors.New("tar not found in your PATH, please install before continue")
	ErrImageNotFound           = errors.New("image not found or you do not have access")
	ErrDownloadedLayerDiffSize = errors.New("size of downloaded layer is different from expected")
	stripFilesList             = []string{
		"usr/bin/qemu-arm-static",
	}
	structureDirs = []string{
		"proc",
		"sys",
		"dev",
		"tmp",
		"var/tmp",
		"run",
		"var/run",
	}
)

type DockerManifest map[string]interface{}

// Manifest represents a Docker/OCI image manifest
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType,omitempty"`
	Config        Descriptor        `json:"config"`
	Layers        []Descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// Descriptor represents a content descriptor
type Descriptor struct {
	MediaType string `json:"mediaType,omitempty"`
	Size      int64  `json:"size"`
	Digest    string `json:"digest"`
}

// DockerImage : return type of ImageExistsInLocalDocker()
type DockerImage struct {
	Exists         bool
	DockerDigest   string
	DockerConfig   map[string]interface{}
	DockerManifest *Manifest
	ImageRef       name.Reference
	ImagePath      string
	DockerPlatform string
}

// getDaemonOptions returns daemon options with a Docker client configured
// for the local Docker daemon's API version.
func getDaemonOptions() []daemon.Option {
	ctx := context.Background()
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
	)
	if err != nil {
		return nil
	}

	// Manual version negotiation: Docker client v24's NegotiateAPIVersion()
	// only DOWNGRADES from DefaultVersion (1.43), never upgrades.
	// Docker 28+ requires minimum API version 1.44, so we ping the daemon
	// directly and adopt its reported API version.
	ping, err := cli.Ping(ctx)
	if err == nil && ping.APIVersion != "" {
		_ = dockerclient.WithVersion(ping.APIVersion)(cli)
	}

	return []daemon.Option{daemon.WithClient(cli)}
}

// GetRemoteImage fetches a remote image using go-containerregistry
func (p *Pvr) GetRemoteImage(imageRef string, auth authn.Authenticator) (v1.Image, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, err
	}

	// If anonymous auth, use default keychain for docker config.json credentials
	if auth == authn.Anonymous {
		return remote.Image(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(context.Background()))
	}

	return remote.Image(ref, remote.WithAuth(auth), remote.WithContext(context.Background()))
}

// GetRemoteImageForPlatform fetches a remote image for a specific platform
func (p *Pvr) GetRemoteImageForPlatform(imageRef string, platform v1.Platform, auth authn.Authenticator) (v1.Image, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, err
	}

	// If anonymous auth, use default keychain for docker config.json credentials
	if auth == authn.Anonymous {
		return remote.Image(ref,
			remote.WithAuthFromKeychain(authn.DefaultKeychain),
			remote.WithPlatform(platform),
			remote.WithContext(context.Background()),
		)
	}

	return remote.Image(ref,
		remote.WithAuth(auth),
		remote.WithPlatform(platform),
		remote.WithContext(context.Background()),
	)
}

// GetRemoteIndex fetches a remote multi-platform image index
func (p *Pvr) GetRemoteIndex(imageRef string, auth authn.Authenticator) (v1.ImageIndex, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, err
	}

	// If anonymous auth, use default keychain for docker config.json credentials
	if auth == authn.Anonymous {
		return remote.Index(ref, remote.WithAuthFromKeychain(authn.DefaultKeychain), remote.WithContext(context.Background()))
	}

	return remote.Index(ref, remote.WithAuth(auth), remote.WithContext(context.Background()))
}

// GetImageManifest extracts the manifest from a v1.Image
func GetImageManifest(img v1.Image) (*Manifest, error) {
	rawManifest, err := img.Manifest()
	if err != nil {
		return nil, err
	}

	manifest := &Manifest{
		SchemaVersion: int(rawManifest.SchemaVersion),
		MediaType:     string(rawManifest.MediaType),
		Config: Descriptor{
			MediaType: string(rawManifest.Config.MediaType),
			Size:      rawManifest.Config.Size,
			Digest:    rawManifest.Config.Digest.String(),
		},
		Layers:      make([]Descriptor, len(rawManifest.Layers)),
		Annotations: rawManifest.Annotations,
	}

	for i, layer := range rawManifest.Layers {
		manifest.Layers[i] = Descriptor{
			MediaType: string(layer.MediaType),
			Size:      layer.Size,
			Digest:    layer.Digest.String(),
		}
	}

	return manifest, nil
}

// GetImageConfig extracts the config from a v1.Image
func GetImageConfig(img v1.Image) (map[string]interface{}, error) {
	configFile, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}

	// Convert ConfigFile.Config to map
	configData, err := json.Marshal(configFile.Config)
	if err != nil {
		return nil, err
	}

	var config map[string]interface{}
	err = pvjson.Unmarshal(configData, &config)
	if err != nil {
		return nil, err
	}

	return config, nil
}

// GetImageDigest returns the digest of a v1.Image as a string
func GetImageDigest(img v1.Image) (string, error) {
	digest, err := img.Digest()
	if err != nil {
		return "", err
	}
	return digest.String(), nil
}

// DownloadLayersFromLocalDocker : Download Layers From Local Docker using go-containerregistry
func DownloadLayersFromLocalDocker(imageRef string) (io.ReadCloser, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, err
	}

	img, err := daemon.Image(ref, getDaemonOptions()...)
	if err != nil {
		return nil, err
	}

	// Export the image as a tar archive using tarball.Write
	pr, pw := io.Pipe()

	go func() {
		err := tarball.Write(ref, img, pw)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.Close()
	}()

	return pr, nil
}

// ExportLocalImage exports a local Docker image as a tar stream
func ExportLocalImage(imageRef string) (io.ReadCloser, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return nil, err
	}

	img, err := daemon.Image(ref, getDaemonOptions()...)
	if err != nil {
		return nil, err
	}

	// Export the image as a tar archive using tarball.Write
	pr, pw := io.Pipe()

	go func() {
		err := tarball.Write(ref, img, pw)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.Close()
	}()

	return pr, nil
}

// FindDockerImage : Find Docker Image
func (p *Pvr) FindDockerImage(app *AppData) (err error) {
	app.LocalImage.Exists = false
	app.RemoteImage.Exists = false

	sourceOrder := strings.Split(app.Source, ",")
	for _, source := range sourceOrder {
		switch source {
		case "local":
			err = LoadLocalImage(app)
			if app.LocalImage.Exists {
				return nil
			}
		case "remote":
			err = p.LoadRemoteImage(app)
			if app.RemoteImage.Exists {
				return nil
			}
		default:
			return errors.New("source type not supported:" + source + "\n")
		}

		if err != nil {
			fmt.Printf("%s source had an error, trying with other sources \n %s \n", source, err)
		}
	}

	if err != nil {
		return err
	}

	return errors.New("Image not found in source:" + app.Source + "\n")
}

// LoadRemoteImage : To check whether Image Exist In Remote Docker Or Not
func (p *Pvr) LoadRemoteImage(app *AppData) error {
	app.RemoteImage = DockerImage{
		Exists: false,
	}

	auth := p.AuthConfig(app.Username, app.Password, "")
	ref, err := name.ParseReference(app.From)
	if err != nil {
		return err
	}

	var img v1.Image
	var repoDigest string
	var dockerPlatform string
	var platforms []interface{}

	if app.Platform == "" {
		dockerJsonI, ok := p.PristineJsonMap["_hostconfig/pvr/docker.json"]
		if ok {
			dockerJson := dockerJsonI.(map[string]interface{})
			platformsI, ok := dockerJson["platforms"]
			if ok {
				platforms = platformsI.([]interface{})
			}
		}
	} else {
		platforms = append(platforms, app.Platform)
	}

	// Try to get image for specific platform if platforms are specified
	if platforms != nil {
		// First try to get the image index to check if it's a multi-platform image
		idx, err := p.GetRemoteIndex(app.From, auth)
		if err == nil {
			indexManifest, err := idx.IndexManifest()
			if err == nil {
				for _, manifest := range indexManifest.Manifests {
					if manifest.Platform == nil {
						continue
					}
					for _, platformStr := range platforms {
						plat := platformStr.(string)
						parts := strings.SplitN(plat, "/", 3)
						if len(parts) >= 2 &&
							manifest.Platform.Architecture == parts[1] &&
							(len(parts) < 3 || parts[2] == manifest.Platform.Variant) {
							repoDigest = manifest.Digest.String()
							app.ManifestType = string(manifest.MediaType)
							dockerPlatform = plat

							// Get the specific image for this platform
							img, err = idx.Image(manifest.Digest)
							if err != nil {
								continue
							}

							fmt.Fprintf(os.Stderr, "Found Manifest for platform %s\n", dockerPlatform)
							break
						}
					}
					if img != nil {
						break
					}
				}
			}
		}
	}

	// If no platform-specific image found, get the default image
	if img == nil {
		img, err = p.GetRemoteImage(app.From, auth)
		if err != nil {
			manifestErr := ReportDockerManifestError(err, app.From)
			if strings.Contains(err.Error(), "MANIFEST_UNKNOWN") ||
				strings.Contains(err.Error(), "NAME_UNKNOWN") ||
				strings.Contains(err.Error(), "not found") {
				fmt.Fprintf(os.Stderr, "%s\n", manifestErr.Error())
				return nil
			}
			return manifestErr
		}
		fmt.Fprintf(os.Stderr, "Found Manifest for default platform.\n")
	}

	// Get manifest
	dockerManifest, err := GetImageManifest(img)
	if err != nil {
		err = ReportDockerManifestError(err, app.From)
		return err
	}

	// Get config
	dockerConfig, err := GetImageConfig(img)
	if err != nil {
		err = ReportDockerManifestError(err, app.From)
		return err
	}

	// Get digest if not already set from multi-platform lookup
	if repoDigest == "" {
		digest, err := img.Digest()
		if err != nil {
			return err
		}
		repoDigest = digest.String()
	}

	// Build the full repo digest with image name
	li := strings.LastIndex(app.From, ":")
	var imageName string
	if li < 0 {
		imageName = app.From
	} else {
		splits := []string{app.From[:li], app.From[li+1:]}
		imageName = splits[0]
	}

	// Extract image name from repo digest
	if strings.Contains(imageName, "@sha256") {
		splits := strings.Split(imageName, "@")
		imageName = splits[0]
	}

	if !strings.Contains(repoDigest, "@") {
		repoDigest = imageName + "@" + repoDigest
	}

	if dockerPlatform == "" {
		configFile, err := img.ConfigFile()
		if err == nil {
			dockerPlatform = fmt.Sprintf("%s/%s", configFile.OS, configFile.Architecture)
		}
	}

	app.RemoteImage.Exists = true
	app.RemoteImage.DockerDigest = repoDigest
	app.RemoteImage.DockerConfig = dockerConfig
	app.RemoteImage.DockerManifest = dockerManifest
	app.RemoteImage.ImageRef = ref
	app.RemoteImage.DockerPlatform = dockerPlatform
	app.RemoteImage.ImagePath = ref.Context().RepositoryStr()

	return nil
}

// LoadLocalImage : To check whether Image Exist In Local Docker Or Not
func LoadLocalImage(app *AppData) error {
	app.LocalImage = DockerImage{
		Exists: false,
	}

	ref, err := name.ParseReference(app.From)
	if err != nil {
		return err
	}

	img, err := daemon.Image(ref, getDaemonOptions()...)
	if err != nil {
		if strings.Contains(err.Error(), "not found") ||
			strings.Contains(err.Error(), "No such image") {
			fmt.Printf("Repo not exists in local docker\n")
			return nil
		}
		return err
	}

	fmt.Printf("Repo exists in local docker\n")

	// Get the digest
	digest, err := img.Digest()
	if err != nil {
		// Fall back to config digest if image digest not available
		configName, err := img.ConfigName()
		if err != nil {
			return err
		}
		app.LocalImage.DockerDigest = configName.String()
	} else {
		// Try to get repo digest format (image@sha256:...)
		app.LocalImage.DockerDigest = ref.Context().String() + "@" + digest.String()
	}

	// Get Docker Config values
	dockerConfig, err := GetImageConfig(img)
	if err != nil {
		return err
	}
	app.LocalImage.DockerConfig = dockerConfig
	app.LocalImage.ImageRef = ref

	configFile, err := img.ConfigFile()
	if err == nil {
		app.LocalImage.DockerPlatform = fmt.Sprintf("%s/%s", configFile.OS, configFile.Architecture)
	}

	app.LocalImage.Exists = true
	return nil
}

// AppData : To hold all required App Information
type AppData struct {
	SquashFile      string
	Appname         string
	DockerURL       string
	Username        string
	Password        string
	Appmanifest     *Source
	TemplateArgs    map[string]interface{}
	Config          map[string]interface{}
	DestinationPath string
	LocalImage      DockerImage
	RemoteImage     DockerImage
	From            string
	Source          string
	Platform        string
	ConfigFile      string
	Volumes         []string
	FormatOptions   string
	SourceType      string
	DoOverlay       bool
	Base            string
	ManifestType    string
	ForceAdd        bool
}

func (data *AppData) GetPlatform(p *Pvr) string {
	if data.Platform != "" {
		return data.Platform
	}

	if p.Platform != "" {
		return p.Platform
	}

	if dockerJsonRaw, ok := p.PristineJsonMap["_hostconfig/pvr/docker.json"]; ok {
		if dockerJson, ok := dockerJsonRaw.(map[string]interface{}); ok {
			if platformsRaw, ok := dockerJson["platforms"]; ok {
				if platformsList, ok := platformsRaw.([]interface{}); ok && len(platformsList) > 0 {
					if platformStr, ok := platformsList[0].(string); ok {
						// Found a platform in the config, use it.
						return platformStr
					}
				}
			}
		}
	}

	return fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
}

func (p *Pvr) GenerateApplicationSquashFS(app *AppData, appManifest *Source) error {
	digestFile := filepath.Join(app.DestinationPath, app.SquashFile+DOCKER_DIGEST_SUFFIX)
	digest := ""
	//	Exists flag is true only if the image got loaded which will depend on
	//  priority order provided in --source=local,remote
	if app.LocalImage.Exists {
		digest = app.LocalImage.DockerDigest
	} else if app.RemoteImage.Exists {
		digest = app.RemoteImage.DockerDigest
	}

	currentDigest, err := os.Open(digestFile)
	if err == nil {
		currentDigestContent, err := io.ReadAll(currentDigest)
		defer currentDigest.Close()
		if err == nil {
			if string(currentDigestContent) == string(digest) && appManifest.Base == "" {
				return nil
			}
		}
	}

	configDir := p.Session.configDir
	cacheDir := filepath.Join(configDir, cacheFolder)
	err = CreateFolder(cacheDir)

	if err != nil {
		return fmt.Errorf("couldn't create cache folder %v", err)
	}

	fmt.Println("Drafting squashfs...")

	tempdir, err := ExecCommandOut(MKTEMP_CMD, "-d", "-t", "download-layer-XXXXXXXX")
	if err != nil {
		return err
	}

	defer RemoveAll(tempdir)

	files := []string{}
	fmt.Println("Downloading layers...")
	//	Exists flag is true only if the image got loaded which will depend on
	//  priority order provided in --source=local,remote
	if app.LocalImage.Exists {
		dockerdigest := app.LocalImage.DockerDigest
		splited := strings.Split(app.LocalImage.DockerDigest, "@")
		if len(splited) >= 2 {
			dockerdigest = splited[1]
		}
		filename := filepath.Join(cacheDir, dockerdigest) + ".tar.gz"
		fileExistInCache, err := IsFileExists(filename)
		if err != nil {
			return err
		}
		if fileExistInCache {
			fmt.Println("Layers Found in Cache")
			fmt.Printf("Extracting layers folder(cache)\n")
		} else {
			fmt.Println("Layers Not Found in Cache")
			fmt.Println("Downloading layers from local docker")
			// Use ImageRef (tag-based) for export as local Docker can't resolve by digest
			imageReader, err := ExportLocalImage(app.LocalImage.ImageRef.String())
			if err != nil {
				return err
			}
			buf := bufio.NewReader(imageReader)
			err = Create(filename, 0644)
			if err != nil {
				return err
			}
			file, err := os.OpenFile(filename, os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			_, err = buf.WriteTo(file)
			if err != nil {
				return err
			}
			fmt.Printf("Layers downloaded from local docker\n")
			fmt.Printf("Extracting layers folder\n")
		}

		MkdirAll(tempdir+"/layers", 0777)
		err = Untar(tempdir+"/layers", filename, []string{"--exclude", ".wh.*"})
		if err != nil {
			return err
		}
		// Read layer.tar file locations from manifest.json
		manifestFile, err := os.ReadFile(tempdir + "/layers/manifest.json")
		if err != nil {
			return err
		}
		manifestData := []map[string]interface{}{}
		err = pvjson.Unmarshal([]byte(manifestFile), &manifestData)
		if err != nil {
			return err
		}
		//Populate layer.tar file locations in files array
		for _, layer := range manifestData[0]["Layers"].([]interface{}) {
			filename := filepath.Join(tempdir, "layers") + "/" + layer.(string)
			files = append(files, filename)
		}

	} else if app.RemoteImage.Exists {
		// Download layers from remote using go-containerregistry
		auth := p.AuthConfig(app.Username, app.Password, "")
		img, err := p.GetRemoteImage(app.From, auth)
		if err != nil {
			return err
		}

		layers, err := img.Layers()
		if err != nil {
			return err
		}

		for i, layer := range layers {
			layerDigest, err := layer.Digest()
			if err != nil {
				return err
			}

			filename := filepath.Join(cacheDir, layerDigest.String()) + ".tar.gz"
			shaValid, err := FileHasSameSha(filename, layerDigest.String())
			if err != nil {
				return err
			}
			if shaValid {
				fmt.Printf("Layer %d downloaded(cache)\n", i)
				files = append(files, filename)
				continue
			}

			// Download compressed layer
			layerReader, err := layer.Compressed()
			if err != nil {
				return err
			}

			buf := bufio.NewReader(layerReader)

			err = Create(filename, 0644)
			if err != nil {
				return err
			}
			file, err := os.OpenFile(filename, os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}

			layerSize, err := layer.Size()
			if err != nil {
				return err
			}

			writedCount, err := buf.WriteTo(file)
			if writedCount != layerSize {
				return ErrDownloadedLayerDiffSize
			}

			if err != nil {
				return err
			}
			files = append(files, filename)
			fmt.Printf("Layer %d downloaded\n", i)
		}
	}

	extractPath := filepath.Join(tempdir, "rootfs")
	MkdirAll(extractPath, 0777)
	defer RemoveAll(extractPath)

	tarPath, err := exec.LookPath(TAR_CMD)
	if err != nil {
		return err
	}

	if tarPath == "" {
		return ErrTarNotFound
	}

	fmt.Println("Extracting layers...")
	for layerNumber, file := range files {
		err := ProcessWhiteouts(extractPath, file, layerNumber)
		if err != nil {
			log.Println("Error processing whiteouts.")
			return err
		}
		err = Untar(extractPath, file, []string{"--exclude", ".wh.*"})
		if err != nil {
			return err
		}
		fmt.Printf("Extracting layer %d\n", layerNumber)
	}

	fmt.Println("Stripping qemu files...")
	for _, file := range stripFilesList {
		fileToDelete := filepath.Join(extractPath, file)
		Remove(fileToDelete)
		if err != nil {
			return err
		}

		PrintDebugf("Deleted %s file\n", fileToDelete)
	}

	fmt.Println("Adding essential structural dirs to operate RO containers")
	for _, file := range structureDirs {
		dirToMake := filepath.Join(extractPath, file)
		err = ExecCommand(MKDIR_CMD, "-p", dirToMake)
		if err != nil {
			_, err2 := os.Lstat(dirToMake)
			if err2 != nil {
				return err
			}
		}
	}

	// if we are using a different name, we generate an overlay
	if app.SquashFile == SQUASH_OVL_FILE {
		basePath, err := ExecCommandOut(MKTEMP_CMD, "-d", "-t", "base-layer-XXXXXXXX")
		if err != nil {
			return err
		}
		defer RemoveAll(basePath)
		ovlPath, err := ExecCommandOut(MKTEMP_CMD, "-d", "-t", "ovl-XXXXXXXX")
		if err != nil {
			return err
		}
		defer RemoveAll(ovlPath)
		from := path.Join(p.Dir, app.Appname, SQUASH_FILE)
		if appManifest.Base != "" {
			from = path.Join(p.Dir, appManifest.Base, SQUASH_FILE)
		}
		err = ExecCommand("unsquashfs", "-f", "-d", basePath, from)
		if err != nil {
			return err
		}
		treeDiff := MkTreeDiff(basePath, extractPath)
		treeDiff.MkOvl(ovlPath)
		extractPath = ovlPath
	} else if app.SquashFile != SQUASH_FILE {
		return errors.New("Unsupported SquashFile: " + app.SquashFile + ". Supported: " + SQUASH_FILE + ", " + SQUASH_OVL_FILE)
	}

	app.Appmanifest = appManifest
	err = p.MakeSquash(extractPath, app)
	if err != nil {
		return err
	}
	Create(digestFile, 0644)
	currentDigest, err = os.OpenFile(digestFile, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer currentDigest.Close()

	_, err = currentDigest.Write([]byte(digest))
	return err
}

// ProcessWhiteouts : FInd Whiteouts from a layer and process it in a given extract path
func ProcessWhiteouts(extractPath string, layerPath string, layerNumber int) error {
	whiteouts, err := FindWhiteoutsFromLayer(layerPath)
	if err != nil {
		return err
	}
	if len(whiteouts) == 0 {
		return nil
	}
	PrintDebugf("Processing Whiteouts from layer %d:%s\n", layerNumber, layerPath)
	for _, whiteoutFile := range whiteouts {

		basename := filepath.Base(whiteoutFile)
		dir := filepath.Join(extractPath, filepath.Dir(whiteoutFile))

		if strings.HasPrefix(basename, ".wh.") && strings.HasSuffix(basename, "opq") {
			//Clear all contents of the folder from the extract path
			PrintDebugln("Processing 'opq' whiteout for:" + dir)
			err := RemoveDirContents(dir)
			if err != nil && !os.IsNotExist(err) {
				fmt.Printf("WARNING: cannot process 'opq' whiteout %s (err=%s)\n", whiteoutFile, err.Error())
				continue
			}

		} else if strings.HasPrefix(basename, ".wh.") {
			//Remove the indicated file from the extract path'
			filePath := filepath.Join(dir, strings.TrimPrefix(basename, ".wh."))
			PrintDebugln("Processing whiteout:" + filePath)
			err := RemoveAll(filePath) //removr a file / dir
			if err != nil && !os.IsNotExist(err) {
				fmt.Printf("WARNING: cannot process whiteout %s (err=%s)\n", whiteoutFile, err.Error())
				continue
			}
		}
	}
	return nil
}

// FindWhiteoutsFromLayer : Find Whiteout Files From a Layer
func FindWhiteoutsFromLayer(layerPath string) ([]string, error) {
	whiteoutPaths := []string{}
	tarFile, err := os.Open(layerPath)
	if err != nil {
		return whiteoutPaths, err
	}
	contentType, err := GetFileContentType(layerPath)
	if err != nil {
		return whiteoutPaths, err
	}

	tr := tar.NewReader(tarFile)

	if contentType == "application/x-gzip" {
		// For zip content types, eg: application/x-gzip
		gzr, err := gzip.NewReader(tarFile)
		if err != nil {
			return whiteoutPaths, err
		}
		defer gzr.Close()
		tr = tar.NewReader(gzr)
	}

	for {
		path, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return whiteoutPaths, err
		}
		basename := filepath.Base(path.Name)
		if strings.HasPrefix(basename, ".wh.") {
			if !SliceContainsItem(whiteoutPaths, path.Name) {
				whiteoutPaths = append(whiteoutPaths, path.Name)
			}
		}
	}
	return whiteoutPaths, nil
}

func (p *Pvr) GetSquashFSDigest(squashFile, appName string) (string, error) {
	content, err := os.ReadFile(filepath.Join(p.Dir, appName, squashFile+DOCKER_DIGEST_SUFFIX))
	if os.IsNotExist(err) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	return string(content), nil
}

// AuthConfig returns an authenticator for registry access
func (p *Pvr) AuthConfig(username, password, registry string) authn.Authenticator {
	if username != "" && password != "" {
		return authn.FromConfig(authn.AuthConfig{
			Username: username,
			Password: password,
		})
	}
	// Fall back to anonymous authentication
	// For authenticated access from ~/.docker/config.json, use remote.WithAuthFromKeychain(authn.DefaultKeychain)
	return authn.Anonymous
}

// IsFileExists : Check if File Exists  or not
func IsFileExists(filePath string) (bool, error) {
	//Check if file exists in cache
	_, err := os.Stat(filePath)

	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}
