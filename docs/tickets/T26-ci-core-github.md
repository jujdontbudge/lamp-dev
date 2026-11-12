# CI core and GitHub

## Goal
Let a CI system hand each job a fresh Lamp sandbox. When this is done: GitHub Actions jobs run on one-job Lamp runners with native results, logs, artifacts and cancellation, for ordinary workflows, the Copilot cloud agent, and the Claude Code and Codex actions (CI-002); a GitHub workflow identity exchanges for a short-lived scoped Lamp key with no stored Lamp secret (CI-003); every job gets a fresh sandbox, deleted when the job ends unless a trusted-job policy keeps it stopped for inspection (CI-004, LIF-004); and a stranger's public pull request runs in a fresh real VM with an allow list and only its CI system's own one-job token (CI-005, B2). The job lifecycle is ready for the other CI systems to plug into.

CI is one of the three things Lamp is for, and journey 4 is this ticket: Alice connects a CI system, each job gets a fresh computer, results go back, and outputs stay available after cleanup.

## Context
- Feature prerequisites: T12, T18, T19. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh owner-air`. All three are the owner’s everyday machines.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `<owner-notes>/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- `gh` on the M5 is signed in as the owner's GitHub account, `JohanSardar`. The Lamp repository is `JohanSardar/lamp`, private.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `<owner-skills>/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Test jobs and fixtures use public third-party projects and disposable test accounts, never the owner's own projects or repositories.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
