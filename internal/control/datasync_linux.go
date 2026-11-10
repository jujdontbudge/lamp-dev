package control

import (
	"os"

	"golang.org/x/sys/unix"
)

// datasync makes written file data durable without flushing metadata that
// data integrity does not need (mtime); a size change is still flushed.
func datasync(f *os.File) error { return unix.Fdatasync(int(f.Fd())) }

// Reserve physical blocks without writing tens of MiB of zeros in a commit's
// critical path. Unwritten extents read as zero; fdatasync after each record
// also commits the extent conversion required to retrieve that data.
func reserveLog(f *os.File, end int64) (bool, error) {
	err := unix.Fallocate(int(f.Fd()), 0, 0, end)
	if err == unix.EOPNOTSUPP || err == unix.ENOSYS {
		return false, nil
	}
	return err == nil, err
}
