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
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DeletedListName is the reserved file in ./devmode/<container>/ that lists
// image paths to delete in the container. It is never uploaded.
const DeletedListName = ".devmode-deleted"

// ParseDeletedList reads one path per line; blank lines and # comments are
// allowed. Every path is checked.
func ParseDeletedList(text string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(text))
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, err := CleanRemotePath(line)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %v", DeletedListName, n, err)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, sc.Err()
}

func deletedListPath(root, container string) string {
	return filepath.Join(root, container, DeletedListName)
}

// ReadDeletedList returns the deleted list of a container, empty if there is none.
func ReadDeletedList(root, container string) ([]string, error) {
	b, err := os.ReadFile(deletedListPath(root, container))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseDeletedList(string(b))
}

// WriteDeletedList replaces the list; an empty list removes the file.
func WriteDeletedList(root, container string, paths []string) error {
	p := deletedListPath(root, container)
	if len(paths) == 0 {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	sort.Strings(paths)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.Join(paths, "\n")+"\n"), 0o644)
}

// AddDeleted adds paths to the list, creating the container directory.
func AddDeleted(root, container string, paths ...string) error {
	cur, err := ReadDeletedList(root, container)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, c := range cur {
		have[c] = true
	}
	for _, p := range paths {
		if !have[p] {
			cur = append(cur, p)
			have[p] = true
		}
	}
	return WriteDeletedList(root, container, cur)
}

// ValidateTree checks the whole local tree of one container and returns the
// first problem; it is what sync runs before touching the device.
func ValidateTree(local []LocalFile, deleted []string) error {
	for _, l := range local {
		if _, err := CleanRemotePath(l.Path); err != nil {
			return err
		}
		if l.Type == "symlink" && l.Target == "" {
			return fmt.Errorf("%s: symlink without a target", l.Path)
		}
		for _, d := range deleted {
			if l.Path == d || strings.HasPrefix(l.Path, d+"/") {
				return fmt.Errorf("%s is in ./%s and also in the deleted list (%s)", l.Path, DefaultDir, d)
			}
		}
	}
	return nil
}
