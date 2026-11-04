//go:build unix

package main

import (
	"errors"
	"os"
	"syscall"
)

func preserveOwner(path string, source os.FileInfo) error {
	owner, ok := source.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot determine source database ownership")
	}
	destination, err := os.Stat(path)
	if err != nil {
		return err
	}
	current, ok := destination.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot determine backup ownership")
	}
	if current.Uid == owner.Uid && current.Gid == owner.Gid {
		return nil
	}
	return os.Chown(path, int(owner.Uid), int(owner.Gid))
}
