# One fleet spans many hosts and looks like one machine

## Original decision — preserved under the 2026-11-09 removal amendment

> **Superseded by the 2026-11-09 removal decision; original wording preserved:** A Lamp install is a fleet: any number of hosts behind one address and one set of keys. Hosts can be laptops, spare computers or cloud VMs, running Linux, macOS or Windows. Lamp places each sandbox on a host with room and the right OS and devices, and a caller can pin it to a specific host. Consoles such as T3 Code or Herdr see the whole fleet as a single machine, and each session in it lives in its own sandbox. We chose this over one independent install per machine for two reasons: consoles link machines one at a time by hand, and sub-agents spread across hosts still have to show up in one place. Coordination moves to another host automatically when one goes away, because laptops that sleep are normal hosts.

## Amendment — 2026-11-09

Owner decision: “skip windows for everything.” The original paragraph above is a historical record; its Windows support statement is superseded for release one. Hosts are Linux x86_64/arm64 (Linux 5.10+) and Apple Silicon macOS 26+. Linux guests run on both host families; native macOS guests run only on Macs. The fleet model and placement rules still apply. No host, guest, test lane, CLI build or CI runner for the removed platform is in release one.
