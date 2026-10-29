// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package image

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gitlab.com/pantacor/pvr/libpvr"
	"gitlab.com/pantacor/pvr/utils/pvjson"
)

// Options of Assemble and Factory.
type Options struct {
	// Base is the image base tarball (pantavisor-image-base-<machine>.tar),
	// or a factory image Factory made of one: then the identity, policy,
	// secure boot mode and trust are the factory's, and no key is needed.
	Base string
	// Exports are merged into the factory revision after the base, in
	// order: apps, an OEM configuration part, a device's revision.
	Exports []string
	// Trust holds the identity's ca-certificates.crt and
	// ca-oem-certificates.crt, and optionally the pantavisor.config
	// 'pvr signing trust' writes, whose PV_OEM_NAME and PV_SECUREBOOT_MODE
	// are the defaults of OEMName and SecureBoot.
	Trust      string
	OEMName    string
	Policy     string
	SecureBoot string
	// PantahubHost and PantahubPort set creds.host and creds.port of the
	// storage's pantahub.config; empty keeps the base's.
	PantahubHost string
	PantahubPort int
	// GroupTimeouts sets the timeout, in seconds, of container groups of
	// the BSP's device.json, by group name, before the platform key signs
	// it: how long a group may take to reach its status goal before an
	// update rolls back. A factory image has them already.
	GroupTimeouts map[string]int
	// Key and X5c are the platform key and its certificate chain, which
	// ca-certificates.crt trusts: they sign the BSP and the base's
	// containers again. A factory image needs none.
	Key, X5c string
	// Output is the image, .img or .img.xz, or for Factory the factory
	// image, .tar; ExportOut, when set, receives the factory revision as a
	// pvr export, for updates that keep this image's BSP.
	Output    string
	ExportOut string
	// WorkDir is an empty directory to work in; empty makes a temporary one.
	WorkDir string
	// Pvr is the pvr executable that runs the repository steps; empty is
	// this one.
	Pvr string
	Log io.Writer
}

var (
	oemName      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	policyName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	secureBoot   = map[string]bool{"disabled": true, "lenient": true, "strict": true, "audit": true}
	pantahubHost = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)
	groupName    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	objectName   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

const (
	platformStore = "ca-certificates.crt"
	oemStore      = "ca-oem-certificates.crt"
	trustDir      = "etc/pantavisor/pvs/trust"
)

type assembler struct {
	Options
	factory                           bool
	work, base, repo, rootfs, objects string
	layout                            *Layout
	env                               []string
}

// Assemble makes the flashable image of opts, from an image base or a
// factory image.
func Assemble(opts Options) error { return run(opts, false) }

// Factory makes a factory image of opts: the image base with the
// identity's trust and PV_OEM_NAME, PV_POLICY and PV_SECUREBOOT_MODE in the
// initrd and its BSP and containers signed with the platform key. It holds
// no secret, and Assemble turns it into images in seconds: it adds the
// exports and writes the storage partition only.
func Factory(opts Options) error { return run(opts, true) }

type step struct {
	what string
	run  func() error
}

