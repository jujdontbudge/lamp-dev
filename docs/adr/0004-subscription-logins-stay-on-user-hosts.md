# Harness subscription logins stay on the user's own hosts

Each harness signs in through its vendor's approved route: Anthropic's own sign-in or `claude setup-token`, OpenAI's sign-in program for open-source apps, Copilot device login, and so on. The login is stored only on the user's hosts and placed only into that user's sandboxes, where host network rules limit it to the vendor's servers. Lamp-run servers, the relay included, never hold or proxy model credentials. API keys work differently: a host proxy swaps them in, so they never enter sandboxes.

## Where the login is kept and where it's stripped

Amended 2026-11-08: the original rule stripped the login before every fork, but a memory fork can't remove a token the running harness holds in memory.

- **Kept:** forks and suspends of the user's own sandboxes. The login never leaves their hosts or their sandboxes, so the fork or resumed sandbox carries on signed in.
- **Stripped:** templates, snapshots that leave the user's hosts, and any sandbox shared with or handed to another person. These start the harness fresh.

## Considered options

- **A central encrypted store injected into sandboxes** (Cloudroom's approach). Rejected, because Anthropic's terms forbid developers from collecting, storing or intermediating Claude.ai credentials.
- **A host proxy for subscription tokens too** (Docker Sandboxes' local approach). Better isolation, but no vendor has confirmed it's allowed. Revisit for each vendor that confirms in writing.

## Consequences

- The "0 leaks" security target covers API keys, passwords and browser cookies. Subscription tokens are a documented exception.
- Lamp can't decrypt subscription traffic. The audit log records each connection's destination, bytes and timing, but not request content. Token caps for that traffic use the usage the harness reports, backed by hard time caps.
