//go:build linux

package control

import (
	"fmt"
	"golang.org/x/sys/unix"
	"net"
)

func authenticatePeer(conn *net.UnixConn, uid, pid int) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var check error
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if e != nil {
			check = e
			return
		}
		if int(cred.Uid) != uid || (pid > 0 && int(cred.Pid) != pid) {
			check = fmt.Errorf("Unix peer credentials do not match assigned UID/PID")
		}
	})
	if err != nil {
		return err
	}
	return check
}
