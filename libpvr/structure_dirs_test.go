package libpvr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMakeStructureDirsKeepsSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "var"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Yocto's var/tmp points at a volatile dir that is absent here.
	if err := os.Symlink("volatile/tmp", filepath.Join(root, "var/tmp")); err != nil {
		t.Fatal(err)
	}
	if err := makeStructureDirs(root); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(root, "var/tmp")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("var/tmp was replaced: %v %v", info, err)
	}
	for _, dir := range []string{"proc", "sys", "dev", "tmp", "run", "var/run"} {
		if info, err := os.Stat(filepath.Join(root, dir)); err != nil || !info.IsDir() {
			t.Fatalf("%s was not made: %v", dir, err)
		}
	}
}
