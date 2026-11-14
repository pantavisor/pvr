// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package image

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A full image is a flashable image a Yocto build made, such as the
// pantavisor-starter images meta-pantavisor's CI publishes
// (<board>-<version>.tar.gz holding a .wic.bz2): its initrd already holds
// the trust and PV_OEM_NAME it was built with, and its BSP is signed for
// them. Assemble takes it as a base to remix: the exports join the factory
// revision of its storage partition (the ext4 holding trails/0), which is
// written again, grown when it is the last partition of an msdos table;
// every other partition is kept byte for byte. Nothing is signed again, so
// no platform key, policy or group timeout: those need an image base.

// DefaultOEMName is the PV_OEM_NAME of Pantavisor's developer images, which
// a full image is taken to have unless the trust or --oem-name say
// otherwise.
const DefaultOEMName = "pv-oem-developer-001"

// maxFullImageBytes caps the decompressed size of a full image.
const maxFullImageBytes int64 = 32 << 30

type fullImage struct {
	wic   string
	table string
	parts []diskPart
	root  int
	label string
	// pvDir is set when the trail has a .pv directory, which U-Boot
	// boards fill with boot links and rpiab ones keep empty.
	pvDir bool
}

type diskPart struct {
	start, size int64
	linux       bool
	// entry is the offset of its msdos table entry; 0 for gpt.
	entry int64
}

var (
	gzipMagic  = []byte{0x1f, 0x8b}
	bzip2Magic = []byte("BZh")
	xzMagic    = []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}
)

func isTar(head []byte) bool { return len(head) >= 262 && string(head[257:262]) == "ustar" }

func isMBR(head []byte) bool { return len(head) >= 512 && head[510] == 0x55 && head[511] == 0xaa }

func wicName(name string) (compression string, ok bool) {
	base := filepath.Base(name)
	for _, c := range []string{"", ".gz", ".bz2", ".xz"} {
		if strings.HasSuffix(base, ".wic"+c) {
			return strings.TrimPrefix(c, "."), true
		}
	}
	return "", false
}

// isFullImage tells a full image from an image base or factory image (a
// tar holding layout.json) by its content.
func isFullImage(file string) (bool, error) {
	f, err := os.Open(file)
	if err != nil {
		return false, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	head, _ := r.Peek(512)
	switch {
	case bytes.HasPrefix(head, gzipMagic):
		zr, err := gzip.NewReader(r)
		if err != nil {
			return false, err
		}
		inner := bufio.NewReaderSize(zr, 1<<16)
		head, _ := inner.Peek(512)
		if !isTar(head) {
			return true, nil
		}
		return tarHoldsWic(inner)
	case bytes.HasPrefix(head, bzip2Magic), bytes.HasPrefix(head, xzMagic):
		return true, nil
	case isTar(head):
		return tarHoldsWic(r)
	default:
		return isMBR(head), nil
	}
}

func tarHoldsWic(r io.Reader) (bool, error) {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if filepath.Base(h.Name) == "layout.json" {
			return false, nil
		}
		if _, ok := wicName(h.Name); ok && h.Typeflag == tar.TypeReg {
			return true, nil
		}
	}
}