func run(opts Options, factory bool) (err error) {
	a := &assembler{Options: opts, factory: factory}
	if a.Log == nil {
		a.Log = io.Discard
	}
	if err := a.settle(); err != nil {
		return err
	}
	if a.work == "" {
		if a.work, err = os.MkdirTemp("", "pvr-image-"); err != nil {
			return err
		}
		// Whatever happens: the work holds rendered configuration.
		defer os.RemoveAll(a.work)
	}
	a.base = filepath.Join(a.work, "base")
	a.repo = filepath.Join(a.work, "repo")
	a.rootfs = filepath.Join(a.work, "rootfs")
	a.objects = filepath.Join(a.rootfs, "objects")
	for _, dir := range []string{a.base, a.repo, a.objects, filepath.Join(a.rootfs, "trails", "0", ".pvr"),
		filepath.Join(a.rootfs, "config"), filepath.Join(a.rootfs, "boot"), filepath.Join(a.rootfs, "logs"),
		filepath.Join(a.work, "pvrconfig")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	a.env = append(os.Environ(), "PVR_CONFIG_DIR="+filepath.Join(a.work, "pvrconfig"), "PVR_DISABLE_SELF_UPGRADE=true")

	if err := a.do(step{"Unpacking the image base", a.unpackBase}, step{"Settling the identity", a.settleIdentity}); err != nil {
		return err
	}
	steps := []step{{"Deploying the base's BSP and containers", a.deployBase}}
	if a.layout.Factory == nil {
		steps = append(steps,
			step{"Putting the identity's trust and configuration in the initrd", a.remaster},
			step{"Setting the container groups' timeouts", a.setGroupTimeouts},
			step{"Signing the BSP and the base's containers with the platform key", a.signBase})
	}
	if factory {
		steps = append(steps,
			step{"Verifying the factory revision as a device will", a.verify},
			step{"Writing the factory image", a.writeFactory})
	} else {
		steps = append(steps,
			step{"Merging the exports", a.mergeExports},
			step{"Verifying the factory revision as a device will", a.verify},
			step{"Writing the storage partition", a.writeRootfs},
			step{"Writing the image", a.writeImage})
	}
	return a.do(steps...)
}

func (a *assembler) do(steps ...step) error {
	for _, s := range steps {
		fmt.Fprintf(a.Log, "==> %s\n", s.what)
		if err := s.run(); err != nil {
			return err
		}
	}
	return nil
}

func (a *assembler) settle() error {
	// The steps run in their own directories.
	for _, p := range []*string{&a.Base, &a.Trust, &a.Key, &a.X5c, &a.Output, &a.ExportOut, &a.WorkDir} {
		if *p == "" {
			continue
		}
		abs, err := filepath.Abs(*p)
		if err != nil {
			return err
		}
		*p = abs
	}
	for i := range a.Exports {
		abs, err := filepath.Abs(a.Exports[i])
		if err != nil {
			return err
		}
		a.Exports[i] = abs
	}
	a.work = a.WorkDir
	for _, f := range []string{a.Base, a.Key, a.X5c} {
		if info, err := os.Stat(f); f != "" && (err != nil || !info.Mode().IsRegular()) {
			return fmt.Errorf("%s is not a file", f)
		}
	}
	if a.Base == "" {
		return errors.New("an image base or factory image is needed")
	}
	for _, f := range a.Exports {
		if info, err := os.Stat(f); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("export %s is not a file", f)
		}
	}
	if a.PantahubHost != "" && !pantahubHost.MatchString(a.PantahubHost) {
		return errors.New("the Pantahub host is a host name")
	}
	if a.PantahubPort < 0 || a.PantahubPort > 65535 {
		return errors.New("the Pantahub port is 1 to 65535")
	}
	for name, seconds := range a.GroupTimeouts {
		if !groupName.MatchString(name) || seconds < 1 || seconds > 86400 {
			return fmt.Errorf("group timeout %s=%d: a group name and 1 to 86400 seconds", name, seconds)
		}
	}
	switch {
	case a.factory && (!strings.HasSuffix(a.Output, ".tar") || a.ExportOut != "" || len(a.Exports) > 0):
		return errors.New("a factory image is written as .tar, without exports")
	case !a.factory && !strings.HasSuffix(a.Output, ".img") && !strings.HasSuffix(a.Output, ".img.xz"):
		return errors.New("the image is written as .img or .img.xz")
	}
	if a.Pvr == "" {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		a.Pvr = self
	}
	for _, tool := range []string{"mcopy", "mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s is needed (mtools, e2fsprogs)", tool)
		}
	}
	if strings.HasSuffix(a.Output, ".xz") {
		if _, err := exec.LookPath("xz"); err != nil {
			return errors.New("xz is needed to write .img.xz")
		}
	}
	return nil
}

// settleIdentity takes the identity, policy and trust from a factory
// image, refusing options that differ, or checks what a base needs.
func (a *assembler) settleIdentity() error {
	if f := a.layout.Factory; f != nil {
		if a.factory {
			return errors.New("the base is a factory image already")
		}
		if len(a.GroupTimeouts) > 0 {
			return errors.New("a factory image's device.json is signed already: set group timeouts when making it")
		}
		for _, c := range []struct{ name, given, factory string }{
			{"OEM name", a.OEMName, f.Identity}, {"policy", a.Policy, f.Policy}, {"secure boot mode", a.SecureBoot, f.SecureBoot},
		} {
			if c.given != "" && c.given != c.factory {
				return fmt.Errorf("the factory image's %s is %q, not %q: make a factory image for it", c.name, c.factory, c.given)
			}
		}
		a.OEMName, a.Policy, a.SecureBoot = f.Identity, f.Policy, f.SecureBoot
		if a.Trust == "" {
			a.Trust = filepath.Join(a.base, "trust")
		}
		for store, want := range map[string]string{platformStore: f.PlatformSHA256, oemStore: f.OEMSHA256} {
			got, err := fileSHA256(filepath.Join(a.Trust, store))
			if err != nil {
				return fmt.Errorf("trust %s: %w", store, err)
			}
			if got != want {
				return fmt.Errorf("%s is not the trust the factory image was made for (identity %s)", store, f.Identity)
			}
		}
		fmt.Fprintf(a.Log, "Factory image of %s, policy %q, secure boot %s\n", a.OEMName, a.Policy, a.SecureBoot)
		return nil
	}
	if a.Trust == "" || a.Key == "" || a.X5c == "" {
		return errors.New("an image base needs the identity's trust and its platform key and chain (--trust, --key, --x5c)")
	}
	for _, store := range []string{platformStore, oemStore} {
		if info, err := os.Stat(filepath.Join(a.Trust, store)); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not in %s", store, a.Trust)
		}
	}
	if defaults, err := os.ReadFile(filepath.Join(a.Trust, "pantavisor.config")); err == nil {
		values := keyValues(defaults)
		if a.OEMName == "" {
			a.OEMName = values["PV_OEM_NAME"]
		}
		if a.SecureBoot == "" {
			a.SecureBoot = values["PV_SECUREBOOT_MODE"]
		}
	}
	if a.SecureBoot == "" {
		a.SecureBoot = "strict"
	}
	if !oemName.MatchString(a.OEMName) {
		return errors.New("the OEM name (PV_OEM_NAME) is needed: the signing identity's name")
	}
	if a.Policy != "" && !policyName.MatchString(a.Policy) {
		return fmt.Errorf("policy %q is a PV_POLICY name: letters, digits, '_' or '-'", a.Policy)
	}
	if !secureBoot[a.SecureBoot] {
		return errors.New("secure boot mode is disabled, lenient, strict or audit")
	}
	return nil
}

