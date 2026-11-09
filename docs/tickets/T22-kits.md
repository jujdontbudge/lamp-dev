# Kits: browser, takeover, password managers, toolchains and Docker

## Goal
Give sandboxes ready-to-use tools without handing agents the owner's secrets. When this is done: a logged-in browser runs outside the agent's sandbox by default, in its own locked-down sandbox (KIT-001); a browser inside the sandbox with copied logins is opt-in and flagged (KIT-002); a person can privately take over the browser and hand it back on a safe page (KIT-003); toolchain kits install requested tools, and Docker runs only inside the sandbox with no host daemon or socket (KIT-004); and 1Password, Bitwarden, KeePassXC and Proton Pass each fill standard login forms on the person's behalf with per-vault approvals, with the person typing privately wherever Lamp cannot prove the password stays hidden from the agent (KIT-005, B3). E08 passes: real authorized work through every credential route, with zero unauthorized key, password or cookie canaries in the guest, output, logs, captures or hostile sinks.

Ready tools and sign-in is one of Lamp's four equal goals, and so is protecting secrets. Journey 5 has Alice privately take over a browser and hand it back on a safe page.

## Context
- Ticket T22 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T11's plugin host and T17's broker. Five tickets run at the same time: T19 (lifecycle and storage), T20 (Linux breadth), T21 (sessions and consoles), T23 (budgets, audit, revocation) and T24 (hosted side). This ticket owns the browser service, the password-manager adapters and the kits. T27 builds the takeover and approval views in the dashboard.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- The owner named 1Password and Proton among the accounts they already pay for.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- Test jobs and fixtures use public third-party projects and disposable test accounts, never the owner's own projects or repositories.
- The owner's real vault items, browser profiles and logins are never test data. Use disposable test vaults and test sites.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
