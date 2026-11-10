package control

import "golang.org/x/sys/unix"

// CLOCK_BOOTTIME advances across system suspend and ignores wall-clock changes.
func authorityTicks() (int64, error) {
	var ts unix.Timespec
	err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts)
	return ts.Nano(), err
}