func keyValues(data []byte) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && !strings.HasPrefix(k, "#") {
			values[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return values
}

func (a *assembler) command(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = a.env
	cmd.Stdout = a.Log
	cmd.Stderr = a.Log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", filepath.Base(name), strings.Join(args, " "), err)
	}
	return nil
}

func (a *assembler) pvr(args ...string) error { return a.command(a.repo, a.Pvr, args...) }

// unpackBase extracts the base's regular files: nothing else, nothing that
// escapes, nothing bigger than an image could be.
func (a *assembler) unpackBase() error {
	f, err := os.Open(a.Base)
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	var cumulativeBytes int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("image base: %w", err)
		}
		name := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(h.Name, "./")))
		switch h.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return fmt.Errorf("image base entry %s is not a regular file", h.Name)
		}
		if !baseFile(name) || h.Size < 0 || h.Size > 8<<30 {
			return fmt.Errorf("image base entry %s is not a file of the base", h.Name)
		}
		cumulativeBytes += h.Size
		if cumulativeBytes > maxImageBaseBytes {
			return fmt.Errorf("image base unpacked size exceeds cumulative limit of %d bytes", maxImageBaseBytes)
		}
		dst := filepath.Join(a.base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(tr, h.Size))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	a.layout, err = readLayout(filepath.Join(a.base, "layout.json"))
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Log, "Image base of %s, %s layout\n", a.layout.Machine, a.layout.Bootloader)
	return nil
}

func (a *assembler) deployBase() error {
	if err := a.pvr("init", "--objects", a.objects); err != nil {
		return err
	}
	exports := append([]string{"bsp.pvrexport.tgz"}, a.layout.Containers...)
	for _, e := range exports {
		if err := a.pvr("get", filepath.Join(a.base, filepath.FromSlash(e))); err != nil {
			return err
		}
	}
	return a.pvr("checkout")
}

// remaster rewrites the initrd, inside the BSP's boot vfat or the BSP file
// itself: the identity's trust, PV_OEM_NAME, PV_SECUREBOOT_MODE and
// PV_POLICY (with its policy file, which Pantavisor needs to boot).
func (a *assembler) remaster() error {
	bspFile := filepath.Join(a.repo, filepath.FromSlash(a.layout.Initrd.BSP))
	edits, err := a.initrdEdits()
	if err != nil {
		return err
	}
	if a.layout.Initrd.Path == "" {
		// Renamed over the BSP file, which may be a hard link to an object.
		remastered := filepath.Join(a.work, "initrd.new")
		if err := rewriteInitrd(bspFile, remastered, edits, gzip.BestCompression); err != nil {
			return fmt.Errorf("BSP %s: %w", a.layout.Initrd.BSP, err)
		}
		if err := os.Rename(remastered, bspFile); err != nil {
			return err
		}
	} else if err := a.remasterVfat(bspFile, edits); err != nil {
		return err
	}
	fmt.Fprintf(a.Log, "Initrd: PV_OEM_NAME=%s PV_SECUREBOOT_MODE=%s PV_POLICY=%s, trust of %s\n", a.OEMName, a.SecureBoot, a.Policy, a.Trust)
	return nil
}

