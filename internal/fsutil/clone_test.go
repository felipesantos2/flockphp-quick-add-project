package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCloneTreeCopiesFilesModesAndSymlinks(t *testing.T) {
	src := filepath.Join(t.TempDir(), "vendor")
	if err := os.MkdirAll(filepath.Join(src, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "autoload.php"), []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bin", "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("bin/tool", filepath.Join(src, "tool")); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "vendor")
	if err := CloneTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "autoload.php")); err != nil || string(data) != "<?php\n" {
		t.Errorf("autoload.php: %q %v", data, err)
	}
	// Windows has no executable bit.
	if info, err := os.Stat(filepath.Join(dst, "bin", "tool")); err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Errorf("bin/tool lost its mode: %v %v", info, err)
	}
	if link, err := os.Readlink(filepath.Join(dst, "tool")); err != nil || filepath.ToSlash(link) != "bin/tool" {
		t.Errorf("symlink: %q %v", link, err)
	}

	// A clone is a copy: changing it leaves the original alone.
	if err := os.WriteFile(filepath.Join(dst, "autoload.php"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(src, "autoload.php")); string(data) != "<?php\n" {
		t.Errorf("the original changed: %q", data)
	}

	file := filepath.Join(t.TempDir(), "copy.php")
	if err := CloneFile(filepath.Join(src, "autoload.php"), file); err != nil {
		t.Fatal(err)
	}
	t.Logf("copy-on-write here: %v", CanClone(filepath.Join(src, "autoload.php"), filepath.Dir(dst)))
}
