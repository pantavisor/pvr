// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package artifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli"
)

const kindSDK = "sdk"

// sdkFlags are the image details that go into the entry's meta.
var sdkFlags = []string{"machine", "target", "platform", "image", "build-id"}

// sdkDescriptor is the part of an SDK descriptor (<name>.sdk.json, written by the
// build) that fills the publish flags. Other fields are ignored.
type sdkDescriptor struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Machine     string `json:"machine"`
	Target      string `json:"target"`
	Platform    string `json:"platform"`
	Image       string `json:"image"`
	BuildID     string `json:"build_id"`
	Description string `json:"description"`
	File        string `json:"file"`
}

// readDescriptor reads the descriptor and returns it with the installer's path,
// which is its "file" next to the descriptor.
func readDescriptor(path string) (sdkDescriptor, string, error) {
	var d sdkDescriptor
	data, err := os.ReadFile(path)
	if err != nil {
		return d, "", fmt.Errorf("cannot read the SDK descriptor %s: %v", path, err)
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return d, "", fmt.Errorf("the SDK descriptor %s is not valid JSON: %v", path, err)
	}
	if d.File == "" {
		return d, "", fmt.Errorf("the SDK descriptor %s has no \"file\" (the installer's name)", path)
	}
	installer := d.File
	if !filepath.IsAbs(installer) {
		installer = filepath.Join(filepath.Dir(path), installer)
	}
	if _, err := os.Stat(installer); err != nil {
		return d, "", fmt.Errorf("the installer %s named by %s was not found", installer, path)
	}
	return d, installer, nil
}

func sdkPublish(c *cli.Context) error {
	if c.NArg() != 1 {
		return errors.New("publish takes one installer file or SDK descriptor (.json)")
	}
	meta, err := parseMeta(c.StringSlice("meta"))
	if err != nil {
		return err
	}
	file := c.Args().First()
	var d sdkDescriptor
	if strings.HasSuffix(strings.ToLower(file), ".json") {
		if d, file, err = readDescriptor(file); err != nil {
			return err
		}
	}
	// a flag on the command line wins over the descriptor
	pick := func(flag, fromFile string) string {
		if v := c.String(flag); v != "" {
			return v
		}
		return fromFile
	}
	values := map[string]string{"machine": d.Machine, "target": d.Target, "platform": d.Platform,
		"image": d.Image, "build-id": d.BuildID}
	for _, f := range sdkFlags {
		if v := pick(f, values[f]); v != "" {
			if meta == nil {
				meta = map[string]interface{}{}
			}
			// Hub metadata keys use underscores.
			key := f
			if f == "build-id" {
				key = "build_id"
			}
			meta[key] = v
		}
	}
	return publish(c, file, Entry{Kind: kindSDK, Name: pick("name", d.Name),
		Version: pick("version", d.Version), Description: pick("description", d.Description), Meta: meta})
}

func sdkLs(c *cli.Context) error {
	return ls(c, kindSDK, []string{"name", "version", "machine", "target", "size"})
}

func sdkRm(c *cli.Context) error {
	if c.NArg() != 1 {
		return errors.New("rm takes an id or name@version")
	}
	ref := c.Args().First()
	if idPattern.MatchString(ref) {
		return rm(c, "", ref, "")
	}
	kind, name, version, err := splitRef(ref, kindSDK)
	if err != nil {
		return err
	}
	return rm(c, kind, name, version)
}

// CommandSDK is the short form of the artifact commands for kind "sdk".
func CommandSDK() cli.Command {
	jsonFlag := cli.BoolFlag{Name: "json", Usage: "print JSON"}
	return cli.Command{Name: "sdk", Usage: "publish the SDK of your image to your Hub account",
		Description: "e.g. `pvr sdk publish tmp/deploy/sdk/acme-image-qemux86-64.sdk.json` (the descriptor the build writes next to the installer)\n   or `pvr sdk publish pv-sdk-installer.sh --name acme-image --version 1.0 --machine qemux86-64`",
		Subcommands: []cli.Command{
			{Name: "publish", ArgsUsage: "<installer|descriptor.json>", Usage: "upload an SDK installer (or the one named by its .json descriptor) and register it", Action: sdkPublish, Flags: []cli.Flag{
				cli.StringFlag{Name: "name", Usage: "SDK name"},
				cli.StringFlag{Name: "version", Usage: "SDK version"},
				cli.StringFlag{Name: "machine", Usage: "machine the image is built for"},
				cli.StringFlag{Name: "target", Usage: "target architecture of the SDK"},
				cli.StringFlag{Name: "platform", Usage: "platform the SDK runs on"},
				cli.StringFlag{Name: "image", Usage: "image the SDK belongs to"},
				cli.StringFlag{Name: "build-id", Usage: "Hub build the SDK came from"},
				cli.StringFlag{Name: "description", Usage: "what it is"},
				cli.StringSliceFlag{Name: "meta", Usage: "extra key=value to store with the entry (repeatable)"},
				jsonFlag,
			}},
			{Name: "ls", Usage: "list your published SDKs", Action: sdkLs, Flags: []cli.Flag{
				cli.StringFlag{Name: "name", Usage: "only this name"},
				jsonFlag,
			}},
			{Name: "rm", ArgsUsage: "<id|name@version>", Usage: "remove a published SDK", Action: sdkRm},
		}}
}
