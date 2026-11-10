package control

import "os"

// datasync is fsync on macOS, which has no separate fdatasync.
func datasync(f *os.File) error { return f.Sync() }

func reserveLog(*os.File, int64) (bool, error) { return false, nil }
