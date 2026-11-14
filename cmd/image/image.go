// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.

// Package image is pvr's 'image' command: flashable device images.
package image

import (
	"errors"
	"os"

	"github.com/urfave/cli"
	"gitlab.com/pantacor/pvr/libpvr/image"
)

// CommandImage makes flashable device images.
func CommandImage() cli.Command {
	return cli.Command{
		Name:  "image",
		Usage: "make flashable device images",
		Subcommands: []cli.Command{
			commandFactory(),
			commandAssemble(),
		},
	}
}

func commandFactory() cli.Command {
	return cli.Command{
		Name:  "factory",
		Usage: "make a factory image: an image base customised for a signing identity and policy, with no secret",
		Description: `Does the slow, secret-free part of an image once: the initrd gets the
identity's trust and PV_OEM_NAME, PV_POLICY and PV_SECUREBOOT_MODE, and the
platform key signs the BSP and the base's containers. The factory image
(.tar) holds no key; 'pvr image assemble --base <factory>' then adds apps and
the OEM configuration part and writes images in seconds, without the key.`,
		Flags: []cli.Flag{
			cli.StringFlag{Name: "base, b", Usage: "image base tarball, factory image, or full image (.wic, or a release .tar.gz holding one)"},
			cli.StringFlag{Name: "trust, t", Usage: "directory with the identity's ca-certificates.crt and ca-oem-certificates.crt ('pvr signing trust')"},
			cli.StringFlag{Name: "oem-name", Usage: "PV_OEM_NAME: the signing identity's name"},
			cli.StringFlag{Name: "policy", Usage: "PV_POLICY of the images made of it, such as prod"},
			cli.StringFlag{Name: "secureboot", Usage: "PV_SECUREBOOT_MODE: disabled, lenient, strict or audit (default strict)"},
			cli.StringSliceFlag{Name: "group-timeout", Usage: "NAME=SECONDS: timeout of a container group of the BSP's device.json, such as root=120; repeat for several"},
			cli.StringFlag{Name: "key, k", EnvVar: "PVR_SIG_KEY", Usage: "platform signing key (PEM)"},
			cli.StringFlag{Name: "x5c, x", EnvVar: "PVR_X5C_PATH", Usage: "platform certificate chain (PEM)"},
			cli.StringFlag{Name: "output, o", Usage: "factory image, .tar"},
			cli.StringFlag{Name: "workdir", Usage: "empty directory to work in (default: a temporary one)"},
		},
		Action: func(c *cli.Context) error {
			for _, flag := range []string{"base", "trust", "key", "x5c", "output"} {
				if c.String(flag) == "" {
					return cli.NewExitError(errors.New("--"+flag+" is needed; see --help"), 2)
				}
			}
			timeouts, err := image.ParseGroupTimeouts(c.StringSlice("group-timeout"))
			if err != nil {
				return cli.NewExitError(err, 2)
			}
			err = image.Factory(image.Options{
				GroupTimeouts: timeouts,
				Base:          c.String("base"), Trust: c.String("trust"), OEMName: c.String("oem-name"), Policy: c.String("policy"),
				SecureBoot: c.String("secureboot"), Key: c.String("key"), X5c: c.String("x5c"), Output: c.String("output"),
				WorkDir: c.String("workdir"), Log: os.Stderr,
			})
			if err != nil {
				return cli.NewExitError(err, 1)
			}
			return nil
		},
	}
}

