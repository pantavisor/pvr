// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package libpvr

import (
	"os"
	"path/filepath"
)

// makeStructureDirs adds the directories a read-only container needs to
// root. Images may ship them as symlinks, such as Yocto's var/tmp to
// volatile/tmp, which do not resolve on the build host: what exists is kept.
func makeStructureDirs(root string) error {
	for _, dir := range structureDirs {
		path := filepath.Join(root, dir)
		if _, err := os.Lstat(path); err == nil {
			continue
		}
		if err := ExecCommand(MKDIR_CMD, "-p", path); err != nil {
			return err
		}
	}
	return nil
}