// remasterVfat rewrites the initrd at the layout's path in the gzipped vfat
// bspFile, keeping the vfat as the boot partition.
func (a *assembler) remasterVfat(bspFile string, edits map[string]Edit) error {
	vfat := a.bootVfat()
	if err := gunzipFile(bspFile, vfat); err != nil {
		return fmt.Errorf("BSP %s: %w", a.layout.Initrd.BSP, err)
	}
	initrd := filepath.Join(a.work, "initrd")
	if err := a.command(a.work, "mcopy", "-n", "-i", vfat, "::"+a.layout.Initrd.Path, initrd); err != nil {
		return err
	}
	remastered := initrd + ".new"
	if err := rewriteInitrd(initrd, remastered, edits, gzip.DefaultCompression); err != nil {
		return err
	}
	if err := a.command(a.work, "mcopy", "-o", "-i", vfat, remastered, "::"+a.layout.Initrd.Path); err != nil {
		return err
	}
	return gzipFile(vfat, bspFile)
}

// initrdEdits are the initrd's files of the identity, policy and mode.
func (a *assembler) initrdEdits() (map[string]Edit, error) {
	platform, err := os.ReadFile(filepath.Join(a.Trust, platformStore))
	if err != nil {
		return nil, err
	}
	oem, err := os.ReadFile(filepath.Join(a.Trust, oemStore))
	if err != nil {
		return nil, err
	}
	config := map[string]string{"PV_OEM_NAME": a.OEMName, "PV_SECUREBOOT_MODE": a.SecureBoot}
	edits := map[string]Edit{
		trustDir + "/" + platformStore: Replace(platform),
		trustDir + "/" + oemStore:      Replace(oem),
	}
	if a.Policy != "" {
		config["PV_POLICY"] = a.Policy
		// Policies of the base (prod, test) keep their settings.
		edits["etc/pantavisor/policies/"+a.Policy+".config"] = func(old []byte) ([]byte, error) {
			if old != nil {
				return old, nil
			}
			return []byte("# PV_POLICY=" + a.Policy + ": settings of this policy, at Pantavisor's POLICY level\n"), nil
		}
	}
	edits["etc/pantavisor.config"] = SetKeys(config)
	return edits, nil
}

func (a *assembler) bootVfat() string { return filepath.Join(a.work, "boot.vfat") }

// rewriteInitrd writes the gzipped cpio src with edits to dst, gzipped at
// level.
func rewriteInitrd(src, dst string, edits map[string]Edit, level int) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	zr, err := gzip.NewReader(bufio.NewReader(in))
	if err != nil {
		return errors.New("the initrd is not a gzipped cpio archive (cpio.gz)")
	}
	defer zr.Close()
	zr.Multistream(false)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	zw, err := gzip.NewWriterLevel(out, level)
	if err != nil {
		return err
	}
	const maxInitrdBytes = 1 << 30 // 1 GiB
	lr := &io.LimitedReader{R: zr, N: maxInitrdBytes + 1}
	if err := RewriteNewc(lr, zw, edits); err != nil {
		return err
	}
	if lr.N <= 0 {
		return fmt.Errorf("decompressed initrd exceeds %d bytes limit", maxInitrdBytes)
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return out.Close()
}

func gunzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	zr, err := gzip.NewReader(bufio.NewReader(in))
	if err != nil {
		return err
	}
	defer zr.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	const maxGunzipBytes int64 = 16 << 30 // 16 GiB
	n, err := io.Copy(out, io.LimitReader(zr, maxGunzipBytes+1))
	if err != nil {
		return err
	}
	if n > maxGunzipBytes {
		return fmt.Errorf("decompressed file %s exceeds %d bytes limit", filepath.Base(src), maxGunzipBytes)
	}
	return out.Close()
}

