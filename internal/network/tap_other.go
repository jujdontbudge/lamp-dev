//go:build !linux && !darwin

package network

import "os"

// Mac release admission waits for T8's engine-owned Ethernet handle and
// qualified service profile. There is deliberately no vmnet/ambient fallback.
func OpenPrivateTAP(namespace, name string) (*os.File, error) { return nil, ErrUnavailable }
func DropGuardPrivileges(uid int) error                       { return ErrUnavailable }
