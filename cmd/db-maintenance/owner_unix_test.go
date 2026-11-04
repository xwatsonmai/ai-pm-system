//go:build unix

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestBackupPreservesSourceOwner(t *testing.T) {
	source, _ := fixture(t)
	destination := filepath.Join(filepath.Dir(source), "ownership.db")
	if code, out, stderr := invoke(t, "backup", source, destination); code != 0 {
		t.Fatal(code, out, stderr)
	}
	before, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	a, b := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	if a.Uid != b.Uid || a.Gid != b.Gid {
		t.Fatal("backup changed database ownership")
	}
}
