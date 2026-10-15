// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.

// Package image assembles a flashable device image from an image base (the
// generic parts meta-pantavisor's pantavisor-image-base packs), a signing
// identity's trust and the exports of a factory revision.
package image

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

// The initrd is a "newc" cpio archive. It is rewritten as a stream, never
// unpacked: device nodes and owners survive without root.

const (
	newcMagic   = "070701"
	newcHeader  = 110
	newcTrailer = "TRAILER!!!"
	modeFile    = 0o100000
	modeDir     = 0o040000
	modeType    = 0o170000
)

type newcEntry struct {
	// fields are the 13 header numbers after the magic, in order: ino,
	// mode, uid, gid, nlink, mtime, filesize, devmajor, devminor,
	// rdevmajor, rdevminor, namesize, check.
	fields [13]uint32
	name   string
	data   []byte
}

func (e *newcEntry) mode() uint32 { return e.fields[1] }

func pad4(n int) int { return (4 - n%4) % 4 }

func readNewc(r io.Reader) (*newcEntry, error) {
	var head [newcHeader]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, fmt.Errorf("initrd cpio: %w", err)
	}
	if string(head[:6]) != newcMagic {
		return nil, errors.New("initrd is not a newc cpio archive")
	}
	e := &newcEntry{}
	for i := range e.fields {
		v, err := strconv.ParseUint(string(head[6+8*i:14+8*i]), 16, 32)
		if err != nil {
			return nil, errors.New("initrd cpio has a bad header")
		}
		e.fields[i] = uint32(v)
	}
	nameSize := int(e.fields[11])
	if nameSize < 1 || nameSize > 4096 {
		return nil, errors.New("initrd cpio has a bad name")
	}
	name := make([]byte, nameSize+pad4(newcHeader+nameSize))
	if _, err := io.ReadFull(r, name); err != nil {
		return nil, fmt.Errorf("initrd cpio: %w", err)
	}
	e.name = string(name[:nameSize-1])
	size := int(e.fields[6])
	if size > 1<<30 {
		return nil, errors.New("initrd cpio entry is too big")
	}
	data := make([]byte, size+pad4(size))
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("initrd cpio: %w", err)
	}
	e.data = data[:size]
	return e, nil
}

func writeNewc(w io.Writer, e *newcEntry) error {
	e.fields[6] = uint32(len(e.data))
	e.fields[11] = uint32(len(e.name) + 1)
	var b bytes.Buffer
	b.WriteString(newcMagic)
	for _, v := range e.fields {
		fmt.Fprintf(&b, "%08X", v)
	}
	b.WriteString(e.name)
	b.WriteByte(0)
	b.Write(make([]byte, pad4(newcHeader+len(e.name)+1)))
	b.Write(e.data)
	b.Write(make([]byte, pad4(len(e.data))))
	_, err := w.Write(b.Bytes())
	return err
}

// cleanName is how an archive names a path: no leading "/" or "./".
func cleanName(name string) string {
	name = path.Clean("/" + name)
	return strings.TrimPrefix(name, "/")
}

// Edit changes a regular file of the archive: it gets the old content (nil
// when the file is missing) and answers the new one.
type Edit func(old []byte) ([]byte, error)

// RewriteNewc copies a newc archive from r to w, applying edits by path.
// A regular file an edit names gets the content the edit answers; a missing
// one is added with mode 0644, root-owned, as are its missing parent
// directories (0755). Everything else is copied unchanged.
func RewriteNewc(r io.Reader, w io.Writer, edits map[string]Edit) error {
	pending := map[string]Edit{}
	for name, edit := range edits {
		pending[cleanName(name)] = edit
	}
	dirs := map[string]bool{"": true, ".": true}
	var maxIno uint32
	for {
		e, err := readNewc(r)
		if err != nil {
			return err
		}
		if e.name == newcTrailer {
			if err := addMissing(w, pending, dirs, maxIno); err != nil {
				return err
			}
			// What follows the trailer is padding: the kernel needs none.
			return writeNewc(w, e)
		}
		if e.fields[0] > maxIno {
			maxIno = e.fields[0]
		}
		name := cleanName(e.name)
		if e.mode()&modeType == modeDir {
			dirs[name] = true
		}
		if edit, ok := pending[name]; ok {
			if e.mode()&modeType != modeFile {
				return fmt.Errorf("initrd %s is not a regular file", name)
			}
			if e.fields[4] > 1 {
				return fmt.Errorf("initrd %s is a hard link", name)
			}
			if e.data, err = edit(e.data); err != nil {
				return fmt.Errorf("initrd %s: %w", name, err)
			}
			delete(pending, name)
		}
		if err := writeNewc(w, e); err != nil {
			return err
		}
	}
}

func addMissing(w io.Writer, pending map[string]Edit, dirs map[string]bool, maxIno uint32) error {
	names := make([]string, 0, len(pending))
	for name := range pending {
		names = append(names, name)
	}
	sort.Strings(names)
	ino := maxIno
	entry := func(name string, mode uint32, data []byte) error {
		ino++
		e := &newcEntry{name: name, data: data}
		e.fields[0] = ino
		e.fields[1] = mode
		e.fields[4] = 1
		if mode&modeType == modeDir {
			e.fields[4] = 2
		}
		return writeNewc(w, e)
	}
	for _, name := range names {
		var parents []string
		for dir := path.Dir(name); !dirs[dir]; dir = path.Dir(dir) {
			parents = append([]string{dir}, parents...)
		}
		for _, dir := range parents {
			if err := entry(dir, modeDir|0o755, nil); err != nil {
				return err
			}
			dirs[dir] = true
		}
		data, err := pending[name](nil)
		if err != nil {
			return fmt.Errorf("initrd %s: %w", name, err)
		}
		if err := entry(name, modeFile|0o644, data); err != nil {
			return err
		}
	}
	return nil
}

// SetKeys is an Edit of a KEY=value file (pantavisor.config, a policy):
// each key gets its value, on the line that set it or a new one.
func SetKeys(values map[string]string) Edit {
	return func(old []byte) ([]byte, error) {
		keys := make([]string, 0, len(values))
		for k, v := range values {
			if strings.ContainsAny(k+v, "\r\n\x00") || strings.Contains(k, "=") {
				return nil, fmt.Errorf("%s is not a one-line key and value", k)
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		done := map[string]bool{}
		var out []string
		text := strings.TrimRight(string(old), "\n")
		if text != "" {
			for _, line := range strings.Split(text, "\n") {
				key, _, found := strings.Cut(strings.TrimSpace(line), "=")
				key = strings.TrimSpace(key)
				if v, ok := values[key]; found && ok && !strings.HasPrefix(key, "#") {
					if done[key] {
						continue
					}
					line = key + "=" + v
					done[key] = true
				}
				out = append(out, line)
			}
		}
		for _, k := range keys {
			if !done[k] {
				out = append(out, k+"="+values[k])
			}
		}
		return []byte(strings.Join(out, "\n") + "\n"), nil
	}
}

// Replace is an Edit that sets the content whatever it was.
func Replace(content []byte) Edit {
	return func([]byte) ([]byte, error) { return content, nil }
}
