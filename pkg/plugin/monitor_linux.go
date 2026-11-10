package plugin

// systemd's private per-worker cgroup enforces aggregate memory, swap, CPU and
// PID limits, including descendants; bwrap supplies authority confinement.
func (w *Worker) monitor() {}
