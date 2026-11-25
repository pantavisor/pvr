// Copyright 2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package devmode

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	hubLeaseSeconds = 600
	// bodies up to this size travel inside the tool call
	hubInlineMax = 256 << 10
	hubHashBatch = 2000
	hubMaxBytes  = 512 << 20
	hubMaxPages  = 1000
)

// HubBackend runs the devmode tools of the Hub on a device. Everything the
// tools do ends at the same pv-ctrl calls as the other transports.
type HubBackend struct {
	Device string // id or nick
	mcp    *mcpClient
	http   *http.Client

	leased     bool
	leaseUntil time.Time
}

var _ Backend = (*HubBackend)(nil)

func NewHubBackend(device, base string, store *hubTokenStore, login LoginToken) *HubBackend {
	auth := newHubAuth(base, store, login)
	return &HubBackend{Device: device, mcp: newMCPClient(base, auth), http: &http.Client{}}
}

// ParseHubTarget gets the device out of hub:<device id or nick>.
func ParseHubTarget(s string) (string, error) {
	d, ok := strings.CutPrefix(s, "hub:")
	d = strings.TrimSpace(d)
	if !ok || d == "" || strings.ContainsAny(d, " \t/") {
		return "", fmt.Errorf("bad device %q: want hub:<device id or nick>", s)
	}
	return d, nil
}

