package network

import "os"

func OpenPrivateTAP(namespace, name string) (*os.File, error) { return nil, ErrUnavailable }

// The engine starts the helper under its dedicated account and Seatbelt profile,
// handing it only a connected Ethernet descriptor. It never starts as root.
func DropGuardPrivileges(uid int) error {
	if uid <= 0 || os.Geteuid() != uid {
		return ErrDenied
	}
	return nil
}
