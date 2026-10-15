// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package image

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode"
)

// Layout is the layout.json of an image base: how a flashable image of the
// base's machine is laid out.
type Layout struct {
	Version    int         `json:"version"`
	Machine    string      `json:"machine"`
	Bootloader string      `json:"bootloader"`
	Table      string      `json:"table"`
	Align      int64       `json:"align"`
	Partitions []Partition `json:"partitions"`
	// Initrd is where the pantavisor initrd is: a vfat image the BSP
	// carries gzipped, and the initrd's path in it.
	Initrd struct {
		BSP  string `json:"bsp"`
		Path string `json:"path"`
	} `json:"initrd"`
	// Containers are exports of the base, signed again with the platform
	// key and deployed with the BSP.
	Containers []string `json:"containers"`
	// Factory is set on a factory image: what it was made for. Its
	// bsp.pvrexport.tgz is the signed revision (BSP, skeleton, base
	// containers), boot.vfat the boot partition with the remastered
	// initrd, trust/ the identity's public trust.
	Factory *FactoryInfo `json:"factory,omitempty"`
}

// FactoryInfo says what a factory image was made for.
type FactoryInfo struct {
	Identity   string `json:"identity"`
	Policy     string `json:"policy,omitempty"`
	SecureBoot string `json:"secureboot"`
	// PlatformSHA256 and OEMSHA256 are the sha256 of its
	// ca-certificates.crt and ca-oem-certificates.crt.
	PlatformSHA256 string `json:"platform_sha256"`
	OEMSHA256      string `json:"oem_sha256"`
	// Base is the sha256 of the image base it was made of.
	Base string `json:"base"`
	// GroupTimeouts are the device.json group timeouts it was made with.
	GroupTimeouts map[string]int `json:"group_timeouts,omitempty"`
}

// Partition sources: a file of the base, a gzipped file of the BSP
// ("bsp:bsp/<file>"), or the pvroot storage made of the factory revision.
type Partition struct {
	Name   string `json:"name"`
	FS     string `json:"fs"`
	Label  string `json:"label"`
	Size   int64  `json:"size"`
	Active bool   `json:"active"`
	Source string `json:"source"`
}

const (
	sourcePvroot   = "pvroot"
	sourceBSP      = "bsp:"
	sector         = 512
	maxLayoutBytes = 16 << 30 // 16 GiB
)

func isDisallowedPathChar(r rune) bool {
	return r < 0x20 || r == 0x7f || r == ' ' || r == '"' || r == '\'' || r == '`' || unicode.IsSpace(r)
}

func baseFile(name string) bool {
	if name == "" || name == "." || path.Clean(name) != name || path.IsAbs(name) || strings.HasPrefix(name, "..") || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	for _, r := range name {
		if isDisallowedPathChar(r) {
			return false
		}
	}
	return true
}