func (h *HubBackend) args(kv ...interface{}) map[string]interface{} {
	m := map[string]interface{}{"device": h.Device}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func (h *HubBackend) call(ctx context.Context, tool string, kv ...interface{}) (map[string]interface{}, error) {
	return h.mcp.Call(ctx, tool, h.args(kv...))
}

// ensureLease takes the lease that the tools which change a device need, and
// takes it again when it is close to running out.
func (h *HubBackend) ensureLease(ctx context.Context) error {
	if h.leased && time.Until(h.leaseUntil) > 90*time.Second {
		return nil
	}
	r, err := h.call(ctx, "lease", "ttl_s", hubLeaseSeconds)
	if err != nil {
		return err
	}
	h.leased = true
	h.leaseUntil = time.Now().Add(hubLeaseSeconds * time.Second)
	if t, err := time.Parse(time.RFC3339, str(r, "expires_at")); err == nil {
		h.leaseUntil = t
	}
	return nil
}

// mutate runs a tool that needs the lease.
func (h *HubBackend) mutate(ctx context.Context, tool string, kv ...interface{}) (map[string]interface{}, error) {
	for attempt := 0; ; attempt++ {
		if err := h.ensureLease(ctx); err != nil {
			return nil, err
		}
		r, err := h.call(ctx, tool, kv...)
		if e, ok := err.(*Error); ok && e.Code == "LEASE_REQUIRED" && attempt == 0 {
			h.leased = false
			continue
		}
		return r, err
	}
}

// Close gives the lease back.
func (h *HubBackend) Close() error {
	if !h.leased {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.leased = false
	_, err := h.call(ctx, "release")
	return err
}

func (h *HubBackend) Status(ctx context.Context) (Status, error) {
	var s Status
	r, err := h.call(ctx, "get_devmode_context")
	if err != nil {
		return s, err
	}
	if !boolean(r, "connected") {
		return s, errf(KindUnreachable, "the device is not connected to the Hub now")
	}
	// an older Hub does not say whether PV_DEVMODE_ALLOWED is set (null or missing); dev_begin refuses then
	s.Enabled = true
	if v, ok := r["devmode_allowed"].(bool); ok {
		s.Enabled = v
	}
	s.Active = boolean(r, "devmode")
	s.Ready = s.Active && !boolean(r, "devmode_starting")
	s.Revision = str(r, "devmode_on_device")
	s.Base = str(r, "devmode_base")
	if s.Active {
		cs, err := h.Containers(ctx)
		if err != nil {
			return s, err
		}
		for n := range cs {
			s.Containers = append(s.Containers, ContainerInfo{Name: n})
		}
		sortContainers(s.Containers)
	}
	return s, nil
}

func (h *HubBackend) Enter(ctx context.Context, base string, resume bool) (EnterResult, error) {
	var res EnterResult
	kv := []interface{}{"resume", resume}
	if base != "" {
		kv = append(kv, "base", base)
	}
	r, err := h.mutate(ctx, "dev_begin", kv...)
	if err != nil {
		return res, err
	}
	rebooted := boolean(r, "rebooted")
	res.Revision = str(r, "on_device")
	res.Base = str(r, "base")
	res.Running = !rebooted
	res.Resumed = resume || !rebooted
	// dev_begin waits for the device; it is only still rebooting when pending
	res.Reboot = rebooted && boolean(r, "pending")
	return res, nil
}

// Leave ends devmode. The Hub can only return to the committed revision.
func (h *HubBackend) Leave(ctx context.Context, rev string) error {
	r, err := h.call(ctx, "get_devmode_context")
	if err != nil {
		return err
	}
	if rev != "" && rev != str(r, "committed_revision") && rev != str(r, "committed_on_device") {
		return errf(KindUsage, fmt.Sprintf("over the Hub the device can only return to its committed revision (%s), not %s", str(r, "committed_revision"), rev))
	}
	_, err = h.mutate(ctx, "dev_end", "mode", "discard")
	h.leased = false // dev_end gives the lease back
	return err
}

func (h *HubBackend) Containers(ctx context.Context) (map[string]ContainerState, error) {
	r, err := h.call(ctx, "container_status")
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(r)
	return ParseContainers(b)
}

func (h *HubBackend) RestartPolicies(ctx context.Context) (map[string]string, error) {
	return policiesOf(h.Containers(ctx))
}

func (h *HubBackend) Reset(ctx context.Context, container string) error {
	_, err := h.mutate(ctx, "reset_changes", "container", container)
	return err
}

func (h *HubBackend) Apply(ctx context.Context, container string, reboot bool) error {
	_, err := h.mutate(ctx, "container_ctl", "container", container, "action", "restart", "allow_reboot", reboot)
	if e, ok := err.(*Error); ok && e.Kind == KindGeneric {
		e.Kind = KindApplyFailed
	}
	return err
}

func (h *HubBackend) Remove(ctx context.Context, container, p string, recursive bool) error {
	return h.apply(ctx, container, []map[string]interface{}{{"op": "rm", "path": p, "recursive": recursive}})
}

// apply sends changes and waits for the answer while the device is busy.
func (h *HubBackend) apply(ctx context.Context, container string, ops []map[string]interface{}) error {
	id, err := randomText(9)
	if err != nil {
		return err
	}
	id = strings.NewReplacer("-", "a", "_", "b").Replace(id)
	r, err := h.mutate(ctx, "apply_changes", "container", container, "ops", ops, "apply_id", id)
	for i := 0; err == nil && boolean(r, "pending") && i < 20; i++ {
		r, err = h.mutate(ctx, "apply_changes", "container", container, "apply_id", id)
	}
	if err == nil && boolean(r, "pending") {
		return errf(KindWaitTimeout, "the device is still applying the changes")
	}
	return err
}

func parentOps(p string) []map[string]interface{} {
	if d := path.Dir(p); d != "." && d != "/" {
		return []map[string]interface{}{{"op": "mkdir", "path": d, "parents": true}}
	}
	return nil
}

// Put writes a file, directory or symlink. The ops carry uid and gid; a Hub
// that does not know them yet refuses the call, and then root-owned files are
// sent without them.
func (h *HubBackend) Put(ctx context.Context, container, p string, body io.Reader, o PutOpts) error {
	if body != nil {
		// the body may be read twice
		if _, ok := body.(io.ReadSeeker); !ok {
			all, err := io.ReadAll(body)
			if err != nil {
				return errf(KindGeneric, "cannot read the file: "+err.Error())
			}
			body = bytes.NewReader(all)
		}
	}
	ops, err := h.putOps(ctx, p, body, o, true)
	if err != nil {
		return err
	}
	err = h.apply(ctx, container, ops)
	if err == nil || !refusesArgument(err, "uid", "gid") {
		return err
	}
	if o.UID != 0 || o.GID != 0 {
		return errf(KindUsage, "this Hub cannot give files an owner other than root; leave out --keep-owner")
	}
	ops, err = h.putOps(ctx, p, body, o, false)
	if err != nil {
		return err
	}
	return h.apply(ctx, container, ops)
}

// refusesArgument tells if a Hub error is about one of the named arguments.
func refusesArgument(err error, names ...string) bool {
	e, ok := err.(*Error)
	if !ok || e.Kind != KindGeneric {
		return false
	}
	for _, n := range names {
		if strings.Contains(e.Msg, n) {
			return true
		}
	}
	return false
}

func (h *HubBackend) putOps(ctx context.Context, p string, body io.Reader, o PutOpts, owner bool) ([]map[string]interface{}, error) {
	setOwner := func(op map[string]interface{}) map[string]interface{} {
		if owner {
			op["uid"], op["gid"] = o.UID, o.GID
		}
		return op
	}
	ops := parentOps(p)
	switch o.Type {
	case "dir":
		op := map[string]interface{}{"op": "mkdir", "path": p, "parents": true}
		if m := o.Mode & 0o7777; m != 0 {
			op["mode"] = fmt.Sprintf("%04o", m)
		}
		return []map[string]interface{}{setOwner(op)}, nil
	case "symlink":
		return append(ops, setOwner(map[string]interface{}{"op": "symlink", "path": p, "target": o.Target})), nil
	}
	if body == nil {
		body = bytes.NewReader(nil)
	}
	put := setOwner(map[string]interface{}{"op": "put", "path": p, "mode": fmt.Sprintf("%04o", o.Mode&0o7777)})
	data, sum, size, err := spool(body)
	if err != nil {
		return nil, errf(KindGeneric, "cannot read the file: "+err.Error())
	}
	if size <= hubInlineMax {
		b, err := io.ReadAll(data)
		if err != nil {
			return nil, errf(KindGeneric, err.Error())
		}
		put["base64"] = base64.StdEncoding.EncodeToString(b)
		if size == 0 {
			delete(put, "base64")
			put["text"] = ""
		}
	} else {
		if err := h.upload(ctx, sum, size, data); err != nil {
			return nil, err
		}
		put["source"] = map[string]interface{}{"sha256": sum, "size": size}
	}
	return append(ops, put), nil
}

// spool gives back a body that can be read again, with its sha256 and size.
func spool(body io.Reader) (io.ReadSeeker, string, int64, error) {
	rs, ok := body.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(body)
		if err != nil {
			return nil, "", 0, err
		}
		rs = bytes.NewReader(b)
	}
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return nil, "", 0, err
	}
	hash := sha256.New()
	n, err := io.Copy(hash, rs)
	if err != nil {
		return nil, "", 0, err
	}
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return nil, "", 0, err
	}
	return rs, hex.EncodeToString(hash.Sum(nil)), n, nil
}