// gzipFile writes src gzipped to a new file it renames over dst: dst may be
// a hard link to an object of the repository.
func gzipFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".pvr-image"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	zw, _ := gzip.NewWriterLevel(out, gzip.BestCompression)
	if _, err := io.Copy(zw, bufio.NewReader(in)); err != nil {
		out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func (a *assembler) state() (map[string]interface{}, error) {
	data, err := os.ReadFile(filepath.Join(a.repo, ".pvr", "json"))
	if err != nil {
		return nil, err
	}
	var state map[string]interface{}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return state, nil
}

func signatures(state map[string]interface{}) []string {
	var names []string
	for key := range state {
		if strings.HasPrefix(key, "_sigs/") && strings.HasSuffix(key, ".json") {
			names = append(names, strings.TrimSuffix(strings.TrimPrefix(key, "_sigs/"), ".json"))
		}
	}
	sort.Strings(names)
	return names
}

// ParseGroupTimeouts parses repeated NAME=SECONDS values, such as root=120,
// into GroupTimeouts.
func ParseGroupTimeouts(values []string) (map[string]int, error) {
	if len(values) == 0 {
		return nil, nil
	}
	timeouts := map[string]int{}
	for _, v := range values {
		name, seconds, ok := strings.Cut(v, "=")
		n, err := strconv.Atoi(seconds)
		if !ok || name == "" || err != nil {
			return nil, fmt.Errorf("group timeout %q is NAME=SECONDS, such as root=120", v)
		}
		timeouts[name] = n
	}
	return timeouts, nil
}

// setGroupTimeouts sets the timeouts of GroupTimeouts in the BSP's
// device.json, refusing a group it does not declare.
func (a *assembler) setGroupTimeouts() error {
	if len(a.GroupTimeouts) == 0 {
		return nil
	}
	file := filepath.Join(a.repo, "device.json")
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("the image base has no device.json to set group timeouts in: %w", err)
	}
	var device map[string]interface{}
	if err := json.Unmarshal(data, &device); err != nil {
		return fmt.Errorf("device.json: %w", err)
	}
	groups, _ := device["groups"].([]interface{})
	set := map[string]bool{}
	for _, g := range groups {
		group, ok := g.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := group["name"].(string)
		if seconds, ok := a.GroupTimeouts[name]; ok {
			group["timeout"] = seconds
			set[name] = true
			fmt.Fprintf(a.Log, "Group %s: timeout %ds\n", name, seconds)
		}
	}
	for name := range a.GroupTimeouts {
		if !set[name] {
			return fmt.Errorf("device.json declares no group %q", name)
		}
	}
	if data, err = json.Marshal(device); err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}

// signBase signs again every signature of the base (the BSP, its skeleton
// and the base's containers) with the platform key, by its own match rule.
func (a *assembler) signBase() error {
	if err := a.pvr("add", "."); err != nil {
		return err
	}
	if err := a.pvr("commit"); err != nil {
		return err
	}
	state, err := a.state()
	if err != nil {
		return err
	}
	names := signatures(state)
	if len(names) == 0 {
		return errors.New("the image base's BSP is not signed: no match rules to sign it by")
	}
	args := append([]string{"sig", "--key", a.Key, "--x5c", a.X5c, "update"}, names...)
	if err := a.pvr(args...); err != nil {
		return err
	}
	if err := a.pvr("add", "."); err != nil {
		return err
	}
	return a.pvr("commit")
}

// writeFactory writes the factory image: the signed revision, the boot
// partition (with a vfat initrd), the base's other partition files and the
// identity's public trust. No key: the platform key never leaves this run.
func (a *assembler) writeFactory() error {
	export := filepath.Join(a.work, "factory.pvrexport.tgz")
	if err := a.pvr("export", export); err != nil {
		return err
	}
	info := &FactoryInfo{Identity: a.OEMName, Policy: a.Policy, SecureBoot: a.SecureBoot, GroupTimeouts: a.GroupTimeouts}
	var err error
	if info.PlatformSHA256, err = fileSHA256(filepath.Join(a.Trust, platformStore)); err != nil {
		return err
	}
	if info.OEMSHA256, err = fileSHA256(filepath.Join(a.Trust, oemStore)); err != nil {
		return err
	}
	if info.Base, err = fileSHA256(a.Base); err != nil {
		return err
	}
	layout := *a.layout
	layout.Containers, layout.Factory = nil, info
	layout.Partitions = append([]Partition{}, a.layout.Partitions...)
	files := map[string]string{
		"bsp.pvrexport.tgz":      export,
		"trust/" + platformStore: filepath.Join(a.Trust, platformStore),
		"trust/" + oemStore:      filepath.Join(a.Trust, oemStore),
	}
	if a.layout.Initrd.Path != "" {
		files["boot.vfat"] = a.bootVfat()
	}
	for i, p := range layout.Partitions {
		for _, src := range p.Files {
			files[src] = filepath.Join(a.base, filepath.FromSlash(src))
		}
		switch {
		case p.Source == sourceBSP+a.layout.Initrd.BSP:
			layout.Partitions[i].Source = "boot.vfat"
		case p.Source != sourcePvroot && !strings.HasPrefix(p.Source, sourceBSP):
			files[p.Source] = filepath.Join(a.base, filepath.FromSlash(p.Source))
		}
	}
	if _, err := os.Stat(filepath.Join(a.base, "pantahub.config")); err == nil {
		files["pantahub.config"] = filepath.Join(a.base, "pantahub.config")
	}
	data, err := json.MarshalIndent(layout, "", "  ")
	if err != nil {
		return err
	}
	if err := writeTar(a.Output, data, files); err != nil {
		return err
	}
	digest, err := fileSHA256(a.Output)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Log, "Factory image %s of %s, policy %q, sha256 %s\n", a.Output, a.OEMName, a.Policy, digest)
	return nil
}

