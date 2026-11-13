package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

func FileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CopyFile uses bounded memory and preserves zero-filled seed disk extents as
// holes. A 20 GiB guest disk must never need 20 GiB of installer memory or space.
func CopyFile(src, dst string, mode os.FileMode) (string, int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	return CopyReader(in, dst, mode)
}

func CopyReader(in io.Reader, dst string, mode os.FileMode) (string, int64, error) {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return "", 0, err
	}
	defer out.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	zeros := make([]byte, len(buf))
	var total int64
	for {
		n, e := io.ReadFull(in, buf)
		if n > 0 {
			h.Write(buf[:n])
			total += int64(n)
			if bytes.Equal(buf[:n], zeros[:n]) {
				_, err = out.Seek(int64(n), io.SeekCurrent)
			} else {
				_, err = out.Write(buf[:n])
			}
			if err != nil {
				return "", total, err
			}
		}
		if e == io.EOF || e == io.ErrUnexpectedEOF {
			break
		}
		if e != nil {
			return "", total, e
		}
	}
	if err = out.Truncate(total); err != nil {
		return "", total, err
	}
	if err = out.Close(); err != nil {
		return "", total, err
	}
	return hex.EncodeToString(h.Sum(nil)), total, nil
}