// upload puts a large file into the account's object store, unless the
// account has it already.
func (h *HubBackend) upload(ctx context.Context, sum string, size int64, data io.ReadSeeker) error {
	// objects belong to the account, so this tool takes no device
	r, err := h.mcp.Call(ctx, "get_object_upload_link", map[string]interface{}{"sha256": sum, "size": size})
	if err != nil {
		return err
	}
	if boolean(r, "already_present") {
		return nil
	}
	link, err := h.resolveLink(str(r, "put_url"))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PUT", link, data)
	if err != nil {
		return errf(KindGeneric, "bad upload link from the Hub")
	}
	req.ContentLength = size
	if size == 0 {
		req.Body = http.NoBody
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return errf(KindUnreachable, "cannot upload the file: "+urlErrorText(err))
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return errf(KindGeneric, fmt.Sprintf("the object store refused the upload (HTTP %d)", resp.StatusCode))
	}
	return nil
}

// resolveLink makes a link the Hub gave without a host absolute.
func (h *HubBackend) resolveLink(l string) (string, error) {
	u, err := url.Parse(l)
	if err != nil || l == "" {
		return "", errf(KindGeneric, "the Hub gave no usable link")
	}
	if u.IsAbs() {
		return l, nil
	}
	base, err := url.Parse(h.mcp.url)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(u).String(), nil
}

func (h *HubBackend) Get(ctx context.Context, container, p string, w io.Writer) error {
	hash := sha256.New()
	out := io.MultiWriter(w, hash)
	var offset int64
	transfer := ""
	for i := 0; i < 1<<20; i++ {
		kv := []interface{}{"container", container, "path", p, "max_bytes", 65536, "offset", offset}
		if transfer != "" {
			kv = append(kv, "as_link", true, "transfer_id", transfer)
		}
		r, err := h.call(ctx, "get_file", kv...)
		if err != nil {
			return err
		}
		if link := str(r, "get_url"); link != "" {
			return h.download(ctx, link, w)
		}
		if boolean(r, "pending") {
			transfer = str(r, "transfer_id")
			continue
		}
		var chunk []byte
		if t, ok := r["text"].(string); ok {
			chunk = []byte(t)
		} else if b64 := str(r, "base64"); b64 != "" {
			if chunk, err = base64.StdEncoding.DecodeString(b64); err != nil {
				return errf(KindGeneric, "the Hub sent file content that is not base64")
			}
		}
		if _, err := out.Write(chunk); err != nil {
			return err
		}
		offset += int64(len(chunk))
		if boolean(r, "eof") {
			if want := str(r, "sha256"); want != "" && want != hex.EncodeToString(hash.Sum(nil)) {
				return errf(KindGeneric, p+": the file changed while it was read; try again")
			}
			return nil
		}
		if len(chunk) == 0 {
			return errf(KindGeneric, p+": the Hub sent no content and no end of file")
		}
	}
	return errf(KindGeneric, p+": the file is too large to read in parts")
}

