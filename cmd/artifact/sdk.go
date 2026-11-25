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

// sdkDescriptor is the part of a descriptor (<name>.sdk.json, written by the
// build) that fills the publish flags. Other fields are ignored.
//
// A descriptor of an image has "publish", the descriptors of what belongs to the
// image, each as a path next to it: publishing the image's descriptor publishes
// them all, with the image's version (see publishBundle).
type sdkDescriptor struct {
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Kind        string                 `json:"kind"`
	Machine     string                 `json:"machine"`
	Target      string                 `json:"target"`
	Platform    string                 `json:"platform"`
	Image       string                 `json:"image"`
	BuildID     string                 `json:"build_id"`
	Description string                 `json:"description"`
	File        string                 `json:"file"`
	SHA256      string                 `json:"sha256"`
	Size        int64                  `json:"size"`
	Meta        map[string]interface{} `json:"meta"`
	Publish     []string               `json:"publish"`
}

// readDescriptor reads the descriptor and returns it with the file's path,
// which is its "file" next to the descriptor. A sha256 or size in the
// descriptor has to match the file.
func readDescriptor(path string) (sdkDescriptor, string, error) {
	var d sdkDescriptor
	data, err := os.ReadFile(path)
	if err != nil {
		return d, "", fmt.Errorf("cannot read the SDK descriptor %s: %v", path, err)
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return d, "", fmt.Errorf("the SDK descriptor %s is not valid JSON: %v", path, err)
	}
	if len(d.Publish) > 0 {
		return d, "", nil
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
	if d.SHA256 != "" || d.Size != 0 {
		sha, size, err := hashFile(installer)
		if err != nil {
			return d, "", err
		}
		if (d.SHA256 != "" && sha != d.SHA256) || (d.Size != 0 && size != d.Size) {
			return d, "", fmt.Errorf("%s does not match %s (sha256 %s, expected %s): it is not the file that was built; build again", installer, path, sha, d.SHA256)
		}
	}
	return d, installer, nil
}

// publishBundle publishes what the descriptor of an image lists. Everything is
// checked before anything is uploaded: each file has to exist and match its
// sha256, have the image's name and version, and not be published yet.
func publishBundle(c *cli.Context, path string, image sdkDescriptor) error {
	for _, f := range []string{"name", "version", "machine", "target", "platform", "image", "build-id", "description"} {
		if c.String(f) != "" {
			return fmt.Errorf("--%s cannot be used with %s: it lists what to publish and takes the version of the image", f, path)
		}
	}
	meta, err := parseMeta(c.StringSlice("meta"))
	if err != nil {
		return err
	}
	type item struct {
		d    sdkDescriptor
		file string
	}
	var items []item
	for _, rel := range image.Publish {
		p := rel
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(path), p)
		}
		d, file, err := readDescriptor(p)
		if err != nil {
			return err
		}
		if len(d.Publish) > 0 {
			return fmt.Errorf("%s lists more descriptors; only the image's descriptor may", p)
		}
		if d.Version != image.Version || d.Name != image.Name {
			return fmt.Errorf("%s is %s@%s but %s is %s@%s: they are from different builds; build both in one run", p, d.Name, d.Version, path, image.Name, image.Version)
		}
		items = append(items, item{d, file})
	}
	h, err := newClient(c)
	if err != nil {
		return err
	}
	for _, it := range items {
		kind := it.d.Kind
		if kind == "" {
			kind = kindSDK
		}
		list, err := h.list(kind, it.d.Name)
		if err != nil {
			return err
		}
		for _, e := range list {
			if e.Version == it.d.Version {
				return fmt.Errorf("%s/%s@%s is already published; remove it first or build again", kind, it.d.Name, it.d.Version)
			}
		}
	}
	for _, it := range items {
		if err := publish(c, it.file, entryOf(it.d, meta, func(_, v string) string { return v })); err != nil {
			return err
		}
	}
	return nil
}

// entryOf makes the entry a descriptor describes. pick chooses between a flag
// and the descriptor's value.
func entryOf(d sdkDescriptor, meta map[string]interface{}, pick func(flag, fromFile string) string) Entry {
	values := map[string]string{"machine": d.Machine, "target": d.Target, "platform": d.Platform,
		"image": d.Image, "build-id": d.BuildID}
	merged := map[string]interface{}{}
	for k, v := range d.Meta {
		merged[k] = v
	}
	for k, v := range meta {
		merged[k] = v
	}
	for _, f := range sdkFlags {
		if v := pick(f, values[f]); v != "" {
			// Hub metadata keys use underscores.
			key := f
			if f == "build-id" {
				key = "build_id"
			}
			merged[key] = v
		}
	}
	if len(merged) == 0 {
		merged = nil
	}
	kind := d.Kind
	if kind == "" {
		kind = kindSDK
	}
	return Entry{Kind: kind, Name: pick("name", d.Name), Version: pick("version", d.Version),
		Description: pick("description", d.Description), Meta: merged}
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
		path := file
		if d, file, err = readDescriptor(path); err != nil {
			return err
		}
		if len(d.Publish) > 0 {
			return publishBundle(c, path, d)
		}
	}
	// a flag on the command line wins over the descriptor
	pick := func(flag, fromFile string) string {
		if v := c.String(flag); v != "" {
			return v
		}
		return fromFile
	}
	return publish(c, file, entryOf(d, meta, pick))
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
		Description: "e.g. `pvr sdk publish tmp/deploy/images/qemux86-64/acme-image-qemux86-64.sdk.json` (the descriptor the build writes next to the image: publishes the SDK and whatever else the build lists with it)\n   or `pvr sdk publish tmp/deploy/sdk/acme-image-qemux86-64.sdk.json` (the SDK alone)\n   or `pvr sdk publish pv-sdk-installer.sh --name acme-image --version 1.0 --machine qemux86-64`",
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
