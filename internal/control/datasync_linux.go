package control

import (
	"os"

	"golang.org/x/sys/unix"
)

// datasync makes written file data durable without flushing metadata that
// data integrity does not need (mtime); a size change is still flushed.
func datasync(f *os.File) error { return unix.Fdatasync(int(f.Fd())) }