// Tree asks the Hub for the folder as a tar and downloads the link it gives.
func (h *HubBackend) Tree(ctx context.Context, container, p string) (io.ReadCloser, error) {
	p, err := treePath(p)
	if err != nil {
		return nil, errf(KindUsage, err.Error())
	}
	transfer := ""
	for i := 0; i < hubMaxPages; i++ {
		kv := []interface{}{"container", container}
		if p != "" {
			kv = append(kv, "path", p)
		}
		if transfer != "" {
			kv = append(kv, "transfer_id", transfer)
		}
		r, err := h.call(ctx, "get_tree", kv...)
		if err != nil {
			if isNoTar(err) {
				e := err.(*Error)
				return nil, &Error{Kind: e.Kind, Msg: e.Msg, Code: codeNoTar}
			}
			return nil, err
		}
		if boolean(r, "pending") {
			transfer = str(r, "transfer_id")
			continue
		}
		link := str(r, "get_url")
		if link == "" {
			return nil, errf(KindGeneric, "the Hub gave no download link for the tar")
		}
		l, err := h.resolveLink(link)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", l, nil)
		if err != nil {
			return nil, errf(KindGeneric, "bad download link from the Hub")
		}
		resp, err := h.http.Do(req)
		if err != nil {
			return nil, errf(KindUnreachable, "cannot download the tar: "+urlErrorText(err))
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, errf(KindGeneric, fmt.Sprintf("the object store refused the download (HTTP %d)", resp.StatusCode))
		}
		size, _ := r["size"].(float64)
		return newSumBody(resp.Body, str(r, "sha256"), int64(size)), nil
	}
	return nil, errf(KindWaitTimeout, "the Hub is still preparing the tar")
}