// unpackFullImage writes the full image's disk image, finds its storage
// partition and takes its files, but the trails, as the rootfs.
func (a *assembler) unpackFullImage() error {
	full := &fullImage{wic: filepath.Join(a.work, "full.wic")}
	if err := a.extractWic(full.wic); err != nil {
		return fmt.Errorf("full image: %w", err)
	}
	f, err := os.Open(full.wic)
	if err != nil {
		return err
	}
	full.table, full.parts, err = readPartitions(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("full image: %w", err)
	}
	full.root = -1
	for i, p := range full.parts {
		if !p.linux {
			continue
		}
		part := filepath.Join(a.work, fmt.Sprintf("part-%d.ext4", i+1))
		if err := copyRange(full.wic, part, p.start, p.size, 0o600); err != nil {
			return err
		}
		state := filepath.Join(a.work, fmt.Sprintf("part-%d.json", i+1))
		_ = a.debugfs(part, "dump /trails/0/.pvr/json "+state)
		if info, err := os.Stat(state); err == nil && info.Size() > 0 {
			full.root = i
			if err := os.Rename(part, filepath.Join(a.work, "storage.ext4")); err != nil {
				return err
			}
			if err := os.Rename(state, filepath.Join(a.base, "state.json")); err != nil {
				return err
			}
			break
		}
		os.Remove(part)
	}
	if full.root < 0 {
		return errors.New("the full image has no Pantavisor storage partition (an ext4 holding trails/0): is it a flashable Pantavisor image?")
	}
	storage := filepath.Join(a.work, "storage.ext4")
	stats, err := a.debugfsOutput(storage, "stats")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(stats, "\n") {
		if v, ok := strings.CutPrefix(line, "Filesystem volume name:"); ok {
			if full.label = strings.TrimSpace(v); full.label == "<none>" {
				full.label = ""
			}
		}
	}
	trail, err := a.debugfsOutput(storage, "ls -p /trails/0")
	if err != nil {
		return err
	}
	uboot := false
	for _, e := range lsEntries(trail) {
		if e != ".pv" {
			continue
		}
		full.pvDir = true
		links, err := a.debugfsOutput(storage, "ls -p /trails/0/.pv")
		if err != nil {
			return err
		}
		uboot = len(lsEntries(links)) > 0
	}
	if err := a.dumpStorage(storage); err != nil {
		return err
	}
	if err := copyNew(filepath.Join(a.rootfs, "config", "pantahub.config"), filepath.Join(a.base, "pantahub.config")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("the storage's pantahub.config: %w", err)
	}
	a.full = full
	// The storage is kept as it was found: the trail's .pv/ boot links
	// (U-Boot boards load the kernel and initrd from them) are made again
	// as writeBootFiles makes them for a uboot layout.
	bootloader := "full"
	if uboot {
		bootloader = "uboot"
	}
	a.layout = &Layout{Machine: strings.TrimSuffix(filepath.Base(a.Base), ".tar.gz"), Bootloader: bootloader, Table: full.table, Align: 1 << 20}
	p := full.parts[full.root]
	fmt.Fprintf(a.Log, "Full image, %s table, storage partition %d (label %q, %d MiB at %d MiB)\n", full.table, full.root+1, full.label, p.size>>20, p.start>>20)
	return nil
}

// extractWic decompresses the full image's disk image into dst.
func (a *assembler) extractWic(dst string) error {
	f, err := os.Open(a.Base)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = bufio.NewReaderSize(f, 1<<16)
	head, _ := r.(*bufio.Reader).Peek(512)
	if bytes.HasPrefix(head, gzipMagic) {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		defer zr.Close()
		br := bufio.NewReaderSize(zr, 1<<16)
		r = br
		head, _ = br.Peek(512)
		if !isTar(head) {
			return a.decompress(r, "", dst)
		}
	}
	if isTar(head) {
		tr := tar.NewReader(r)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				return errors.New("it holds no .wic disk image")
			}
			if err != nil {
				return err
			}
			if c, ok := wicName(h.Name); ok && h.Typeflag == tar.TypeReg {
				fmt.Fprintf(a.Log, "Disk image %s\n", filepath.Base(h.Name))
				return a.decompress(tr, c, dst)
			}
		}
	}
	switch {
	case bytes.HasPrefix(head, bzip2Magic):
		return a.decompress(r, "bz2", dst)
	case bytes.HasPrefix(head, xzMagic):
		return a.decompress(r, "xz", dst)
	}
	return a.decompress(r, "", dst)
}

// decompress writes r, compressed with compression ("", gz, bz2 or xz), to
// dst, at most maxFullImageBytes.
func (a *assembler) decompress(r io.Reader, compression, dst string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	limited := func(src io.Reader) error {
		n, err := io.Copy(out, io.LimitReader(src, maxFullImageBytes+1))
		if err == nil && n > maxFullImageBytes {
			err = fmt.Errorf("the disk image exceeds %d bytes", maxFullImageBytes)
		}
		return err
	}
	tool := map[string]string{"bz2": "bzip2", "xz": "xz"}[compression]
	switch {
	case compression == "":
		err = limited(r)
	case compression == "gz":
		var zr *gzip.Reader
		if zr, err = gzip.NewReader(r); err == nil {
			err = limited(zr)
		}
	case tool != "" && hasTool(tool):
		cmd := exec.Command(tool, "-dc")
		cmd.Stdin = r
		cmd.Stderr = a.Log
		var stdout io.ReadCloser
		if stdout, err = cmd.StdoutPipe(); err != nil {
			return err
		}
		if err = cmd.Start(); err != nil {
			return err
		}
		err = limited(stdout)
		if werr := cmd.Wait(); err == nil && werr != nil {
			err = fmt.Errorf("%s: %w", tool, werr)
		}
	case compression == "bz2":
		err = limited(bzip2.NewReader(r))
	default:
		return fmt.Errorf("%s is needed to read a .wic.%s", tool, compression)
	}
	if err != nil {
		return err
	}
	return out.Close()
}

