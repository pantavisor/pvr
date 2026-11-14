// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package image

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTarFile(t *testing.T, file string, gz bool, entries map[string][]byte) {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	if gz {
		data = gzipped(t, data)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mbrDisk(t *testing.T, size int64, entries []mbrEntry) []byte {
	t.Helper()
	f := &fileAt{b: make([]byte, size)}
	if err := writeMBR(f, entries, 0x1234); err != nil {
		t.Fatal(err)
	}
	return f.b
}

func TestIsFullImage(t *testing.T) {
	dir := t.TempDir()
	disk := mbrDisk(t, 1<<20, []mbrEntry{{start: 4096, size: 8192, kind: 0x83}})
	cases := map[string]struct {
		write func(string)
		full  bool
	}{
		"image base": {func(f string) { writeTarFile(t, f, false, map[string][]byte{"layout.json": []byte("{}")}) }, false},
		"CI release": {func(f string) {
			writeTarFile(t, f, true, map[string][]byte{"x.wic.bmap": []byte("bmap"), "x.rootfs.wic.bz2": []byte("BZh9")})
		}, true},
		"raw wic":     {func(f string) { os.WriteFile(f, disk, 0o644) }, true},
		"gzipped wic": {func(f string) { os.WriteFile(f, gzipped(t, disk), 0o644) }, true},
		"garbage":     {func(f string) { os.WriteFile(f, bytes.Repeat([]byte("x"), 2048), 0o644) }, false},
	}
	for name, c := range cases {
		file := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		c.write(file)
		full, err := isFullImage(file)
		if err != nil || full != c.full {
			t.Errorf("%s: full %v (%v), want %v", name, full, err, c.full)
		}
	}
}

func TestReadPartitions(t *testing.T) {
	disk := mbrDisk(t, 1<<20, []mbrEntry{{start: 4096, size: 8192, kind: 0x0c, active: true}, {start: 12288, size: 16384, kind: 0x83}})
	table, parts, err := readPartitions(bytes.NewReader(disk))
	if err != nil || table != "msdos" || len(parts) != 2 || parts[1].start != 12288 || parts[1].size != 16384 || !parts[1].linux || parts[0].linux || parts[1].entry != 446+16 {
		t.Fatalf("msdos: %s %+v %v", table, parts, err)
	}
	f := &fileAt{b: make([]byte, 4<<20)}
	if err := writeGPT(f, []gptEntry{{start: 1 << 20, size: 1 << 20, kind: gptVfat, name: "boot"}, {start: 2 << 20, size: 1 << 20, kind: gptLinux, name: "root"}}, int64(len(f.b)), [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	table, parts, err = readPartitions(bytes.NewReader(f.b))
	if err != nil || table != "gpt" || len(parts) != 2 || parts[1].start != 2<<20 || parts[1].size != 1<<20 || !parts[1].linux || parts[0].linux {
		t.Fatalf("gpt: %s %+v %v", table, parts, err)
	}
	if _, _, err := readPartitions(bytes.NewReader(make([]byte, 1024))); err == nil {
		t.Error("a disk without a table accepted")
	}
}

func TestLsEntries(t *testing.T) {
	out := "/2/040755/0/0/.//\n/2/040755/0/0/..//\n/13/040755/0/0/boot//\n/45/100644/0/0/oemEnv.txt/9/\n\n"
	got := strings.Join(lsEntries(out), ",")
	if got != "boot,oemEnv.txt" {
		t.Errorf("lsEntries: %s", got)
	}
}

func TestSettleFullRefuses(t *testing.T) {
	trust := t.TempDir()
	for _, store := range []string{platformStore, oemStore} {
		os.WriteFile(filepath.Join(trust, store), []byte("CA"), 0o644)
	}
	for name, a := range map[string]*assembler{
		"factory":  {factory: true, Options: Options{Trust: trust}},
		"policy":   {Options: Options{Trust: trust, Policy: "prod"}},
		"key":      {Options: Options{Trust: trust, Key: "k", X5c: "x"}},
		"no trust": {},
	} {
		a.full, a.Log = &fullImage{}, io.Discard
		if a.settleFull() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	a := &assembler{Options: Options{Trust: trust, Log: io.Discard}, full: &fullImage{}}
	if err := a.settleFull(); err != nil || a.OEMName != DefaultOEMName || a.SecureBoot != "lenient" {
		t.Errorf("defaults: %q %q %v", a.OEMName, a.SecureBoot, err)
	}
}

// TestRemixFullImage takes a full image apart and writes it again with a
// new factory revision: the other partitions kept byte for byte, the
// storage's files but the trails kept, the trail written from the state.
func TestRemixFullImage(t *testing.T) {
	for _, tool := range []string{"mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is needed (e2fsprogs)", tool)
		}
	}
	work := t.TempDir()
	object := []byte("kernel image")
	sum := sha256.Sum256(object)
	sha := hex.EncodeToString(sum[:])
	state := `{"#spec":"pantavisor-service-system@1","bsp/kernel.img":"` + sha + `","device.json":{"groups":[]}}`
	src := filepath.Join(work, "src")
	for name, data := range map[string]string{
		"trails/0/.pvr/json":                 state,
		"trails/0/bsp/kernel.img":            string(object),
		"objects/" + sha:                     string(object),
		"objects/" + strings.Repeat("0", 64): "unused",
		"config/pantahub.config":             "creds.host=api.pantahub.com\ncreds.port=443\n",
		"boot/boot.scr":                      "boot script",
		"oemEnv.txt":                         "oemargs=\n",
	} {
		file := filepath.Join(src, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(file), 0o755)
		if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(src, "trails", "0", ".pv"), 0o755)
	ext4 := filepath.Join(work, "src.ext4")
	if out, err := exec.Command("mkfs.ext4", "-q", "-F", "-L", "root", "-d", src, ext4, "8M").CombinedOutput(); err != nil {
		t.Fatalf("mkfs: %s %v", out, err)
	}
	storage, _ := os.ReadFile(ext4)
	boot := bytes.Repeat([]byte("vfat"), (1<<20)/4)
	const mib = 1 << 20
	disk := mbrDisk(t, 2*mib+int64(len(storage)), []mbrEntry{{start: mib, size: mib, kind: 0x0c, active: true}, {start: 2 * mib, size: int64(len(storage)), kind: 0x83}})
	copy(disk[mib:], boot)
	copy(disk[2*mib:], storage)
	release := filepath.Join(work, "board-030.tar.gz")
	writeTarFile(t, release, true, map[string][]byte{"pantavisor-starter-board.rootfs.wic.gz": gzipped(t, disk)})

	a := &assembler{work: filepath.Join(work, "w")}
	a.Base, a.Output, a.Log = release, filepath.Join(work, "out.img"), io.Discard
	a.base, a.repo, a.rootfs = filepath.Join(a.work, "base"), filepath.Join(a.work, "repo"), filepath.Join(a.work, "rootfs")
	a.objects = filepath.Join(a.rootfs, "objects")
	for _, dir := range []string{a.base, filepath.Join(a.repo, ".pvr"), a.objects, filepath.Join(a.rootfs, "trails", "0", ".pvr"), filepath.Join(a.rootfs, "config")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	a.env = os.Environ()
	if err := a.unpack(); err != nil {
		t.Fatal(err)
	}
	if a.full == nil || a.full.root != 1 || a.full.label != "root" || !a.full.pvDir || a.layout.Bootloader != "full" {
		t.Fatalf("full image read as %+v, layout %+v", a.full, a.layout)
	}
	// The revision as deployFull and mergeExports leave it: pvr is not run here.
	if err := os.WriteFile(filepath.Join(a.repo, ".pvr", "json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	a.PantahubHost = "api.stage.pantahub.com"
	if err := a.writeRootfs(); err != nil {
		t.Fatal(err)
	}
	if err := a.writeFullImage(); err != nil {
		t.Fatal(err)
	}
	img, err := os.ReadFile(a.Output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(img[mib:2*mib], boot) || !bytes.Equal(img[:446], disk[:446]) {
		t.Error("the boot partition or the boot code changed")
	}
	_, parts, err := readPartitions(bytes.NewReader(img))
	if err != nil || len(parts) != 2 || parts[1].start != 2*mib || int64(len(img)) != parts[1].start+parts[1].size {
		t.Fatalf("remixed table %+v (%d bytes): %v", parts, len(img), err)
	}
	root := filepath.Join(work, "root.ext4")
	os.WriteFile(root, img[parts[1].start:], 0o644)
	for file, want := range map[string]string{
		"/boot/boot.scr": "boot script", "/oemEnv.txt": "oemargs=\n", "/trails/0/.pvr/json": state,
		"/trails/0/bsp/kernel.img": string(object), "/config/pantahub.config": "creds.host=api.stage.pantahub.com\ncreds.port=443\n",
	} {
		out, err := exec.Command("debugfs", "-R", "cat "+file, root).Output()
		if err != nil || string(out) != want {
			t.Errorf("%s reads %q: %v", file, out, err)
		}
	}
	out, _ := exec.Command("debugfs", "-R", "ls -p /trails/0", root).Output()
	if !strings.Contains(string(out), "/.pv/") {
		t.Errorf("the trail lost its .pv directory: %s", out)
	}
	out, _ = exec.Command("debugfs", "-R", "ls -p /objects", root).Output()
	if strings.Contains(string(out), strings.Repeat("0", 64)) || !strings.Contains(string(out), sha) {
		t.Errorf("objects: %s", out)
	}
}
