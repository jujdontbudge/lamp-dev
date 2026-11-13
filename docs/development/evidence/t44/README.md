# T44 live evidence

These are real CLI, HTTP and MCP calls against disposable Ubuntu Firecracker
VMs on the owner's Ubuntu machine. No host Docker daemon, owner browser,
visible desktop, account or vault was used. Only public package registries,
example.com and guest-local files were involved. The template derives from
the public Ubuntu seed used by T35, with the T36 guest helper; the final Docker
run also uses the guest init built from this change. This is not a fresh
release-installer test on every host. Apple Silicon execution was not run.

Completion arrays in the saved JSON are summarized as `returned_items` and
`items_sample`; counts and the server's own truncation fields are preserved.

## Observations

- The built-in catalogue works without an owner kits configuration.
- Create with the Node kit completes after installation. Node prints `42`.
- Python prints `41` and returns `42`, then `43`; a fresh context gets NameError;
  `1/0` returns structured ZeroDivisionError; matplotlib returns the attached PNG.
  Restarting lampd preserves the namespace and returns `44`.
- Chromium installs, navigates to example.com, evaluates `document.title`, and
  screenshots a Python http.server page inside the guest: `browser-local.png`.
- TypeScript LSP start/open/symbols/completions/diagnostics/close/stop work through
  the CLI. MCP independently returns `greet` and non-empty completions. There
  are 983 completions, with 580 returned and truncation explicitly reported.
- The desktop reports 1024x768 and mouse position (100,100), accepts `echo hi`
  and Return, and returns a screenshot showing `hi` plus an MP4 recording.
  The guest HTTP screenshot route returns 200 image/png even with a refresh
  query string. See `desktop.png` and `desktop.mp4`.
- The final fresh VM is created with the Docker kit and pulls Alpine 3.20 from
  Docker Hub, then prints `DIND_OK`. The literal command without a network flag
  also prints `DIND_OK`. That default network warns that forwarding is off;
  `--network host` shares Docker's private, slirp-backed guest namespace for
  outbound connectivity. No host Docker resources were touched.

## Iteration failures retained honestly

`first-run.json` is a partial run, not an overall success. It reached Python
and desktop but then lost the audited relay during LSP installation. Another
attempt lost the relay during Python installation. Python subprocess launches
now use plain fork under the existing collector rather than vfork.

The unpinned npm TypeScript release was 7.0.2, without the tsserver layout used
by typescript-language-server. Startup failed; the kit now installs 5.9.3 and
passes its tsserver path explicitly. The corrected CLI and MCP runs succeeded.

Docker first started but failed container setup with mount/network permission
errors. A single-UID nested user namespace additionally failed chown of
/etc/shadow. The final implementation retains Lamp's full mapped UID range,
uses private mount/network namespaces, rootless runc, and slirp4netns. TUN
originally had mode 0600, so guest init now gives it standard 0666 permissions;
kernel namespace capability checks still control interface creation.

Some runs lost external DNS after the existing network audit heartbeat stopped.
The implementation does not weaken that enforcement. A suspend/resume restored
networking and Chromium then navigated successfully. The first recovery attempt
also exposed a demo grant restricted to generation 1; subsequent disposable
worker grants explicitly authorized recovery generations.

Initial preview attempts used the wrong authorization flow (API token, then a
fragment link without executing its JavaScript exchange). The corrected probe
exchanges the share token privately for a cookie. On an earlier VM it reached
the proxy but returned 502; guest-local desktop HTTP still returned 200. These
failed attempts are not counted as successful external view evidence.
After the final VM recovered through suspend/resume, the authenticated private
view returned 200 for both `/` and `/screen.png?t=proof`. See `preview.json` and
`preview-desktop.png`. No share bearer or preview cookie is recorded.

## Local checks

`make build`, focused `go vet ./internal/control ./cmd/lamp ./internal/surface`,
Python compilation, public generator checks, SDK generator checks, and
`git diff --check` passed. No new automated tests or benchmark runs were added.
The earlier contracts check reports unrelated existing fleet field drift for
`unavailable_host_ids` and `stranded_sandbox_ids`; it is not called a pass here.

The final sandbox deletion completed with cleanup_state `complete`. Its worker
and daemon stopped, no Firecracker process remained, and the task's temporary
VM stores, fixtures, binaries and disposable credentials were removed.