func hasTool(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// readPartitions reads an msdos or gpt partition table.
func readPartitions(r io.ReaderAt) (string, []diskPart, error) {
	mbr := make([]byte, sector)
	if _, err := r.ReadAt(mbr, 0); err != nil {
		return "", nil, err
	}
	if !isMBR(mbr) {
		return "", nil, errors.New("no partition table")
	}
	var parts []diskPart
	for i := 0; i < 4; i++ {
		e := mbr[446+16*i : 446+16*(i+1)]
		kind := e[4]
		start := int64(binary.LittleEndian.Uint32(e[8:])) * sector
		size := int64(binary.LittleEndian.Uint32(e[12:])) * sector
		if kind == 0xee {
			return readGPTTable(r)
		}
		if kind == 0 || size == 0 {
			continue
		}
		if kind == 0x05 || kind == 0x0f || kind == 0x85 {
			// Logical partitions: Pantavisor's storage is a primary one.
			continue
		}
		parts = append(parts, diskPart{start: start, size: size, linux: kind == 0x83, entry: int64(446 + 16*i)})
	}
	if len(parts) == 0 {
		return "", nil, errors.New("the partition table is empty")
	}
	return "msdos", parts, nil
}

func readGPTTable(r io.ReaderAt) (string, []diskPart, error) {
	header := make([]byte, sector)
	if _, err := r.ReadAt(header, sector); err != nil {
		return "", nil, err
	}
	if string(header[:8]) != "EFI PART" {
		return "", nil, errors.New("the gpt header is missing")
	}
	entriesLBA := int64(binary.LittleEndian.Uint64(header[72:]))
	count := int64(binary.LittleEndian.Uint32(header[80:]))
	entrySize := int64(binary.LittleEndian.Uint32(header[84:]))
	if count <= 0 || count > 1024 || entrySize < 128 || entrySize > 4096 {
		return "", nil, errors.New("the gpt header is not valid")
	}
	entries := make([]byte, count*entrySize)
	if _, err := r.ReadAt(entries, entriesLBA*sector); err != nil {
		return "", nil, err
	}
	var parts []diskPart
	for i := int64(0); i < count; i++ {
		e := entries[i*entrySize : (i+1)*entrySize]
		var kind [16]byte
		copy(kind[:], e[:16])
		if kind == ([16]byte{}) {
			continue
		}
		first := int64(binary.LittleEndian.Uint64(e[32:]))
		last := int64(binary.LittleEndian.Uint64(e[40:]))
		if last < first {
			return "", nil, errors.New("a gpt entry is not valid")
		}
		parts = append(parts, diskPart{start: first * sector, size: (last - first + 1) * sector, linux: kind == gptLinux})
	}
	return "gpt", parts, nil
}

// copyRange copies size bytes at offset of src to the new file dst.
func copyRange(src, dst string, offset, size int64, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	n, err := io.Copy(out, io.NewSectionReader(in, offset, size))
	if err != nil {
		return err
	}
	if n != size {
		return errors.New("a partition reaches past the end of the disk image")
	}
	return out.Close()
}

func (a *assembler) debugfs(file, request string) error {
	cmd := exec.Command("debugfs", "-R", request, file)
	cmd.Env = a.env
	return cmd.Run()
}

func (a *assembler) debugfsOutput(file, request string) (string, error) {
	cmd := exec.Command("debugfs", "-R", request, file)
	cmd.Env = a.env
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("debugfs %s: %w", request, err)
	}
	return string(out), nil
}

// lsEntries are the names of debugfs 'ls -p' lines: /inode/mode/uid/gid/name/size/.
func lsEntries(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "/")
		if len(fields) >= 7 && fields[0] == "" && fields[5] != "." && fields[5] != ".." {
			names = append(names, fields[5])
		}
	}
	return names
}

