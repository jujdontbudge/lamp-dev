package control

import "golang.org/x/sys/unix"

// Darwin CLOCK_MONOTONIC_RAW uses mach_continuous_time, including sleep.
func authorityTicks() (int64, error) {
	var ts unix.Timespec
	err := unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &ts)
	return ts.Nano(), err
}
