//go:build !unix

package file

import "os"

// Non-unix platforms get no cross-process lock; the file backend is then
// safe only for a single hauth process, which matches its test and
// single-workspace use.
func flock(f *os.File) error { return nil }

func funlock(f *os.File) {}
