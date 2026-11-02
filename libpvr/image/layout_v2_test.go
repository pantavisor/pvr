// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package image

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"
)

// jetsonLayout is the version 2 layout meta-pantavisor writes for the
// Jetson Nano devkit.
const jetsonLayout = `{"version":2,"machine":"jetson-nano-devkit","bootloader":"uboot","table":"gpt","align":1048576,
	"partitions":[{"name":"APP","fs":"ext4","label":"root","source":"pvroot",
		"files":{"boot/boot.scr":"boot.scr","oemEnv.txt":"oemEnv.txt"}}],
	"initrd":{"bsp":"bsp/pantavisor"},"containers":["containers/pv-avahi.pvrexport.tgz"]}`

func TestLayoutV2Validation(t *testing.T) {
	var l Layout
	if err := json.Unmarshal([]byte(jetsonLayout), &l); err != nil {
		t.Fatal(err)
	}
	if err := l.validate(); err != nil {
		t.Fatal(err)
	}
	// Version 2 takes the rpiab layout of version 1 as is.
	rpiab := l
	rpiab.Bootloader, rpiab.Table = "rpiab", "msdos"
	rpiab.Initrd.BSP, rpiab.Initrd.Path = "bsp/pantavisor-rpi.img.gz", "pantavisor"
	rpiab.Partitions = []Partition{{Name: "boot_a", FS: "vfat", Size: 1 << 20, Source: "bsp:bsp/pantavisor-rpi.img.gz"}, {Name: "root", FS: "ext4", Label: "root", Source: "pvroot"}}
	if err := rpiab.validate(); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]func(*Layout){
		"version 3":             func(l *Layout) { l.Version = 3 },
		"uboot in version 1":    func(l *Layout) { l.Version = 1 },
		"gpt in version 1":      func(l *Layout) { l.Version, l.Bootloader, l.Initrd.Path = 1, "rpiab", "pantavisor" },
		"other loader":          func(l *Layout) { l.Bootloader = "grub" },
		"other table":           func(l *Layout) { l.Table = "apm" },
		"unnamed gpt partition": func(l *Layout) { l.Partitions[0].Name = "" },
		"long gpt name":         func(l *Layout) { l.Partitions[0].Name = strings.Repeat("A", 37) },
		"control in gpt name":   func(l *Layout) { l.Partitions[0].Name = "AP\nP" },
		"escaping initrd":       func(l *Layout) { l.Initrd.BSP = "bsp/../x" },
		"escaping file source":  func(l *Layout) { l.Partitions[0].Files = map[string]string{"boot/boot.scr": "../boot.scr"} },
		"absolute file":         func(l *Layout) { l.Partitions[0].Files = map[string]string{"/boot/boot.scr": "boot.scr"} },
		"file over objects":     func(l *Layout) { l.Partitions[0].Files = map[string]string{"objects/x": "boot.scr"} },
		"file over trails":      func(l *Layout) { l.Partitions[0].Files = map[string]string{"trails": "boot.scr"} },
		"layout as file":        func(l *Layout) { l.Partitions[0].Files = map[string]string{"x": "layout.json"} },
		"files elsewhere": func(l *Layout) {
			l.Partitions = append(l.Partitions, Partition{Name: "boot", FS: "vfat", Size: 1 << 20, Source: "boot.vfat", Files: map[string]string{"a": "b"}})
		},
		"initrd as partition": func(l *Layout) {
			l.Partitions = append(l.Partitions, Partition{Name: "boot", FS: "vfat", Size: 1 << 20, Source: "bsp:bsp/pantavisor"})
		},
	} {
		c := l
		c.Partitions = append([]Partition{}, l.Partitions...)
		bad(&c)
		if c.validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Version 1 has neither files nor an initrd without a path.
	v1 := rpiab
	v1.Version = 1
	if err := v1.validate(); err != nil {
		t.Fatal(err)
	}
	v1.Partitions = []Partition{v1.Partitions[0], {Name: "root", FS: "ext4", Label: "root", Source: "pvroot", Files: map[string]string{"a": "b"}}}
	if v1.validate() == nil {
		t.Error("files in a version 1 layout accepted")
	}
	v1.Partitions[1].Files, v1.Initrd.Path = nil, ""
	if v1.validate() == nil {
		t.Error("a version 1 initrd without a path accepted")
	}
}