func (l *Layout) validate() error {
	if l.Version != 1 {
		return fmt.Errorf("image base layout version %d is not supported: update pvr", l.Version)
	}
	if l.Bootloader != "rpiab" {
		return fmt.Errorf("image base bootloader %q is not supported: only rpiab", l.Bootloader)
	}
	if l.Table != "msdos" {
		return fmt.Errorf("image base partition table %q is not supported: only msdos", l.Table)
	}
	if l.Align < sector || l.Align%sector != 0 || l.Align > 64<<20 {
		return errors.New("image base align is a multiple of 512 bytes up to 64 MiB")
	}
	if len(l.Partitions) == 0 || len(l.Partitions) > 4 {
		return errors.New("image base has 1 to 4 partitions")
	}
	if !baseFile(l.Initrd.BSP) || !strings.HasPrefix(l.Initrd.BSP, "bsp/") || !baseFile(l.Initrd.Path) {
		return errors.New("image base initrd names a gzipped vfat file of the BSP and a file in it")
	}
	pvroot := 0
	var totalSize int64
	for _, p := range l.Partitions {
		switch {
		case p.Source == sourcePvroot:
			pvroot++
			if p.FS != "ext4" {
				return errors.New("image base pvroot partition is ext4")
			}
			if p.Label == "" || len(p.Label) > 16 || strings.ContainsAny(p.Label, " \t\n/") {
				return errors.New("image base pvroot partition needs an ext4 label (PV_STORAGE_DEVICE)")
			}
		case strings.HasPrefix(p.Source, sourceBSP):
			if !baseFile(strings.TrimPrefix(p.Source, sourceBSP)) {
				return fmt.Errorf("image base partition %s source is not a BSP file", p.Name)
			}
		default:
			if !baseFile(p.Source) {
				return fmt.Errorf("image base partition %s source is not a file of the base", p.Name)
			}
		}
		if p.FS != "vfat" && p.FS != "ext4" {
			return fmt.Errorf("image base partition %s fs is vfat or ext4", p.Name)
		}
		if p.Source != sourcePvroot && (p.Size <= 0 || p.Size%sector != 0) {
			return fmt.Errorf("image base partition %s needs a size in whole sectors", p.Name)
		}
		if p.Size < 0 || p.Size > maxLayoutBytes {
			return fmt.Errorf("image base partition %s size exceeds %d bytes limit", p.Name, maxLayoutBytes)
		}
		totalSize += p.Size
		if totalSize > maxLayoutBytes {
			return fmt.Errorf("image base total partition size exceeds %d bytes limit", maxLayoutBytes)
		}
	}
	if pvroot != 1 {
		return errors.New("image base has exactly one pvroot partition")
	}
	for _, c := range l.Containers {
		if !baseFile(c) {
			return fmt.Errorf("image base container %q is not a file of the base", c)
		}
	}
	if f := l.Factory; f != nil {
		if !oemName.MatchString(f.Identity) || (f.Policy != "" && !policyName.MatchString(f.Policy)) || !secureBoot[f.SecureBoot] ||
			!objectName.MatchString(f.PlatformSHA256) || !objectName.MatchString(f.OEMSHA256) || !objectName.MatchString(f.Base) || len(l.Containers) > 0 {
			return errors.New("factory image layout.json has an invalid factory block")
		}
	}
	return nil
}

func readLayout(file string) (*Layout, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("image base has no layout.json: %w", err)
	}
	var l Layout
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("image base layout.json: %w", err)
	}
	return &l, l.validate()
}

// mbrEntry is one primary partition of an msdos table, in bytes.
type mbrEntry struct {
	start, size int64
	kind        byte
	active      bool
}

// Partition types as wic writes them.
func mbrKind(fs string) byte {
	if fs == "vfat" {
		return 0x0c // W95 FAT32 (LBA)
	}
	return 0x83 // Linux
}

// writeMBR writes the msdos partition table of entries into the first
// sector of w. CHS fields hold the LBA-only marker, as for any disk past
// 8 GiB; the disk identifier is id.
func writeMBR(w io.WriterAt, entries []mbrEntry, id uint32) error {
	var mbr [sector]byte
	binary.LittleEndian.PutUint32(mbr[440:], id)
	for i, e := range entries {
		if e.start%sector != 0 || e.size%sector != 0 || (e.start+e.size)/sector > 0xffffffff {
			return errors.New("partition is not addressable in an msdos table")
		}
		p := mbr[446+16*i : 446+16*(i+1)]
		if e.active {
			p[0] = 0x80
		}
		copy(p[1:4], []byte{0xfe, 0xff, 0xff})
		p[4] = e.kind
		copy(p[5:8], []byte{0xfe, 0xff, 0xff})
		binary.LittleEndian.PutUint32(p[8:], uint32(e.start/sector))
		binary.LittleEndian.PutUint32(p[12:], uint32(e.size/sector))
	}
	mbr[510], mbr[511] = 0x55, 0xaa
	_, err := w.WriteAt(mbr[:], 0)
	return err
}

func alignUp(n, align int64) int64 {
	return (n + align - 1) / align * align
}
