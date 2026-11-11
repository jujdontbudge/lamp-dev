//go:build darwin

package main

// RLIMIT_CORE is set before opening any credentials. The OS owner and privileged
// debugger are trusted on macOS; no key material is delivered to a task process.
func disableDumps() error { return nil }