// writeTar writes a tar of layout.json and files (archive name to path),
// root-owned and sorted.
func writeTar(output string, layout []byte, files map[string]string) error {
	out, err := os.Create(output)
	if err != nil {
		return err
	}
	defer out.Close()
	tw := tar.NewWriter(out)
	header := func(name string, size int64) error {
		return tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatPAX})
	}
	if err := header("layout.json", int64(len(layout))); err != nil {
		return err
	}
	if _, err := tw.Write(layout); err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := os.Open(files[name])
		if err != nil {
			return err
		}
		st, err := f.Stat()
		if err == nil {
			err = header(name, st.Size())
		}
		if err == nil {
			_, err = io.Copy(tw, f)
		}
		f.Close()
		if err != nil {
			return fmt.Errorf("factory image %s: %w", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return out.Close()
}

func (a *assembler) mergeExports() error {
	for _, e := range a.Exports {
		if err := a.pvr("get", e); err != nil {
			return err
		}
	}
	// pvr sig ls reads the signatures of the working copy.
	return a.pvr("checkout")
}

// verify checks every signature against the store a device picks for it
// (signature.c): the OEM store when its leaf is the OEM name and it covers
// neither bsp/** nor <oem>/**, the platform store otherwise; and, in strict
// mode, that signatures cover every key.
func (a *assembler) verify() error {
	state, err := a.state()
	if err != nil {
		return err
	}
	covered := map[string]bool{}
	for _, name := range signatures(state) {
		key := "_sigs/" + name + ".json"
		protected, excluded, err := libpvr.SignatureCoverage(state, key)
		if err != nil {
			return fmt.Errorf("signature %s: %w", name, err)
		}
		// A device counts what a signature excludes as covered too.
		for _, e := range excluded {
			covered[e] = true
		}
		oemSignable := true
		for _, p := range protected {
			covered[p] = true
			if strings.HasPrefix(p, "bsp/") || strings.HasPrefix(p, a.OEMName+"/") {
				oemSignable = false
			}
		}
		cn, err := leafCN(state[key])
		if err != nil {
			return fmt.Errorf("signature %s: %w", name, err)
		}
		store := platformStore
		if oemSignable && cn == a.OEMName {
			store = oemStore
		}
		fmt.Fprintf(a.Log, "Signature %s by %s, verified with %s\n", name, cn, store)
		if err := a.pvr("sig", "--cacerts", filepath.Join(a.Trust, store), "ls", name); err != nil {
			return fmt.Errorf("signature %s (by %s) does not verify with the identity's %s: a device refuses it", name, cn, store)
		}
	}
	if a.SecureBoot != "strict" {
		return nil
	}
	var missing []string
	for key := range state {
		if !strings.HasPrefix(key, "_sigs/") && !covered[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("strict secure boot refuses a revision with unsigned keys: %s", strings.Join(missing, ", "))
	}
	return nil
}

func leafCN(signature interface{}) (string, error) {
	sig, ok := signature.(map[string]interface{})
	if !ok {
		return "", errors.New("not a signature")
	}
	protected, _ := sig["protected"].(string)
	if protected == "" {
		if list, ok := sig["signatures"].([]interface{}); ok && len(list) > 0 {
			if first, ok := list[0].(map[string]interface{}); ok {
				protected, _ = first["protected"].(string)
			}
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(protected, "="))
	if err != nil {
		return "", errors.New("signature header is not base64")
	}
	var header struct {
		X5c []string `json:"x5c"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return "", errors.New("signature header is not JSON")
	}
	if len(header.X5c) == 0 {
		return "", errors.New("signature carries no certificate (x5c): a device verifies it with pub.pem only")
	}
	der, err := base64.StdEncoding.DecodeString(header.X5c[0])
	if err != nil {
		return "", errors.New("signature certificate is not base64")
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return "", err
	}
	return cert.Subject.CommonName, nil
}

// writeRootfs lays out the storage partition: the objects the factory
// revision names, its state, and the revision checked out in trails/0 as a
// Yocto image has it: JSON documents written, objects hard linked. Pantavisor
// refuses the revision when a JSON document is missing, and mounts volumes
// (dm-verity ones included) from trails/0 without linking them itself.
func (a *assembler) writeRootfs() error {
	if a.ExportOut != "" {
		if err := a.pvr("export", a.ExportOut); err != nil {
			return err
		}
	}
	state, err := a.state()
	if err != nil {
		return err
	}
	used := map[string]bool{}
	for _, v := range state {
		if s, ok := v.(string); ok && objectName.MatchString(s) {
			used[s] = true
		}
	}
	entries, err := os.ReadDir(a.objects)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !used[e.Name()] {
			if err := os.RemoveAll(filepath.Join(a.objects, e.Name())); err != nil {
				return err
			}
		}
	}
	for sha := range used {
		if _, err := os.Stat(filepath.Join(a.objects, sha)); err != nil {
			return fmt.Errorf("object %s of the factory revision is missing", sha)
		}
	}
	data, err := os.ReadFile(filepath.Join(a.repo, ".pvr", "json"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(a.rootfs, "trails", "0", ".pvr", "json"), data, 0o644); err != nil {
		return err
	}
	if err := checkoutTrail(filepath.Join(a.rootfs, "trails", "0"), data, a.objects); err != nil {
		return err
	}
	config, err := os.ReadFile(filepath.Join(a.base, "pantahub.config"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	overrides := map[string]string{}
	if a.PantahubHost != "" {
		overrides["creds.host"] = a.PantahubHost
	}
	if a.PantahubPort != 0 {
		overrides["creds.port"] = strconv.Itoa(a.PantahubPort)
	}
	if config, err = SetKeys(overrides)(config); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(a.rootfs, "config", "pantahub.config"), config, 0o644); err != nil {
		return err
	}
	return a.writeBootFiles()
}

// writeBootFiles copies the pvroot partition's files of the base into the
// storage and, for the uboot bootloader, the boot/uboot.txt Pantavisor
// keeps its revision in, at revision 0, unless the base has one, and the
// trail's .pv/ boot links U-Boot loads the kernel and initrd from.
func (a *assembler) writeBootFiles() error {
	for _, p := range a.layout.Partitions {
		if p.Source != sourcePvroot {
			continue
		}
		dsts := make([]string, 0, len(p.Files))
		for dst := range p.Files {
			dsts = append(dsts, dst)
		}
		sort.Strings(dsts)
		for _, dst := range dsts {
			if err := copyNew(filepath.Join(a.base, filepath.FromSlash(p.Files[dst])), filepath.Join(a.rootfs, filepath.FromSlash(dst))); err != nil {
				return fmt.Errorf("partition %s file %s: %w", p.Name, dst, err)
			}
			fmt.Fprintf(a.Log, "Partition %s: %s from the base's %s\n", p.Name, dst, p.Files[dst])
		}
	}
	if a.layout.Bootloader != "uboot" {
		return nil
	}
	// Hard links to the trail's BSP objects, as pvr deploy makes them: the
	// remastered initrd, signed already.
	trail := filepath.Join(a.rootfs, "trails", "0")
	if err := (&libpvr.Pvr{Dir: trail}).DeployPvLinks(); err != nil {
		return fmt.Errorf("boot links of the trail (.pv/): %w", err)
	}
	f, err := os.OpenFile(filepath.Join(a.rootfs, "boot", "uboot.txt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.WriteString("pv_rev=0\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// copyNew copies the regular file src to dst, which may not exist.
func copyNew(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if info, err := in.Stat(); err != nil || !info.Mode().IsRegular() {
		return errors.New("not a regular file of the base")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

func dirSize(dir string) (int64, int, error) {
	var size int64
	count := 0
	seen := map[int64][]os.FileInfo{} // by size: hard links are counted once
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		count++
		if info.Mode().IsRegular() {
			for _, other := range seen[info.Size()] {
				if os.SameFile(info, other) {
					return nil
				}
			}
			seen[info.Size()] = append(seen[info.Size()], info)
		}
		size += (info.Size() + 4095) / 4096 * 4096
		return nil
	})
	return size, count, err
}

// mkfs makes the ext4 storage of the pvroot, owned by root whoever runs it.
func (a *assembler) mkfs(p Partition) (string, int64, error) {
	content, files, err := dirSize(a.rootfs)
	if err != nil {
		return "", 0, err
	}
	size := alignUp(content+content/4+int64(files)*8192+64<<20, a.layout.Align)
	file := filepath.Join(a.work, "root.ext4")
	if err := a.command(a.work, "mkfs.ext4", "-q", "-F", "-L", p.Label, "-E", "root_owner=0:0", "-d", a.rootfs, file, strconv.FormatInt(size/1024, 10)+"k"); err != nil {
		return "", 0, err
	}
	var script bytes.Buffer
	err = filepath.Walk(a.rootfs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(a.rootfs, path)
		if err != nil || rel == "." {
			return err
		}
		for _, r := range rel {
			if isDisallowedPathChar(r) {
				return fmt.Errorf("rootfs path %q contains control characters, whitespace, or quotes", rel)
			}
		}
		fmt.Fprintf(&script, "set_inode_field /%s uid 0\nset_inode_field /%[1]s gid 0\n", filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	cmds := filepath.Join(a.work, "root.debugfs")
	if err := os.WriteFile(cmds, script.Bytes(), 0o644); err != nil {
		return "", 0, err
	}
	if err := a.command(a.work, "debugfs", "-w", "-f", cmds, file); err != nil {
		return "", 0, err
	}
	return file, size, nil
}

func (a *assembler) writeImage() error {
	img := strings.TrimSuffix(a.Output, ".xz")
	if strings.HasSuffix(a.Output, ".xz") {
		img = filepath.Join(a.work, "image.img")
	}
	out, err := os.Create(img)
	if err != nil {
		return err
	}
	defer out.Close()
	gpt := a.layout.Table == "gpt"
	var entries []mbrEntry
	var gptEntries []gptEntry
	sum := sha256.Sum256([]byte(a.OEMName + "\x00" + a.Policy + "\x00" + a.Output))
	offset := a.layout.Align
	if gpt {
		// After the gpt table's header and entries.
		offset = alignUp((1+gptSectors)*sector, a.layout.Align)
	}
	for _, p := range a.layout.Partitions {
		src, size := "", p.Size
		switch {
		case p.Source == sourcePvroot:
			if src, size, err = a.mkfs(p); err != nil {
				return err
			}
		case strings.HasPrefix(p.Source, sourceBSP):
			name := strings.TrimPrefix(p.Source, sourceBSP)
			if name != a.layout.Initrd.BSP {
				src = filepath.Join(a.work, "part-"+p.Name)
				if err := gunzipFile(filepath.Join(a.repo, filepath.FromSlash(name)), src); err != nil {
					return err
				}
			} else {
				src = a.bootVfat()
			}
		default:
			src = filepath.Join(a.base, filepath.FromSlash(p.Source))
		}
		offset = alignUp(offset, a.layout.Align)
		if err := copyAt(out, src, offset, size); err != nil {
			return fmt.Errorf("partition %s: %w", p.Name, err)
		}
		entries = append(entries, mbrEntry{start: offset, size: size, kind: mbrKind(p.FS), active: p.Active})
		gptEntries = append(gptEntries, gptEntry{start: offset, size: size, kind: gptKind(p.FS), id: seededGUID(sum[:], "partition "+p.Name),
			name: p.Name, active: p.Active})
		fmt.Fprintf(a.Log, "Partition %d %s (%s): %d MiB at %d MiB\n", len(entries), p.Name, p.FS, size>>20, offset>>20)
		offset += size
	}
	if gpt {
		// Room for the backup gpt table.
		size := alignUp(offset+gptSectors*sector, a.layout.Align)
		if err := out.Truncate(size); err != nil {
			return err
		}
		if err := writeGPT(out, gptEntries, size, seededGUID(sum[:], "disk")); err != nil {
			return err
		}
	} else {
		if err := out.Truncate(alignUp(offset, a.layout.Align)); err != nil {
			return err
		}
		if err := writeMBR(out, entries, binary.LittleEndian.Uint32(sum[:4])); err != nil {
			return err
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	if img != a.Output {
		xz, err := os.Create(a.Output)
		if err != nil {
			return err
		}
		defer xz.Close()
		cmd := exec.Command("xz", "-T0", "-6", "-c", img)
		cmd.Stdout = xz
		cmd.Stderr = a.Log
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("xz: %w", err)
		}
		if err := xz.Close(); err != nil {
			return err
		}
	}
	digest, err := fileSHA256(a.Output)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Log, "Image %s sha256 %s\n", a.Output, digest)
	return nil
}

func copyAt(out *os.File, src string, offset, size int64) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if info.Size() > size {
		return fmt.Errorf("%d bytes do not fit its %d", info.Size(), size)
	}
	_, err = io.Copy(io.NewOffsetWriter(out, offset), in)
	return err
}

func fileSHA256(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// checkoutTrail checks the revision of state out in trail: its inline JSON
// documents canonical as in the state, so Pantavisor finds each one matching,
// and its objects hard linked from objects.
func checkoutTrail(trail string, state []byte, objects string) error {
	var docs map[string]interface{}
	if err := pvjson.Unmarshal(state, &docs); err != nil {
		return err
	}
	for name, v := range docs {
		s, isString := v.(string)
		object := isString && objectName.MatchString(s)
		if !object && !strings.HasSuffix(name, ".json") {
			continue
		}
		if !baseFile(name) || strings.HasPrefix(name, ".pvr/") {
			return fmt.Errorf("state entry %q is not a path in the trail", name)
		}
		file := filepath.Join(trail, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return err
		}
		if object {
			if err := os.Link(filepath.Join(objects, s), file); err != nil {
				return err
			}
			continue
		}
		data, err := pvjson.Marshal(v, pvjson.MarshalOptions{Canonical: true})
		if err != nil {
			return err
		}
		if err := os.WriteFile(file, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
