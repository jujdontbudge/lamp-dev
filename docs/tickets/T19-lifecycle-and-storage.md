# Lifecycle and storage

## Goal
Make sandboxes cheap to keep and easy to come back to. When this is done: an idle sandbox suspends after its grace period, and a message, exec, SSH attach or allowed dev request resumes it (LIF-001), using the owner's exact definition of idle (LIF-002, A5); expiry and per-sandbox overrides work (LIF-003); warm pools hand out never-used, signed-out sandboxes (LIF-005); a caller can fork mid-task, run alternatives, keep the winner and bring the chosen changes back through git without blind overwrite (LIF-006); shared caches (STO-001), named volumes with several attachments (STO-002), CI outputs and logs that outlive their job (STO-003), owner-only persistence by default (STO-004), and durability, encrypted backup and restore (STO-005) all work; installed tools can be saved into a template, rebuilt clean when the source sandbox is tainted (TPL-002); and named recipes resolve atomically to immutable versions (TPL-003).

Journey 3 is trying two approaches and keeping the winner; journey 4 is keeping a CI job's outputs after its sandbox is gone. Owning your machines and your data is one of Lamp's four equal goals, and so is having tools ready without setting them up again.

## Context
- Feature prerequisites: T7, T10. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh johansardars-macbook-air`. All three are the owner’s everyday machines.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `/Users/johansardar/.claude/skills/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Power-loss and write-reordering faults run only on disposable fixtures, never on a machine's live disk.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
