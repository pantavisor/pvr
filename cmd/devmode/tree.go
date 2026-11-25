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
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"strings"
)

// codeNoTar marks a device that cannot answer a tree request as a tar.
const codeNoTar = "NO_TAR"

func errNoTar(why string) *Error {
	e := errf(KindGeneric, "this device cannot send a folder as a tar ("+why+")")
	e.Code = codeNoTar
	return e
}

// isNoTar tells if a Tree failure only means the device is too old for tar
// files; the caller then falls back to the file-by-file pull.
func isNoTar(err error) bool {
	e, ok := err.(*Error)
	if !ok {
		return false
	}
	return e.Code == codeNoTar || e.Code == "PANTAVISOR_TOO_OLD" ||
		strings.Contains(e.Msg, "PANTAVISOR_TOO_OLD") || strings.Contains(e.Msg, "unknown tool")
}

// treePath is the path part of a tree request; "" and "." mean the whole container.
func treePath(p string) (string, error) {
	if p == "" || p == "." {
		return "", nil
	}
	return CleanRemotePath(p)
}

type tarBody struct {
	io.Reader
	c io.Closer
}

func (t tarBody) Close() error { return t.c.Close() }

// checkTar peeks at the first block of a stream: it must be a tar header or
// the zero blocks of an empty tar, else the device did not send a tar.
func checkTar(rc io.ReadCloser) (io.ReadCloser, error) {
	br := bufio.NewReaderSize(rc, 1<<16)
	b, err := br.Peek(512)
	if len(b) < 512 {
		rc.Close()
		if e, ok := err.(*Error); ok {
			return nil, e
		}
		return nil, errNoTar("the answer is not a tar file")
	}
	zero := true
	for _, c := range b {
		if c != 0 {
			zero = false
			break
		}
	}
	if !zero && string(b[257:262]) != "ustar" {
		rc.Close()
		return nil, errNoTar("the answer is not a tar file")
	}
	return tarBody{br, rc}, nil
}

// sumBody checks the sha256 and size a device or the Hub announced, once the
// stream ends.
type sumBody struct {
	r    io.ReadCloser
	h    hash.Hash
	want string
	n    int64
	size int64
}

func newSumBody(r io.ReadCloser, sum string, size int64) io.ReadCloser {
	if sum == "" {
		return r
	}
	h := sha256.New()
	return &sumBody{r: r, h: h, want: sum, size: size}
}

func (s *sumBody) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	s.h.Write(p[:n])
	s.n += int64(n)
	if err == io.EOF {
		if got := hex.EncodeToString(s.h.Sum(nil)); got != s.want || (s.size > 0 && s.n != s.size) {
			return n, errf(KindGeneric, "the tar did not arrive intact (checksum differs); try again")
		}
	}
	return n, err
}

func (s *sumBody) Close() error { return s.r.Close() }

// Tree for the pvcontrol backend: `pvcontrol devmode files get <c> [path] --tar`.
func (c *Client) Tree(ctx context.Context, container, p string) (io.ReadCloser, error) {
	p, err := treePath(p)
	if err != nil {
		return nil, errf(KindUsage, err.Error())
	}
	args := []string{"devmode", "files", "get", container}
	if p != "" {
		args = append(args, p)
	}
	args = append(args, "--tar")
	pr, pw := io.Pipe()
	go func() {
		_, err := c.pvcontrol(ctx, nil, pw, args...)
		pw.CloseWithError(err)
	}()
	rc, err := checkTar(pr)
	if err != nil {
		pr.CloseWithError(err)
		// an old pvcontrol does not know --tar
		if e, ok := err.(*Error); ok && e.Code == "" && e.Kind == KindGeneric {
			l := strings.ToLower(e.Msg)
			if strings.Contains(l, "tar") || strings.Contains(l, "usage") || strings.Contains(l, "unknown") || strings.Contains(l, "unrecognized") {
				return nil, errNoTar(e.Msg)
			}
		}
		return nil, err
	}
	return rc, nil
}
