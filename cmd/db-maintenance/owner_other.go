//go:build !unix

package main

import "os"

// Non-Unix systems have no POSIX uid/gid to preserve.
func preserveOwner(string, os.FileInfo) error { return nil }
