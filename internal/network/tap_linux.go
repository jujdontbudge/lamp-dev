//go:build linux

package network

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// OpenPrivateTAP opens an already-created TAP in the supervisor's private
// namespace, then restores the host namespace before any provider can dial.
// Only this locked OS thread switches; the resulting FD is the engine binding.
func OpenPrivateTAP(namespace, name string) (*os.File, error) {
	if name == "" || len(name) > 15 || namespace == "" {
		return nil, ErrDenied
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return nil, ErrDenied
		}
	}
	if !strings.HasPrefix(namespace, "/") {
		return nil, ErrDenied
	}
	type result struct {
		file *os.File
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		host, err := os.Open("/proc/thread-self/ns/net")
		if err != nil {
			resultCh <- result{err: ErrUnavailable}
			return
		}
		defer host.Close()
		target, err := os.Open(namespace)
		if err != nil {
			resultCh <- result{err: ErrUnavailable}
			return
		}
		defer target.Close()
		hostInfo, e1 := host.Stat()
		targetInfo, e2 := target.Stat()
		if e1 != nil || e2 != nil || os.SameFile(hostInfo, targetInfo) {
			resultCh <- result{err: ErrDenied}
			return
		}
		if unix.Setns(int(target.Fd()), unix.CLONE_NEWNET) != nil {
			resultCh <- result{err: ErrUnavailable}
			return
		}
		var tap *os.File
		openErr := func() error {
			socket, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				return err
			}
			defer unix.Close(socket)
			existing, err := unix.NewIfreq(name)
			if err != nil {
				return err
			}
			if unix.IoctlIfreq(socket, unix.SIOCGIFINDEX, existing) != nil {
				return ErrDenied
			}
			fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			if err != nil {
				return err
			}
			request, err := unix.NewIfreq(name)
			if err != nil {
				unix.Close(fd)
				return err
			}
			request.SetUint16(unix.IFF_TAP | unix.IFF_NO_PI)
			if unix.IoctlIfreq(fd, unix.TUNSETIFF, request) != nil {
				unix.Close(fd)
				return ErrUnavailable
			}
			tap = os.NewFile(uintptr(fd), "assigned-private-ethernet")
			return nil
		}()
		if unix.Setns(int(host.Fd()), unix.CLONE_NEWNET) != nil {
			// Never put a wrong-namespace thread back into the Go scheduler and never
			// let a provider silently dial from an uncertain routing context.
			fmt.Fprintln(os.Stderr, "lamp-network: host namespace restore failed")
			os.Exit(70)
		}
		if openErr != nil {
			resultCh <- result{err: ErrUnavailable}
			return
		}
		resultCh <- result{file: tap}
	}()
	r := <-resultCh
	return r.file, r.err
}

// DropGuardPrivileges happens after opening the assigned descriptor and audit
// segment, before provider enrollment or guest release. No network capability
// is needed to read the FD or create the selected host-side sockets.
func DropGuardPrivileges(uid int) error {
	if uid < 65536 || uid == int(^uint32(0)) {
		return ErrDenied
	}
	// prctl's bounding-set/no-new-privileges flags are thread-scoped. Apply
	// them to every Go OS thread, not whichever thread currently runs main.
	// cgo builds cannot guarantee this and must fail admission (ENOTSUP).
	prctlAll := func(option int, arg uintptr) error {
		_, _, errno := syscall.AllThreadsSyscall6(unix.SYS_PRCTL, uintptr(option), arg, 0, 0, 0, 0)
		if errno != 0 {
			return errno
		}
		return nil
	}
	if os.Geteuid() == 0 {
		for cap := 0; cap < 64; cap++ {
			if err := prctlAll(unix.PR_CAPBSET_DROP, uintptr(cap)); err != nil && err != unix.EINVAL {
				return ErrUnavailable
			}
		}
		if unix.Setgroups(nil) != nil || unix.Setgid(uid) != nil || unix.Setuid(uid) != nil {
			return ErrUnavailable
		}
	} else if os.Geteuid() != uid {
		return ErrDenied
	}
	if prctlAll(unix.PR_SET_DUMPABLE, 0) != nil || prctlAll(unix.PR_SET_NO_NEW_PRIVS, 1) != nil {
		return ErrUnavailable
	}
	return unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
}