func gzipped(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestRemasterCpioInitrd remasters an initrd that is the BSP file itself.
func TestRemasterCpioInitrd(t *testing.T) {
	work := t.TempDir()
	a := &assembler{work: work, repo: filepath.Join(work, "repo"), layout: &Layout{}}
	a.layout.Initrd.BSP = "bsp/pantavisor"
	a.Trust, a.OEMName, a.SecureBoot, a.Policy, a.Log = filepath.Join(work, "trust"), "pv-oem-developer-001", "lenient", "green", io.Discard
	for _, dir := range []string{filepath.Join(a.repo, "bsp"), a.Trust} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for store, data := range map[string]string{platformStore: "developer platform CA", oemStore: "developer OEM CA"} {
		if err := os.WriteFile(filepath.Join(a.Trust, store), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	initrd := gzipped(t, archive(t,
		entry(1, "etc", modeDir|0o755, ""),
		entry(2, "etc/pantavisor.config", modeFile|0o644, "PV_STORAGE_DEVICE=LABEL=root\nPV_OEM_NAME=pv-oem-developer-001\n"),
		entry(3, "etc/pantavisor/pvs/trust/ca-certificates.crt", modeFile|0o644, "old"),
		entry(4, "init", modeFile|0o755, "#!/bin/sh\n"),
	))
	// The BSP file is a hard link to its object, which must not change.
	object := filepath.Join(work, "object")
	if err := os.WriteFile(object, initrd, 0o644); err != nil {
		t.Fatal(err)
	}
	bspFile := filepath.Join(a.repo, "bsp", "pantavisor")
	if err := os.Link(object, bspFile); err != nil {
		t.Fatal(err)
	}
	if err := a.remaster(); err != nil {
		t.Fatal(err)
	}
	if kept, _ := os.ReadFile(object); !bytes.Equal(kept, initrd) {
		t.Fatal("remastering wrote through the object's hard link")
	}
	data, err := os.ReadFile(bspFile)
	if err != nil {
		t.Fatal(err)
	}
	// gzip's XFL byte says maximum compression.
	if len(data) < 10 || data[8] != 2 {
		t.Fatalf("initrd not gzipped at maximum compression: % x", data[:10])
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	files := readAll(t, raw)
	config := keyValues(files["etc/pantavisor.config"].data)
	if config["PV_STORAGE_DEVICE"] != "LABEL=root" || config["PV_OEM_NAME"] != "pv-oem-developer-001" || config["PV_SECUREBOOT_MODE"] != "lenient" || config["PV_POLICY"] != "green" {
		t.Errorf("pantavisor.config: %q", files["etc/pantavisor.config"].data)
	}
	if string(files[trustDir+"/"+platformStore].data) != "developer platform CA" || string(files[trustDir+"/"+oemStore].data) != "developer OEM CA" {
		t.Error("trust not replaced")
	}
	if files["etc/pantavisor/policies/green.config"] == nil || string(files["init"].data) != "#!/bin/sh\n" {
		t.Error("policy missing or other files changed")
	}
	if err := os.WriteFile(bspFile, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a.remaster() == nil {
		t.Error("a BSP file that is not a gzipped cpio remastered")
	}
}

// gptPartition is a partition read back from a gpt table.
type gptPartition struct {
	kind       [16]byte
	start, end uint64
	attrs      uint64
	name       string
}

// readGPT reads and checks the primary and backup gpt tables of img.
func readGPT(t *testing.T, img []byte) []gptPartition {
	t.Helper()
	if img[510] != 0x55 || img[511] != 0xaa || img[450] != 0xee {
		t.Fatal("no protective msdos table")
	}
	sectors := uint64(len(img) / sector)
	var parts []gptPartition
	for _, lba := range []uint64{1, sectors - 1} {
		h := append([]byte{}, img[lba*sector:lba*sector+92]...)
		if string(h[:8]) != "EFI PART" || binary.LittleEndian.Uint64(h[24:]) != lba {
			t.Fatalf("no gpt header at sector %d", lba)
		}
		crc := binary.LittleEndian.Uint32(h[16:])
		binary.LittleEndian.PutUint32(h[16:], 0)
		if crc32.ChecksumIEEE(h) != crc {
			t.Fatalf("gpt header at %d: bad crc", lba)
		}
		table := binary.LittleEndian.Uint64(h[72:])
		array := img[table*sector : table*sector+128*128]
		if crc32.ChecksumIEEE(array) != binary.LittleEndian.Uint32(h[88:]) {
			t.Fatalf("gpt entries at %d: bad crc", table)
		}
		if lba != 1 {
			continue
		}
		for i := 0; i < 128; i++ {
			e := array[128*i : 128*(i+1)]
			var p gptPartition
			copy(p.kind[:], e[:16])
			if p.kind == ([16]byte{}) {
				continue
			}
			p.start, p.end, p.attrs = binary.LittleEndian.Uint64(e[32:]), binary.LittleEndian.Uint64(e[40:]), binary.LittleEndian.Uint64(e[48:])
			name := make([]uint16, 36)
			for j := range name {
				name[j] = binary.LittleEndian.Uint16(e[56+2*j:])
			}
			p.name = strings.TrimRight(string(utf16.Decode(name)), "\x00")
			parts = append(parts, p)
		}
	}
	return parts
}

func TestWriteGPT(t *testing.T) {
	const size = 8 << 20
	f := &fileAt{b: make([]byte, size)}
	entries := []gptEntry{
		{start: 1 << 20, size: 2 << 20, kind: gptKind("vfat"), name: "boot", active: true},
		{start: 3 << 20, size: 4 << 20, kind: gptKind("ext4"), name: "APP"},
	}
	if err := writeGPT(f, entries, size, seededGUID([]byte("seed"), "disk")); err != nil {
		t.Fatal(err)
	}
	parts := readGPT(t, f.b)
	if len(parts) != 2 || parts[0].name != "boot" || parts[0].start != 2048 || parts[0].end != 6143 || parts[0].attrs != 4 || parts[0].kind != gptVfat ||
		parts[1].name != "APP" || parts[1].start != 6144 || parts[1].kind != gptLinux {
		t.Fatalf("gpt reads %+v", parts)
	}
	if err := writeGPT(f, []gptEntry{{start: 7 << 20, size: 1 << 20, kind: gptLinux, name: "x"}}, size, [16]byte{}); err == nil {
		t.Error("a partition over the backup table accepted")
	}
	if err := writeGPT(f, []gptEntry{{start: sector, size: 1 << 20, kind: gptLinux, name: "x"}}, size, [16]byte{}); err == nil {
		t.Error("a partition over the primary table accepted")
	}
}

// TestAssembleGPTImage writes the storage files and image of the Jetson
// layout and reads them back as a disk.
func TestAssembleGPTImage(t *testing.T) {
	for _, tool := range []string{"mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is needed (e2fsprogs)", tool)
		}
	}
	work := t.TempDir()
	a := &assembler{work: work, base: filepath.Join(work, "base"), rootfs: filepath.Join(work, "rootfs")}
	a.Output, a.OEMName, a.Log = filepath.Join(work, "nano.img"), "pv-oem-developer-001", io.Discard
	if err := json.Unmarshal([]byte(jetsonLayout), &a.layout); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{a.base, filepath.Join(a.rootfs, "boot"), filepath.Join(a.rootfs, "trails", "0", "bsp")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	trail := filepath.Join(a.rootfs, "trails", "0")
	for name, data := range map[string]string{
		"base/boot.scr":                  "boot script",
		"base/oemEnv.txt":                "oem=1\n",
		"rootfs/trails/0/bsp/kernel.img": "kernel",
		"rootfs/trails/0/bsp/run.json":   `{"initrd":"pantavisor","linux":"kernel.img","firmware":"firmware.squashfs","modules":"modules.squashfs","addons":[]}`,
		"rootfs/trails/0/bsp/pantavisor": string(gzipped(t, archive(t, entry(1, "etc/pantavisor.config", modeFile|0o644, "PV_STORAGE_DEVICE=LABEL=root\nPV_LOG_LEVEL=3\n")))),
		"trust/" + platformStore:         "developer platform CA",
		"trust/" + oemStore:              "developer OEM CA",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(work, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The initrd remastered in the trail, as the signed revision has it.
	a.repo, a.Trust, a.SecureBoot = trail, filepath.Join(work, "trust"), "strict"
	if err := a.remaster(); err != nil {
		t.Fatal(err)
	}
	initrd, err := os.ReadFile(filepath.Join(trail, "bsp", "pantavisor"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(initrd))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	config := keyValues(readAll(t, raw)["etc/pantavisor.config"].data)
	if config["PV_STORAGE_DEVICE"] != "LABEL=root" || config["PV_LOG_LEVEL"] != "3" || config["PV_OEM_NAME"] != "pv-oem-developer-001" || config["PV_SECUREBOOT_MODE"] != "strict" {
		t.Fatalf("remastered pantavisor.config: %v", config)
	}
	if err := a.writeBootFiles(); err != nil {
		t.Fatal(err)
	}
	if err := a.writeImage(); err != nil {
		t.Fatal(err)
	}
	img, err := os.ReadFile(a.Output)
	if err != nil {
		t.Fatal(err)
	}
	parts := readGPT(t, img)
	if len(parts) != 1 || parts[0].name != "APP" || parts[0].start != 2048 || parts[0].kind != gptLinux {
		t.Fatalf("gpt reads %+v", parts)
	}
	if len(img)%(1<<20) != 0 || uint64(len(img)/sector) < parts[0].end+1+gptSectors {
		t.Fatalf("image of %d bytes has no room for the backup table", len(img))
	}
	root := filepath.Join(work, "root.part")
	if err := os.WriteFile(root, img[parts[0].start*sector:(parts[0].end+1)*sector], 0o644); err != nil {
		t.Fatal(err)
	}
	// The ext4 label is at offset 120 of the superblock, at 1024.
	if label := strings.TrimRight(string(img[parts[0].start*sector+1024+120:][:16]), "\x00"); label != "root" {
		t.Errorf("ext4 label %q", label)
	}
	// U-Boot writes the storage: no checksums its writer leaves stale, no
	// orphan_file the device's older e2fsprogs cannot fsck or resize, and
	// the 4 KiB blocks of the card the storage grows to.
	stats, err := exec.Command("debugfs", "-R", "stats", root).Output()
	if err != nil {
		t.Fatal(err)
	}
	features := regexp.MustCompile(`Filesystem features:\s+(.*)`).FindStringSubmatch(string(stats))
	if features == nil || strings.Contains(features[1], "metadata_csum") || strings.Contains(features[1], "orphan_file") || !strings.Contains(string(stats), "Block size:               4096") {
		t.Errorf("storage ext4 for U-Boot: %s", stats[:min(len(stats), 2000)])
	}
	for file, want := range map[string]string{"/boot/boot.scr": "boot script", "/oemEnv.txt": "oem=1\n", "/boot/uboot.txt": "pv_rev=0\n",
		"/trails/0/bsp/kernel.img": "kernel", "/trails/0/.pv/pv-kernel.img": "kernel", "/trails/0/.pv/pv-initrd.img": string(initrd)} {
		out, err := exec.Command("debugfs", "-R", "cat "+file, root).Output()
		if err != nil || string(out) != want {
			t.Errorf("%s reads %q: %v", file, out, err)
		}
	}
	// The boot links stay hard links in the ext4, owned by root.
	inode := regexp.MustCompile(`Inode: (\d+) .*\n(?s:.*)User: +0 +Group: +0 `)
	for link, target := range map[string]string{"/trails/0/.pv/pv-kernel.img": "/trails/0/bsp/kernel.img", "/trails/0/.pv/pv-initrd.img": "/trails/0/bsp/pantavisor"} {
		var inodes []string
		for _, file := range []string{link, target} {
			out, err := exec.Command("debugfs", "-R", "stat "+file, root).Output()
			m := inode.FindStringSubmatch(string(out))
			if err != nil || m == nil {
				t.Fatalf("stat %s: %s %v", file, out, err)
			}
			inodes = append(inodes, m[1])
		}
		if inodes[0] != inodes[1] {
			t.Errorf("%s is not a hard link to %s: inodes %v", link, target, inodes)
		}
	}
	if sfdisk, err := exec.LookPath("sfdisk"); err == nil {
		out, err := exec.Command(sfdisk, "--json", a.Output).Output()
		if err != nil {
			t.Fatal(err)
		}
		var table struct {
			Partitiontable struct {
				Label      string `json:"label"`
				Partitions []struct {
					Start int64  `json:"start"`
					Name  string `json:"name"`
					Type  string `json:"type"`
				} `json:"partitions"`
			} `json:"partitiontable"`
		}
		if err := json.Unmarshal(out, &table); err != nil {
			t.Fatal(err)
		}
		p := table.Partitiontable.Partitions
		if table.Partitiontable.Label != "gpt" || len(p) != 1 || p[0].Start != 2048 || p[0].Name != "APP" || p[0].Type != "0FC63DAF-8483-4772-8E79-3D69D8477DE4" {
			t.Fatalf("sfdisk reads %s", out)
		}
		// sfdisk --verify finds no problem in either table.
		if out, err := exec.Command(sfdisk, "--verify", a.Output).CombinedOutput(); err != nil || !strings.Contains(string(out), "No errors detected") {
			t.Fatalf("sfdisk --verify: %s %v", out, err)
		}
	}
	// A base file the layout names but lacks fails the assembly.
	if err := os.Remove(filepath.Join(a.rootfs, "oemEnv.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(a.base, "oemEnv.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(a.rootfs, "boot", "boot.scr")); err != nil {
		t.Fatal(err)
	}
	if a.writeBootFiles() == nil {
		t.Error("a missing partition file accepted")
	}
}
