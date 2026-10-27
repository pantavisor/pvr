// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package image

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func archive(t *testing.T, entries ...*newcEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, e := range append(entries, &newcEntry{name: newcTrailer}) {
		if err := writeNewc(&b, e); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func entry(ino uint32, name string, mode uint32, data string) *newcEntry {
	e := &newcEntry{name: name, data: []byte(data)}
	e.fields[0], e.fields[1], e.fields[4] = ino, mode, 1
	return e
}

func readAll(t *testing.T, data []byte) map[string]*newcEntry {
	t.Helper()
	out := map[string]*newcEntry{}
	r := bytes.NewReader(data)
	for {
		e, err := readNewc(r)
		if err != nil {
			t.Fatal(err)
		}
		if e.name == newcTrailer {
			return out
		}
		out[e.name] = e
	}
}

func TestRewriteNewcKeepsWhatItDoesNotEdit(t *testing.T) {
	console := entry(4, "dev/console", 0o020600, "")
	console.fields[2], console.fields[3], console.fields[9], console.fields[10] = 0, 5, 5, 1
	in := archive(t,
		entry(1, ".", modeDir|0o755, ""),
		entry(2, "dev", modeDir|0o755, ""),
		console,
		entry(5, "etc", modeDir|0o755, ""),
		entry(6, "etc/pantavisor.config", modeFile|0o644, "PV_OEM_NAME=pv-oem-developer-001\n# comment\nPV_STORAGE_DEVICE=LABEL=root\n"),
		entry(7, "etc/pantavisor", modeDir|0o755, ""),
		entry(8, "etc/pantavisor/pvs", modeDir|0o755, ""),
		entry(9, "etc/pantavisor/pvs/trust", modeDir|0o755, ""),
		entry(10, "etc/pantavisor/pvs/trust/ca-certificates.crt", modeFile|0o644, "default"),
		entry(11, "init", modeFile|0o755, "#!/bin/sh\n"),
	)
	var out bytes.Buffer
	err := RewriteNewc(bytes.NewReader(in), &out, map[string]Edit{
		"/etc/pantavisor/pvs/trust/ca-certificates.crt": Replace([]byte("identity")),
		"etc/pantavisor.config":                         SetKeys(map[string]string{"PV_OEM_NAME": "acme", "PV_POLICY": "green"}),
		"etc/pantavisor/policies/green.config":          Replace([]byte("# green\n")),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := readAll(t, out.Bytes())
	if got["dev/console"].fields != console.fields {
		t.Fatalf("device node changed: %v", got["dev/console"].fields)
	}
	if string(got["init"].data) != "#!/bin/sh\n" || got["init"].mode() != modeFile|0o755 {
		t.Fatal("init changed")
	}
	if string(got["etc/pantavisor/pvs/trust/ca-certificates.crt"].data) != "identity" {
		t.Fatal("trust not replaced")
	}
	if config := string(got["etc/pantavisor.config"].data); config != "PV_OEM_NAME=acme\n# comment\nPV_STORAGE_DEVICE=LABEL=root\nPV_POLICY=green\n" {
		t.Fatalf("config: %q", config)
	}
	dir, policy := got["etc/pantavisor/policies"], got["etc/pantavisor/policies/green.config"]
	if dir == nil || dir.mode() != modeDir|0o755 || policy == nil || string(policy.data) != "# green\n" || policy.mode() != modeFile|0o644 {
		t.Fatal("policy file and its directory not added")
	}
	if dir.fields[0] <= 11 || policy.fields[0] <= dir.fields[0] {
		t.Fatal("added entries reuse inode numbers")
	}

	// cpio itself reads the result.
	if _, err := exec.LookPath("cpio"); err == nil {
		cmd := exec.Command("cpio", "-t", "--quiet")
		cmd.Stdin = bytes.NewReader(out.Bytes())
		list, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(list), "etc/pantavisor/policies/green.config") {
			t.Fatalf("cpio lists %s", list)
		}
	}
}

func TestRewriteNewcRefusesHardLinksAndNonFiles(t *testing.T) {
	link := entry(3, "etc/a", modeFile|0o644, "x")
	link.fields[4] = 2
	in := archive(t, link, entry(4, "etc/dir", modeDir|0o755, ""))
	for name, want := range map[string]string{"etc/a": "hard link", "etc/dir": "not a regular file"} {
		err := RewriteNewc(bytes.NewReader(in), io.Discard, map[string]Edit{name: Replace(nil)})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestSetKeysValues(t *testing.T) {
	out, err := SetKeys(map[string]string{"creds.host": "api.stage.pantahub.com"})([]byte("creds.host=api.pantahub.com\ncreds.port=443\n"))
	if err != nil || string(out) != "creds.host=api.stage.pantahub.com\ncreds.port=443\n" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := SetKeys(map[string]string{"K": "a\nB=b"})(nil); err == nil {
		t.Fatal("a value with a newline is accepted")
	}
}

type fileAt struct{ b []byte }

func (f *fileAt) WriteAt(p []byte, off int64) (int, error) {
	copy(f.b[off:], p)
	return len(p), nil
}

func TestWriteMBR(t *testing.T) {
	f := &fileAt{b: make([]byte, sector)}
	err := writeMBR(f, []mbrEntry{
		{start: 4 << 20, size: 32 << 20, kind: mbrKind("vfat"), active: true},
		{start: 36 << 20, size: 128 << 20, kind: mbrKind("ext4")},
	}, 0x1234)
	if err != nil {
		t.Fatal(err)
	}
	b := f.b
	if b[510] != 0x55 || b[511] != 0xaa || binary.LittleEndian.Uint32(b[440:]) != 0x1234 {
		t.Fatal("bad signature")
	}
	if b[446] != 0x80 || b[450] != 0x0c || binary.LittleEndian.Uint32(b[454:]) != 8192 || binary.LittleEndian.Uint32(b[458:]) != 65536 {
		t.Fatalf("bad first entry % x", b[446:462])
	}
	if b[462] != 0 || b[466] != 0x83 || binary.LittleEndian.Uint32(b[470:]) != 73728 {
		t.Fatalf("bad second entry % x", b[462:478])
	}
	if sfdisk, err := exec.LookPath("sfdisk"); err == nil {
		img := filepath.Join(t.TempDir(), "disk.img")
		if err := os.WriteFile(img, b, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(img, 200<<20); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(sfdisk, "--json", img).Output()
		if err != nil {
			t.Fatal(err)
		}
		var table struct {
			Partitiontable struct {
				Label      string `json:"label"`
				Partitions []struct {
					Start int64  `json:"start"`
					Size  int64  `json:"size"`
					Type  string `json:"type"`
					Boot  bool   `json:"bootable"`
				} `json:"partitions"`
			} `json:"partitiontable"`
		}
		if err := json.Unmarshal(out, &table); err != nil {
			t.Fatal(err)
		}
		p := table.Partitiontable.Partitions
		if table.Partitiontable.Label != "dos" || len(p) != 2 || p[0].Start != 8192 || !p[0].Boot || p[1].Type != "83" {
			t.Fatalf("sfdisk reads %s", out)
		}
	}
}

func TestLayoutValidation(t *testing.T) {
	good := `{"version":1,"machine":"raspberrypi","bootloader":"rpiab","table":"msdos","align":4194304,
	"partitions":[{"name":"bootsel","fs":"vfat","size":33554432,"active":true,"source":"bootsel.vfat"},
	{"name":"boot_a","fs":"vfat","size":134217728,"source":"bsp:bsp/pantavisor-rpi.img.gz"},
	{"name":"root","fs":"ext4","label":"root","source":"pvroot"}],
	"initrd":{"bsp":"bsp/pantavisor-rpi.img.gz","path":"pantavisor"},"containers":["containers/a.pvrexport.tgz"]}`
	var l Layout
	if err := json.Unmarshal([]byte(good), &l); err != nil {
		t.Fatal(err)
	}
	if err := l.validate(); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]func(*Layout){
		"escaping source":     func(l *Layout) { l.Partitions[0].Source = "../etc/passwd" },
		"absolute source":     func(l *Layout) { l.Partitions[0].Source = "/etc/passwd" },
		"no pvroot":           func(l *Layout) { l.Partitions[2].Source = "root.ext4" },
		"other loader":        func(l *Layout) { l.Bootloader = "uboot" },
		"odd size":            func(l *Layout) { l.Partitions[1].Size = 1000 },
		"escaping export":     func(l *Layout) { l.Containers = []string{"../x"} },
		"oversized partition": func(l *Layout) { l.Partitions[1].Size = 17 << 30 },
		"oversized total":     func(l *Layout) { l.Partitions[0].Size = 10 << 30; l.Partitions[1].Size = 10 << 30 },
		"space in source":     func(l *Layout) { l.Partitions[0].Source = "boot sel.vfat" },
		"newline in source":   func(l *Layout) { l.Partitions[0].Source = "boot\nsel.vfat" },
		"quote in source":     func(l *Layout) { l.Partitions[0].Source = "boot\"sel.vfat" },
	} {
		copy := l
		copy.Partitions = append([]Partition{}, l.Partitions...)
		bad(&copy)
		if copy.validate() == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestFactoryLayoutValidation(t *testing.T) {
	sha := strings.Repeat("a", 64)
	l := Layout{Version: 1, Bootloader: "rpiab", Table: "msdos", Align: 4 << 20,
		Partitions: []Partition{{Name: "boot", FS: "vfat", Size: 1 << 20, Source: "boot.vfat"}, {Name: "root", FS: "ext4", Label: "root", Source: "pvroot"}}}
	l.Initrd.BSP, l.Initrd.Path = "bsp/pantavisor-rpi.img.gz", "pantavisor"
	l.Factory = &FactoryInfo{Identity: "acme", Policy: "green", SecureBoot: "strict", PlatformSHA256: sha, OEMSHA256: sha, Base: sha}
	if err := l.validate(); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]func(*FactoryInfo){
		"bad identity":  func(f *FactoryInfo) { f.Identity = "../x" },
		"bad policy":    func(f *FactoryInfo) { f.Policy = "a/b" },
		"bad mode":      func(f *FactoryInfo) { f.SecureBoot = "on" },
		"bad trust sha": func(f *FactoryInfo) { f.PlatformSHA256 = "x" },
	} {
		f := *l.Factory
		bad(&f)
		c := l
		c.Factory = &f
		if c.validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	c := l
	c.Containers = []string{"containers/a.pvrexport.tgz"}
	if c.validate() == nil {
		t.Error("a factory listing unsigned containers accepted")
	}
}

func TestCheckoutTrail(t *testing.T) {
	sha := strings.Repeat("a", 64)
	state := `{"#spec":"pantavisor-service-system@1","app/run.json":{"name":"app","volumes":[]},"app/root.squashfs":"` + sha + `","bsp/src.json":"` + sha + `","_sigs/app.json":{"#spec":"pvs@2","protected":"x"}}`
	objects := t.TempDir()
	if err := os.WriteFile(filepath.Join(objects, sha), []byte("object"), 0o644); err != nil {
		t.Fatal(err)
	}
	object, _ := os.Stat(filepath.Join(objects, sha))
	dir := t.TempDir()
	if err := checkoutTrail(dir, []byte(state), objects); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"app/run.json": `{"name":"app","volumes":[]}`, "_sigs/app.json": `{"#spec":"pvs@2","protected":"x"}`} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != want || !strings.Contains(state, string(got)) {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	for _, name := range []string{"app/root.squashfs", "bsp/src.json"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || !os.SameFile(info, object) {
			t.Errorf("%s: not linked to its object: %v", name, err)
		}
	}
	// The parent directory's own block makes up for the object counted once.
	both, _, _ := dirSize(filepath.Dir(dir))
	inObjects, _, _ := dirSize(objects)
	inTrail, _, _ := dirSize(dir)
	if both != inObjects+inTrail {
		t.Errorf("hard links counted twice: %d, not %d+%d", both, inObjects, inTrail)
	}
	if checkoutTrail(dir, []byte(`{"../x.json":{}}`), objects) == nil {
		t.Error("a document outside the trail is written")
	}
}

func TestSetGroupTimeouts(t *testing.T) {
	a := &assembler{Options: Options{Log: io.Discard}, repo: t.TempDir()}
	device := `{"groups":[{"name":"root","status_goal":"STARTED","timeout":30},{"name":"app","timeout":30}],"volumes":{}}`
	file := filepath.Join(a.repo, "device.json")
	if err := os.WriteFile(file, []byte(device), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.setGroupTimeouts(); err != nil {
		t.Fatalf("no timeouts: %v", err)
	}
	if got, _ := os.ReadFile(file); string(got) != device {
		t.Error("device.json rewritten without timeouts to set")
	}
	a.GroupTimeouts = map[string]int{"root": 120}
	if err := a.setGroupTimeouts(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Groups []struct {
			Name       string `json:"name"`
			StatusGoal string `json:"status_goal"`
			Timeout    int    `json:"timeout"`
		} `json:"groups"`
		Volumes map[string]interface{} `json:"volumes"`
	}
	data, _ := os.ReadFile(file)
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 2 || got.Groups[0].Timeout != 120 || got.Groups[0].StatusGoal != "STARTED" || got.Groups[1].Timeout != 30 || got.Volumes == nil {
		t.Errorf("device.json: %s", data)
	}
	a.GroupTimeouts = map[string]int{"platfrom": 120}
	if a.setGroupTimeouts() == nil {
		t.Error("a group device.json does not declare is accepted")
	}
}

func TestGroupTimeoutsValidated(t *testing.T) {
	base := filepath.Join(t.TempDir(), "base.tar")
	if err := os.WriteFile(base, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, timeouts := range []map[string]int{{"root": 0}, {"root": 86401}, {"../x": 10}} {
		a := &assembler{Options: Options{Base: base, Output: "x.img", GroupTimeouts: timeouts}}
		if err := a.settle(); err == nil || !strings.Contains(err.Error(), "group timeout") {
			t.Errorf("%v: %v", timeouts, err)
		}
	}
}

func TestBaseFileValidation(t *testing.T) {
	good := []string{
		"file.txt",
		"sub/dir/file.txt",
		"pantavisor-rpi.img.gz",
	}
	for _, f := range good {
		if !baseFile(f) {
			t.Errorf("baseFile(%q) = false, want true", f)
		}
	}
	bad := []string{
		"",
		".",
		"..",
		"../file",
		"/etc/passwd",
		"file\nname",
		"file\rname",
		"file\tname",
		"file name",
		"file\"name",
		"file'name",
		"file`name",
		"file\x00name",
		"file\x1fname",
		"file\x7fname",
	}
	for _, f := range bad {
		if baseFile(f) {
			t.Errorf("baseFile(%q) = true, want false", f)
		}
	}
}

func TestUnpackBaseRejectsNegativeSize(t *testing.T) {
	header := make([]byte, 512)
	copy(header[0:], "bad.txt")
	copy(header[100:], "0000644\x00")
	copy(header[108:], "0000000\x00")
	copy(header[116:], "0000000\x00")
	// GNU negative size: 0xff in first byte
	header[124] = 0xff
	for i := 125; i < 136; i++ {
		header[i] = 0xff
	}
	header[156] = '0'
	copy(header[148:], "        ")
	var sum int64
	for _, b := range header {
		sum += int64(b)
	}
	copy(header[148:], fmt.Sprintf("%06o\x00 ", sum))

	base := filepath.Join(t.TempDir(), "base.tar")
	if err := os.WriteFile(base, header, 0o644); err != nil {
		t.Fatal(err)
	}

	a := &assembler{
		Options: Options{Base: base},
		base:    t.TempDir(),
	}
	if err := a.unpackBase(); err == nil {
		t.Fatal("unpackBase accepted negative file size")
	}
}