// dumpStorage takes the storage's files, but its trails (written again
// from the factory revision) and lost+found, into the rootfs. Only
// directories and regular files: a link or a device node could make the
// steps after write outside the work directory.
func (a *assembler) dumpStorage(storage string) error {
	top, err := a.debugfsOutput(storage, "ls -p /")
	if err != nil {
		return err
	}
	dump := filepath.Join(a.work, "storage")
	if err := os.Mkdir(dump, 0o700); err != nil {
		return err
	}
	for _, name := range lsEntries(top) {
		if name == "trails" || name == "lost+found" {
			continue
		}
		if !baseFile(name) || strings.Contains(name, "/") {
			return fmt.Errorf("storage entry %q is not a plain name", name)
		}
		// rdump cannot give files to root when not run by root: it says
		// so for each one, and mkfs makes every file root's again.
		_ = a.debugfs(storage, "rdump /"+name+" "+dump)
		if _, err := os.Lstat(filepath.Join(dump, name)); err != nil {
			return fmt.Errorf("storage entry %s could not be read: %w", name, err)
		}
	}
	err = filepath.Walk(dump, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			rel, _ := filepath.Rel(dump, path)
			return fmt.Errorf("storage entry %s is neither a file nor a directory", rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dump)
	if err != nil {
		return err
	}
	for _, e := range entries {
		dst := filepath.Join(a.rootfs, e.Name())
		// The directories run made, empty.
		if info, err := os.Lstat(dst); err == nil && info.IsDir() {
			if err := os.Remove(dst); err != nil {
				return fmt.Errorf("storage entry %s: %w", e.Name(), err)
			}
		}
		if err := os.Rename(filepath.Join(dump, e.Name()), dst); err != nil {
			return err
		}
	}
	return nil
}

// deployFull makes the repository of the full image's factory revision,
// from the storage's objects.
func (a *assembler) deployFull() error {
	if err := a.pvr("init", "--objects", a.objects); err != nil {
		return err
	}
	state, err := os.ReadFile(filepath.Join(a.base, "state.json"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(a.repo, ".pvr", "json"), state, 0o644); err != nil {
		return err
	}
	return a.pvr("checkout")
}

// writeFullImage writes the full image again with the new storage
// partition: grown when it is the last partition of an msdos table, else
// in its place, which it must fit.
func (a *assembler) writeFullImage() error {
	full := a.full
	root := full.parts[full.root]
	last := true
	for _, p := range full.parts {
		if p.start > root.start {
			last = false
		}
	}
	growable := last && full.table == "msdos"
	if full.pvDir {
		if err := os.MkdirAll(filepath.Join(a.rootfs, "trails", "0", ".pv"), 0o755); err != nil {
			return err
		}
	}
	file, size, err := a.mkfsSized(Partition{Label: full.label}, root.size)
	if err != nil {
		return err
	}
	if size > root.size && !growable {
		return fmt.Errorf("the factory revision needs a %d MiB storage partition; the full image's is %d MiB and cannot grow (it is not the last partition of an msdos table)", size>>20, root.size>>20)
	}
	img := strings.TrimSuffix(a.Output, ".xz")
	if strings.HasSuffix(a.Output, ".xz") {
		img = filepath.Join(a.work, "image.img")
	}
	if err := copyRange(full.wic, img, 0, fileSize(full.wic), 0o644); err != nil {
		return err
	}
	out, err := os.OpenFile(img, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer out.Close()
	if err := copyAt(out, file, root.start, size); err != nil {
		return fmt.Errorf("storage partition: %w", err)
	}
	if size > root.size {
		if err := out.Truncate(root.start + size); err != nil {
			return err
		}
		var sectors [4]byte
		binary.LittleEndian.PutUint32(sectors[:], uint32(size/sector))
		if _, err := out.WriteAt(sectors[:], root.entry+12); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.Log, "Partition %d (storage): %d MiB at %d MiB, the others as the full image has them\n", full.root+1, size>>20, root.start>>20)
	if err := out.Close(); err != nil {
		return err
	}
	return a.finishImage(img)
}

func fileSize(file string) int64 {
	info, err := os.Stat(file)
	if err != nil {
		return 0
	}
	return info.Size()
}
