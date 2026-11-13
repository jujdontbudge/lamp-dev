# Local dashboard

After installation, open the printed `dashboard_sign_in` file URL, or run
`lamp dashboard`. Installed clients discover their own prefix; `--socket
/path/to/api.sock` selects another local installation. The private file signs
you in without pasting an owner token. It exchanges a one-use code by POST;
the cookie lasts eight hours or until daemon restart. Keep the file private.
`dashboard_url` opens the page in an already signed-in browser.

The installer enables a loopback listener on an available port. A manually
started daemon can use `--dashboard-listen 127.0.0.1:0`. This listener is local;
there is no hosted dashboard or automatic public ingress.

Sandbox cards show CPU history, memory, disk, network counters and recent boot,
engine and command logs. The same observations are available with:

```sh
lamp sandbox metrics ID --limit 60
lamp sandbox logs ID --limit 60
```

HTTP uses `GET /v1/sandboxes/{sandbox_id}/metrics` and `/logs`; MCP exposes the
same operations. Metrics sample every five seconds, retain up to one hour and
survive daemon restart. CPU 100% means one busy core. Missing counters are
unavailable. Older guest templates need the current helper to report metrics.
Logs retain bounded lifecycle events, engine console tails and completed exec
output tails; use artifacts for durable output archives.