func (h *HubBackend) download(ctx context.Context, link string, w io.Writer) error {
	l, err := h.resolveLink(link)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", l, nil)
	if err != nil {
		return errf(KindGeneric, "bad download link from the Hub")
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return errf(KindUnreachable, "cannot download the file: "+urlErrorText(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errf(KindGeneric, fmt.Sprintf("the object store refused the download (HTTP %d)", resp.StatusCode))
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// modeText writes a file mode as octal text.
func modeText(v interface{}) string {
	switch m := v.(type) {
	case string:
		return m
	case float64:
		return fmt.Sprintf("%04o", int(m)&0o7777)
	}
	return ""
}

func cleanPath(p string) string {
	return strings.TrimPrefix(strings.TrimPrefix(p, "./"), "/")
}

// Manifest lists what devmode changed in a container: ls for the entries and
// hash for the content of the files.
func (h *HubBackend) Manifest(ctx context.Context, container string) (Manifest, error) {
	m := Manifest{Container: container, Files: []Entry{}}
	page := ""
	noDeleted := false
	for i := 0; i < hubMaxPages; i++ {
		kv := []interface{}{"container", container, "path", ".", "recursive", true, "max_entries", 2000}
		if page != "" {
			kv = append(kv, "page", page)
		}
		if !noDeleted {
			kv = append(kv, "include_deleted", true)
		}
		r, err := h.call(ctx, "ls", kv...)
		if err != nil && !noDeleted && refusesArgument(err, "include_deleted") {
			// a Hub from before include_deleted: the deleted image paths stay unknown
			noDeleted = true
			i--
			continue
		}
		if err != nil {
			return m, err
		}
		for _, e := range list(r, "entries") {
			p := cleanPath(str(e, "path"))
			if p == "" || p == "." {
				continue
			}
			if boolean(e, "deleted") {
				m.Files = append(m.Files, Entry{Path: p, Type: "deleted"})
				continue
			}
			if str(e, "origin") != "inlay" {
				continue
			}
			ent := Entry{Path: p, Type: str(e, "type"), Mode: modeText(e["mode"]), Target: str(e, "target")}
			if sz, ok := e["size"].(float64); ok {
				ent.Size = int64(sz)
			}
			if ent.Type == "other" {
				ent.Type = "special"
			}
			m.Files = append(m.Files, ent)
		}
		if page = str(r, "next_page"); page == "" {
			break
		}
	}
	var files []string
	for _, e := range m.Files {
		if e.Type == "file" {
			files = append(files, e.Path)
		}
	}
	sums, err := h.hashFiles(ctx, container, files)
	if err != nil {
		return m, err
	}
	for i := range m.Files {
		m.Files[i].SHA256 = sums[m.Files[i].Path]
	}
	return m, nil
}

func (h *HubBackend) hashFiles(ctx context.Context, container string, files []string) (map[string]string, error) {
	sums := map[string]string{}
	for len(files) > 0 {
		n := min(len(files), hubHashBatch)
		batch := files[:n]
		files = files[n:]
		page, id := "", ""
		for i := 0; i < hubMaxPages; i++ {
			kv := []interface{}{"container", container, "max_bytes", hubMaxBytes}
			if id != "" {
				kv = append(kv, "hash_id", id)
			} else {
				kv = append(kv, "paths", batch)
				if page != "" {
					kv = append(kv, "page", page)
				}
			}
			r, err := h.call(ctx, "hash", kv...)
			if err != nil {
				return nil, err
			}
			if boolean(r, "pending") {
				id = str(r, "hash_id")
				continue
			}
			id = ""
			for _, e := range list(r, "entries") {
				if s := str(e, "sha256"); s != "" {
					sums[cleanPath(str(e, "path"))] = s
				}
			}
			if page = str(r, "next_page"); page == "" {
				break
			}
		}
	}
	return sums, nil
}

func sortContainers(c []ContainerInfo) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j].Name < c[j-1].Name; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}

// Exec uses the exec tool.
func (h *HubBackend) Exec(ctx context.Context, container string, argv []string, o ExecOpts) (ExecResult, error) {
	var res ExecResult
	kv := []interface{}{"container", container, "argv", argv, "timeout_s", o.seconds()}
	if len(o.Stdin) > 0 {
		kv = append(kv, "stdin_base64", base64.StdEncoding.EncodeToString(o.Stdin))
	}
	if o.MaxOutput > 0 {
		kv = append(kv, "max_output", o.MaxOutput)
	}
	if o.Cwd != "" {
		kv = append(kv, "cwd", o.Cwd)
	}
	if len(o.Env) > 0 {
		kv = append(kv, "env", o.Env)
	}
	r, err := h.mutate(ctx, "exec", kv...)
	if err != nil {
		return res, err
	}
	res.TimedOut = boolean(r, "timed_out")
	if v, ok := r["exit_code"].(float64); ok {
		n := int(v)
		res.ExitCode = &n
	}
	if v, ok := r["signal"].(float64); ok {
		n := int(v)
		res.Signal = &n
	}
	if res.Stdout, err = streamText(r, "stdout"); err != nil {
		return res, err
	}
	if res.Stderr, err = streamText(r, "stderr"); err != nil {
		return res, err
	}
	if t, ok := r["truncated"].(map[string]interface{}); ok {
		res.TruncStdout, res.TruncStderr = boolean(t, "stdout"), boolean(t, "stderr")
	}
	if v, ok := r["duration_ms"].(float64); ok {
		res.DurationMS = int64(v)
	}
	return res, nil
}

// streamText reads stdout or stderr, which come as text or as base64.
func streamText(r map[string]interface{}, name string) (string, error) {
	s := str(r, name)
	if str(r, name+"_encoding") != "base64" {
		return s, nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", errf(KindGeneric, "the Hub sent command output that is not base64")
	}
	return string(b), nil
}

// Logs uses the logs tool, which reads the console log of the container.
func (h *HubBackend) Logs(ctx context.Context, container string, tail int) ([]string, error) {
	kv := []interface{}{"container", container}
	if tail > 0 {
		kv = append(kv, "tail", tail)
	}
	r, err := h.call(ctx, "logs", kv...)
	if err != nil {
		return nil, err
	}
	// the tool adds pantavisor.log lines naming the container; logs shows the console log only, as over ssh and http
	console := consoleLogSource(container)
	lines := []string{}
	for _, l := range list(r, "lines") {
		if src := str(l, "src"); src == "" || src == console {
			lines = append(lines, str(l, "text"))
		}
	}
	return lines, nil
}
