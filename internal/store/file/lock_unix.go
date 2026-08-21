//go:build unix

package file

import (
	"os"

	"golang.org/x/sys/unix"
)

func flock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func funlock(f *os.File) {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
