// Package guard is the control-side guard integration boundary. The Linux packet
// and process correctness path lives in crates/lamp-guard; supervisor admission,
// assigned TAP/stream handles and remaining gates are documented in
// docs/development/T4-guard.md. No Go-side unguarded fallback is implemented.
package guard
