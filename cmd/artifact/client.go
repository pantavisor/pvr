// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty"
	"github.com/pantavisor/pvr/libpvr"
	"github.com/urfave/cli"
)

var errUnsupported = errors.New("this Hub does not support published artifacts yet")

type client struct {
	session *libpvr.Session
	base    string
}

func newClient(c *cli.Context) (*client, error) {
	base, _ := c.App.Metadata["PVR_BASEURL"].(string)
	if base == "" {
		return nil, errors.New("no Hub URL: set --baseurl")
	}
	session, err := libpvr.NewSession(c.App)
	if err != nil {
		return nil, err
	}
	return &client{session: session, base: strings.TrimRight(base, "/")}, nil
}

// call sends one JSON request and answers the body of a 2xx reply.
func (h *client) call(method, path string, body interface{}) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := h.session.DoAuthCall(false, libpvr.NoneCallMethod, func(req *resty.Request) error {
		req.Method, req.URL = method, h.base+path
		req.SetContext(ctx)
		if body != nil {
			req.SetHeader("Content-Type", "application/json")
			req.SetBody(body)
		}
		return nil
	})
	if err != nil || resp == nil {
		return nil, 0, errors.New("request to the Hub failed; check connectivity and `pvr login`")
	}
	status := resp.StatusCode()
	if status < 200 || status >= 300 {
		return resp.Body(), status, apiError(status, resp.Body())
	}
	return resp.Body(), status, nil
}

func apiError(status int, body []byte) error {
	var explained struct {
		Message string `json:"message"`
		Error   string `json:"Error"`
	}
	_ = json.Unmarshal(body, &explained)
	reason := explained.Message
	if reason == "" {
		reason = explained.Error
	}
	if len(reason) > 300 || strings.ContainsAny(reason, "\r\n") {
		reason = ""
	}
	switch {
	case status == http.StatusUnauthorized:
		return errors.New("not logged in to this Hub; run `pvr login`")
	case reason != "":
		return fmt.Errorf("Hub: HTTP %d (%s): %s", status, http.StatusText(status), reason)
	}
	return fmt.Errorf("Hub: HTTP %d (%s)", status, http.StatusText(status))
}

// Entry is one published artifact as the Hub stores it.
type Entry struct {
	ID          string                 `json:"id,omitempty"`
	Kind        string                 `json:"kind"`
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	SHA256      string                 `json:"sha256"`
	Size        int64                  `json:"size"`
	Description string                 `json:"description,omitempty"`
	Meta        map[string]interface{} `json:"meta,omitempty"`
}

// list answers the account's artifacts, filtered by kind and name when given.
func (h *client) list(kind, name string) ([]Entry, error) {
	path := "/artifacts"
	sep := "?"
	for _, q := range [][2]string{{"kind", kind}, {"name", name}} {
		if q[1] != "" {
			path += sep + q[0] + "=" + queryEscape(q[1])
			sep = "&"
		}
	}
	body, status, err := h.call(http.MethodGet, path, nil)
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented {
		return nil, errUnsupported
	}
	if err != nil {
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, errors.New("the Hub answered unexpected JSON")
	}
	return entries, nil
}

func queryEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// hashFile answers the sha256 and size of the file.
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

type objectRequest struct {
	ObjectName string `json:"objectname"`
	Sha        string `json:"sha256sum"`
	Size       string `json:"size"`
	MimeType   string `json:"mime-type"`
}

type objectReply struct {
	SignedPutUrl string `json:"signed-puturl"`
}

// upload puts the file into the account's object store, unless the account
// already has it. It answers whether bytes were sent.
func (h *client) upload(path, name, sha string, size int64, progress io.Writer) (bool, error) {
	body, status, err := h.call(http.MethodPost, "/objects/", objectRequest{
		ObjectName: name, Sha: sha, Size: strconv.FormatInt(size, 10), MimeType: "application/octet-stream"})
	if status == http.StatusConflict {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var reply objectReply
	if json.Unmarshal(body, &reply) != nil || reply.SignedPutUrl == "" {
		return false, errors.New("the Hub gave no upload URL for the object")
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	req, err := http.NewRequest(http.MethodPut, reply.SignedPutUrl, &progressReader{r: f, total: size, out: progress})
	if err != nil {
		return false, err
	}
	req.ContentLength = size
	if size == 0 {
		req.Body = http.NoBody
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, errors.New("uploading the file failed; check connectivity")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("uploading the file failed: HTTP %d", resp.StatusCode)
	}
	if progress != nil {
		fmt.Fprintln(progress)
	}
	return true, nil
}

// progressReader prints the percentage sent, at most twice a second.
type progressReader struct {
	r     io.Reader
	total int64
	sent  int64
	last  time.Time
	out   io.Writer
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.sent += int64(n)
	if p.out != nil && p.total > 0 && (time.Since(p.last) > 500*time.Millisecond || p.sent == p.total) {
		p.last = time.Now()
		fmt.Fprintf(p.out, "\ruploading %d/%d MiB (%d%%)", p.sent>>20, p.total>>20, p.sent*100/p.total)
	}
	return n, err
}
