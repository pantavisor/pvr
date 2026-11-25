//
// Copyright 2026  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package devmode

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DefaultDir is the local tree: ./devmode/<container>/<path>.
const DefaultDir = "devmode"

// CleanRemotePath checks a container-relative path and returns it cleaned.
func CleanRemotePath(p string) (string, error) {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return "", fmt.Errorf("bad path %q", p)
	}
	if strings.ContainsAny(p, "\n\r\x00") {
		return "", fmt.Errorf("bad path %q", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", fmt.Errorf("bad path %q", p)
		}
	}
	c := path.Clean(p)
	if c == "." {
		return "", fmt.Errorf("bad path %q", p)
	}
	return c, nil
}

// LocalToRemote maps <root>/<container>/<rel> to (container, container path).
func LocalToRemote(root, file string) (container, rel string, err error) {
	r, err := filepath.Rel(root, file)
	if err != nil {
		return "", "", err
	}
	r = filepath.ToSlash(r)
	parts := strings.SplitN(r, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[0] == ".." {
		return "", "", fmt.Errorf("%s is not inside a container directory of %s", file, root)
	}
	rel, err = CleanRemotePath(parts[1])
	return parts[0], rel, err
}

// RemoteToLocal maps a container path to a local file below
// <root>/<container> and refuses anything that escapes it.
func RemoteToLocal(root, container, rel string) (string, error) {
	c, err := CleanRemotePath(rel)
	if err != nil {
		return "", err
	}
	if container == "" || strings.ContainsAny(container, "/\\") || container == ".." || container == "." {
		return "", fmt.Errorf("bad container name %q", container)
	}
	return filepath.Join(root, container, filepath.FromSlash(c)), nil
}

// Ignored tells if a local file is editor or VCS noise that is never pushed.
func Ignored(name string) bool {
	switch {
	case name == ".git", name == ".DS_Store", name == "4913", name == DeletedListName:
		return true
	case strings.HasSuffix(name, "~"), strings.HasSuffix(name, ".swp"),
		strings.HasSuffix(name, ".swx"), strings.HasSuffix(name, ".tmp"),
		strings.HasPrefix(name, ".#"):
		return true
	}
	return false
}

func fileSHA(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// ScanLocal lists the files below <root>/<container>.
func ScanLocal(root, container string) ([]LocalFile, error) {
	base := filepath.Join(root, container)
	var out []LocalFile
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == base {
			return nil
		}
		if Ignored(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		lf := LocalFile{Path: filepath.ToSlash(rel)}
		info, err := d.Info()
		if err != nil {
			return err
		}
		lf.Mode = uint32(info.Mode().Perm())
		switch {
		case d.IsDir():
			// only empty directories are pushed; others come with their files
			ents, rerr := os.ReadDir(p)
			if rerr != nil {
				return rerr
			}
			for _, x := range ents {
				if !Ignored(x.Name()) {
					return nil
				}
			}
			lf.Type = "dir"
		case info.Mode()&fs.ModeSymlink != 0:
			lf.Type = "symlink"
			lf.Target, err = os.Readlink(p)
		case info.Mode().IsRegular():
			lf.Type = "file"
			lf.SHA256, lf.Size, err = fileSHA(p)
		default:
			return nil
		}
		if err != nil {
			return err
		}
		out = append(out, lf)
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return out, err
}

// LocalContainers lists the container directories below root.
func LocalContainers(root string) ([]string, error) {
	ents, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && !Ignored(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out, err
}
