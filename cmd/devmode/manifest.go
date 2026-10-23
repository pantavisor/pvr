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
	"sort"
	"strconv"
)

// Entry is one line of the device manifest (GET /devmode/files/{container}).
type Entry struct {
	Path   string `json:"path"`
	Type   string `json:"type"` // file, dir, symlink, deleted
	Size   int64  `json:"size,omitempty"`
	Mode   string `json:"mode,omitempty"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
}

type Manifest struct {
	Container string  `json:"container"`
	Files     []Entry `json:"files"`
}

// LocalFile is one file of the local tree.
type LocalFile struct {
	Path   string // container path, slash separated, no leading slash
	Type   string // file or symlink
	Mode   uint32 // permission bits
	Size   int64
	SHA256 string
	Target string
}

// Change says what differs for one path.
type Change struct {
	Path   string `json:"path"`
	Reason string `json:"reason,omitempty"` // content, mode, type
}

// Diff is what a push would do for one container.
type Diff struct {
	Added   []Change `json:"added"`
	Changed []Change `json:"changed"`
	// Deleted lists files on the device that are missing locally; only
	// push --delete removes them.
	Deleted   []Change `json:"deleted"`
	Unchanged int      `json:"unchanged"`
}

func (d Diff) Empty() bool {
	return len(d.Added)+len(d.Changed)+len(d.Deleted) == 0
}

func parseMode(s string) (uint32, bool) {
	v, err := strconv.ParseUint(s, 8, 32)
	return uint32(v), err == nil
}

// ComputeDiff compares the local tree with the device manifest.
func ComputeDiff(local []LocalFile, m Manifest) Diff {
	dev := map[string]Entry{}
	for _, e := range m.Files {
		dev[e.Path] = e
	}
	d := Diff{Added: []Change{}, Changed: []Change{}, Deleted: []Change{}}
	seen := map[string]bool{}
	for _, l := range local {
		seen[l.Path] = true
		e, ok := dev[l.Path]
		switch {
		case !ok || e.Type == "deleted" || e.Type == "dir":
			d.Added = append(d.Added, Change{Path: l.Path})
		case e.Type != l.Type:
			d.Changed = append(d.Changed, Change{l.Path, "type"})
		case l.Type == "symlink":
			if e.Target != l.Target {
				d.Changed = append(d.Changed, Change{l.Path, "content"})
			} else {
				d.Unchanged++
			}
		case e.SHA256 != l.SHA256:
			d.Changed = append(d.Changed, Change{l.Path, "content"})
		default:
			if mode, ok := parseMode(e.Mode); ok && mode&0o777 != l.Mode&0o777 {
				d.Changed = append(d.Changed, Change{l.Path, "mode"})
			} else {
				d.Unchanged++
			}
		}
	}
	for _, e := range m.Files {
		if (e.Type == "file" || e.Type == "symlink") && !seen[e.Path] {
			d.Deleted = append(d.Deleted, Change{Path: e.Path})
		}
	}
	for _, s := range [][]Change{d.Added, d.Changed, d.Deleted} {
		sort.Slice(s, func(i, j int) bool { return s[i].Path < s[j].Path })
	}
	return d
}
