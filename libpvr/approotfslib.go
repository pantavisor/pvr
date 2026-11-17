//
// Copyright 2017-2023  Pantacor Ltd.
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
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"
	"github.com/pantavisor/pvr/utils/pvjson"
)

func UpdateRootFSApp(p *Pvr, app *AppData, appManifest *Source) error {
	appPath := filepath.Join(p.Dir, app.Appname)
	if _, err := os.Stat(appPath); os.IsNotExist(err) {
		return errors.New("application is not installed")
	}

	appManifest.RootFsURL = app.From

	persistence, err := GetPersistence(app)
	if err != nil {
		return err
	}
	app.Appmanifest.Persistence = persistence

	if app.ConfigFile != "" {
		if app.Appmanifest.DockerConfig == nil {
			app.Appmanifest.DockerConfig = map[string]interface{}{}
		}

		config, err := GetDockerConfigFile(p, app)
		if err != nil {
			return err
		}

		for k, v := range config {
			app.Appmanifest.DockerConfig[k] = v
		}
	}

	fromPath, err := GetFromRootFs(app)
	if err != nil {
		return err
	}

	app.DestinationPath = appPath

	if err = p.MakeSquash(fromPath, app); err != nil {
		return err
	}

	squashFilePath := filepath.Join(app.DestinationPath, app.SquashFile)
	digestFile := filepath.Join(app.DestinationPath, app.SquashFile+ROOTFS_DIGEST_SUFFIX)
	squashFile, err := os.Open(squashFilePath)
	if err != nil {
		return err
	}

	rootfsDigest, err := digest.FromReader(squashFile)
	if err != nil {
		return err
	}

	if err = os.WriteFile(digestFile, []byte(rootfsDigest), 0644); err != nil {
		return nil
	}

	app.Appmanifest.RootFsDigest = rootfsDigest.String()
	srcContent, err := pvjson.Marshal(app.Appmanifest, pvjson.MarshalOptions{Prefix: " ", Indent: " "})
	if err != nil {
		return err
	}

	srcFilePath := filepath.Join(appPath, SRC_FILE)
	if err = os.WriteFile(srcFilePath, srcContent, 0644); err != nil {
		return err
	}

	return nil
}

func InstallRootFsApp(p *Pvr, app *AppData, appManifest *Source) error {

	dockerConfig := map[string]interface{}{}
	if app.Appmanifest.DockerConfig != nil {
		dockerConfig = app.Appmanifest.DockerConfig
	}

	app.DestinationPath = filepath.Join(p.Dir, app.Appname)
	err := p.GenerateApplicationTemplateFiles(app.Appname, dockerConfig, appManifest)
	if err != nil {
		return err
	}

	return nil
}

func AddRootFsApp(p *Pvr, app *AppData) error {
	appPath := filepath.Join(p.Dir, app.Appname)

	from, err := GetFromRootFs(app)
	if err != nil {
		return err
	}

	persistence, err := GetPersistence(app)
	if err != nil {
		return err
	}

	config := map[string]interface{}{}
	if app.ConfigFile != "" {
		configFile, err := GetDockerConfigFile(p, app)
		if err != nil {
			return err
		}

		for k, v := range configFile {
			config[k] = v
		}
	}

	if app.Config != nil {
		for k, v := range app.Config {
			config[k] = v
		}
	}

	src := Source{
		Spec:         SRC_SPEC,
		Template:     TEMPLATE_BUILTIN_LXC_DOCKER,
		TemplateArgs: app.TemplateArgs,
		Config:       config,
		Persistence:  persistence,
		DockerSource: DockerSource{
			FormatOptions: app.FormatOptions,
		},
		RootFsSource: RootFsSource{
			RootFsURL: app.From,
		},
	}

	if app.ForceAdd {
		RemoveAll(appPath)
	}

	err = os.MkdirAll(appPath, 0777)
	if err != nil {
		return err
	}
	app.Appmanifest = &src
	app.DestinationPath = appPath

	if err = p.MakeSquash(from, app); err != nil {
		return err
	}

	squashFilePath := filepath.Join(app.DestinationPath, app.SquashFile)
	digestFile := filepath.Join(app.DestinationPath, app.SquashFile+ROOTFS_DIGEST_SUFFIX)
	squashFile, err := os.Open(squashFilePath)
	if err != nil {
		return err
	}

	rootfsDigest, err := digest.FromReader(squashFile)
	if err != nil {
		return err
	}

	if err = os.WriteFile(digestFile, []byte(rootfsDigest), 0644); err != nil {
		return nil
	}

	src.RootFsDigest = rootfsDigest.String()
	srcContent, err := pvjson.Marshal(src, pvjson.MarshalOptions{Prefix: " ", Indent: " "})
	if err != nil {
		return err
	}

	srcFilePath := filepath.Join(appPath, SRC_FILE)
	if err = os.WriteFile(srcFilePath, srcContent, 0644); err != nil {
		return err
	}

	app.Appmanifest = &src

	return err
}

func GetFromRootFs(app *AppData) (string, error) {
	rootfsPath := app.From

	uri, err := url.Parse(rootfsPath)
	if err != nil {
		return "", err
	}

	var from, downloadDir string
	if uri.Scheme != "" {
		fmt.Printf("Downloading file: %s", app.From)
		from, err = DownloadFile(uri)
		if err != nil {
			return "", err
		}
		// DownloadFile returns the downloaded file inside a private
		// temporary directory the caller owns.
		downloadDir = filepath.Dir(from)
	} else {
		expanded, err := ExpandPath(rootfsPath)
		if err != nil {
			return "", err
		}
		// A local rootfs is the pvr user's own file, wherever it is; the
		// builder never lets package metadata choose a rootfs source.
		from = expanded
	}

	fileInfo, err := os.Stat(from)
	if err != nil {
		if downloadDir != "" {
			os.RemoveAll(downloadDir)
		}
		return "", err
	}

	if !fileInfo.IsDir() {
		tempdir, err := os.MkdirTemp(os.TempDir(), "rootfs-")
		if err != nil {
			if downloadDir != "" {
				os.RemoveAll(downloadDir)
			}
			return "", err
		}

		if err = Untar(tempdir, from, []string{}); err != nil {
			os.RemoveAll(tempdir)
			if downloadDir != "" {
				os.RemoveAll(downloadDir)
			}
			return "", err
		}

		if downloadDir != "" {
			os.RemoveAll(downloadDir)
		}
		from = tempdir
	}

	return from, nil
}
