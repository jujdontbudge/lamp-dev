package network

import (
	"os"

	"golang.org/x/sys/unix"
)

// AdoptEthernetFD accepts only a private connected Unix datagram/seqpacket
// endpoint. Linux TAPs are opened by OpenPrivateTAP, not arbitrary file paths.
// The supervisor transfers exclusive ownership; regular files/stdio/streams
// must never be usable as a packet writer.
func AdoptEthernetFD(fd int) (*os.File, error) {
	if fd < 3 {
		return nil, ErrDenied
	}
	fail := func() (*os.File, error) { unix.Close(fd); return nil, ErrDenied }
	kind, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil || (kind != unix.SOCK_DGRAM && kind != unix.SOCK_SEQPACKET) {
		return fail()
	}
	peer, err := unix.Getpeername(fd)
	if err != nil {
		return fail()
	}
	if _, ok := peer.(*unix.SockaddrUnix); !ok {
		return fail()
	}
	unix.CloseOnExec(fd)
	if unix.SetNonblock(fd, true) != nil {
		return fail()
	}
	file := os.NewFile(uintptr(fd), "assigned-private-ethernet")
	if file == nil {
		return fail()
	}
	return file, nil
}