func commandAssemble() cli.Command {
	return cli.Command{
		Name:      "assemble",
		ArgsUsage: "",
		Usage:     "assemble a flashable image from an image base, a signing identity's trust and exports",
		Description: `Makes a flashable image in minutes, without a Yocto build, from an image
base (meta-pantavisor's pantavisor-image-base-<machine>.tar: the BSP, the
core containers, the boot selector and the partition layout):

  - the initrd gets the identity's trust (ca-certificates.crt,
    ca-oem-certificates.crt) and PV_OEM_NAME, PV_SECUREBOOT_MODE and
    PV_POLICY in /etc/pantavisor.config;
  - the BSP and the base's containers are signed again with the platform
    key (--key, --x5c), which ca-certificates.crt trusts;
  - the exports (apps signed with the identity's OEM key, an OEM
    configuration part, ...) are merged into the factory revision;
  - every signature is verified with the store a device would use, and in
    strict mode every key must be signed;
  - the partitions are written as the base's layout says: an msdos or gpt
    table, the storage partition with the base's boot files (such as a
    U-Boot boot.scr) and, for the uboot bootloader, boot/uboot.txt.

--trust is the directory 'pvr signing trust <identity> -o DIR' writes; its
pantavisor.config gives the defaults of --oem-name and --secureboot.

--base may be a factory image ('pvr image factory'): its identity, policy and
trust are used, nothing is signed again, and --key and --x5c are not needed.

--base may also be a full image, such as a release meta-pantavisor's CI
publishes (https://pantavisor.io/downloads: <board>-<version>.tar.gz holding
a .wic.bz2), or any .wic (raw, .gz, .bz2, .xz): its initrd and BSP are kept
as they are, the exports join the factory revision of its storage partition
(the ext4 holding trails/0), which is written again, grown when it is the
last partition of an msdos table; the other partitions are kept byte for
byte. --trust is the trust it was built with (Pantavisor's developer one
for those releases: PV_OEM_NAME pv-oem-developer-001); no key, policy or
group timeout, which need an image base. For example:

  pvr image assemble --base rpi-scarthgap-030.tar.gz --trust devtrust \
    --export app.pvrexport.tgz -o rpi-app.img.xz

Needs mtools, e2fsprogs and, for .img.xz, xz.`,
		Flags: []cli.Flag{
			cli.StringFlag{Name: "base, b", Usage: "image base tarball, factory image, or full image (.wic, or a release .tar.gz holding one)"},
			cli.StringSliceFlag{Name: "export, e", Usage: "pvr export to merge into the factory revision; repeat for several, merged in order"},
			cli.StringFlag{Name: "trust, t", Usage: "directory with the identity's ca-certificates.crt and ca-oem-certificates.crt"},
			cli.StringFlag{Name: "oem-name", Usage: "PV_OEM_NAME: the signing identity's name"},
			cli.StringFlag{Name: "policy", Usage: "PV_POLICY: picks <oem>/<policy>.config of an OEM part, such as prod or dev"},
			cli.StringFlag{Name: "secureboot", Usage: "PV_SECUREBOOT_MODE: disabled, lenient, strict or audit (default strict)"},
			cli.StringFlag{Name: "pantahub-host", Usage: "creds.host of the device's pantahub.config"},
			cli.IntFlag{Name: "pantahub-port", Usage: "creds.port of the device's pantahub.config"},
			cli.StringSliceFlag{Name: "group-timeout", Usage: "NAME=SECONDS: timeout of a container group of the BSP's device.json, such as root=120; repeat for several"},
			cli.StringFlag{Name: "key, k", EnvVar: "PVR_SIG_KEY", Usage: "platform signing key (PEM)"},
			cli.StringFlag{Name: "x5c, x", EnvVar: "PVR_X5C_PATH", Usage: "platform certificate chain (PEM)"},
			cli.StringFlag{Name: "output, o", Usage: "image file, .img or .img.xz"},
			cli.StringFlag{Name: "export-out", Usage: "also write the factory revision as a pvr export"},
			cli.StringFlag{Name: "workdir", Usage: "empty directory to work in (default: a temporary one)"},
		},
		Action: func(c *cli.Context) error {
			for _, flag := range []string{"base", "output"} {
				if c.String(flag) == "" {
					return cli.NewExitError(errors.New("--"+flag+" is needed; see --help"), 2)
				}
			}
			timeouts, err := image.ParseGroupTimeouts(c.StringSlice("group-timeout"))
			if err != nil {
				return cli.NewExitError(err, 2)
			}
			err = image.Assemble(image.Options{
				GroupTimeouts: timeouts,
				Base:          c.String("base"),
				Exports:       c.StringSlice("export"),
				Trust:         c.String("trust"),
				OEMName:       c.String("oem-name"),
				Policy:        c.String("policy"),
				SecureBoot:    c.String("secureboot"),
				PantahubHost:  c.String("pantahub-host"),
				PantahubPort:  c.Int("pantahub-port"),
				Key:           c.String("key"),
				X5c:           c.String("x5c"),
				Output:        c.String("output"),
				ExportOut:     c.String("export-out"),
				WorkDir:       c.String("workdir"),
				Log:           os.Stderr,
			})
			if err != nil {
				return cli.NewExitError(err, 1)
			}
			return nil
		},
	}
}
