// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package artifact

import (
	"errors"

	"github.com/urfave/cli"
)

const kindSDK = "sdk"

// sdkFlags are the image details that go into the entry's meta.
var sdkFlags = []string{"machine", "target", "platform", "image", "build-id"}

func sdkPublish(c *cli.Context) error {
	if c.NArg() != 1 {
		return errors.New("publish takes one installer file")
	}
	meta, err := parseMeta(c.StringSlice("meta"))
	if err != nil {
		return err
	}
	for _, f := range sdkFlags {
		if v := c.String(f); v != "" {
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
	return publish(c, c.Args().First(), Entry{Kind: kindSDK, Name: c.String("name"),
		Version: c.String("version"), Description: c.String("description"), Meta: meta})
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
		Description: "e.g. `pvr sdk publish pv-sdk-installer.sh --name acme-image --version 1.0 --machine qemux86-64`",
		Subcommands: []cli.Command{
			{Name: "publish", ArgsUsage: "<installer>", Usage: "upload an SDK installer and register it", Action: sdkPublish, Flags: []cli.Flag{
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
