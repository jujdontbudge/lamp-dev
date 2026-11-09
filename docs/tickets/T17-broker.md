# Broker: API keys, settings and signed-in connections

## Goal
Build `lamp-broker`, so agents can use the owner's accounts without ever holding the keys. When this is done: model API keys stay behind a host proxy and never enter the task sandbox (CRED-005); chosen settings copy one way into sandboxes, and sandbox edits come back only through an explicit, authenticated, selected-path approval, with signing keys kept out of task memory (CRED-006); signed-in MCP servers are reached through a gateway (CON-001); the signed-in CLIs `gh`, `stripe` and `aws` do real authorized work without their tokens entering the sandbox (CON-002); and each owner-machine service offers `off`, `read_only`, `ask_first` or `full`, enforced on real operations (CON-003). A real harness does useful work on the API-key route with zero key canaries in the guest, its output, the logs or any capture.

Lamp's promise is zero leaked API keys, passwords or browser cookies across the whole attack suite, while agents still get ready-to-use sign-in. Sessions (T21), kits (T22) and metering (T23) all build on this broker.

## Context
- Ticket T17 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T10's control core and T11's plugin host. Four tickets run at the same time: T14 (format freeze), T15 (Mac devices and fallback), T16 (fleet) and T18 (access). This ticket owns `lamp-broker`. T22 adds the browser and password-manager kits on top, where E08 completes; T23 adds exact proxy metering.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- An OpenRouter key is in `~/.pi/agent/auth.json` on the M5. That account has almost no credit, so only its free models work.

## Constraints
- Never print, log or commit a secret.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- Test jobs and fixtures use public third-party projects and disposable test accounts, never the owner's own projects or repositories.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
