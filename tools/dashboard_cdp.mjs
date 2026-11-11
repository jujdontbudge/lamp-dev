// Headless Chrome driver for the T27 dashboard demonstration. It starts its
// own disposable profile (never an owner profile or visible window) and reads
// one JSON command per stdin line: goto, eval, click, shot. Replies one JSON
// line per command on stdout.
import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import readline from 'node:readline';

// LAMP_CDP_ENDPOINT=http://127.0.0.1:PORT attaches to an already running,
// disposable headless browser (for example Playwright's headless shell on
// another machine reached through an SSH tunnel) instead of starting one.
const endpoint = process.env.LAMP_CDP_ENDPOINT;
const chrome = process.argv[2] || '/usr/bin/google-chrome';
const profile = endpoint ? null : mkdtempSync(join(tmpdir(), 'lamp-t27-chrome-'));
const proc = endpoint ? null : spawn(chrome, ['--headless=new', '--no-first-run', '--no-default-browser-check', '--disable-extensions', '--disable-sync',
  '--no-proxy-server', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--window-size=1400,1000', 'about:blank'], { stdio: 'ignore' });
let pageTarget = null;
const cleanup = () => {
  if (proc) { try { proc.kill('SIGTERM'); } catch (_) {} setTimeout(() => rmSync(profile, { recursive: true, force: true }), 500); }
};
process.on('exit', cleanup);

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let base = endpoint;
for (let i = 0; i < 100 && !base; i++) {
  const f = join(profile, 'DevToolsActivePort');
  if (existsSync(f)) base = 'http://127.0.0.1:' + readFileSync(f, 'utf8').split('\n')[0];
  else await sleep(100);
}
if (endpoint) pageTarget = await (await fetch(`${base}/json/new?about:blank`, { method: 'PUT' })).json();
const page = pageTarget || (await (await fetch(`${base}/json/list`)).json()).find((t) => t.type === 'page');
// Over a tunnel the advertised socket host is the remote's loopback; use ours.
const wsUrl = page.webSocketDebuggerUrl.replace(/^ws:\/\/[^/]+/, base.replace(/^http/, 'ws'));
const ws = new WebSocket(wsUrl);
await new Promise((r) => ws.addEventListener('open', r, { once: true }));
let seq = 0;
const pending = new Map();
ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data);
  if (process.env.LAMP_CDP_DEBUG) process.stderr.write(String(ev.data).slice(0, 300) + '\n');
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); }
});
const send = (method, params = {}) => new Promise((resolve) => { const id = ++seq; pending.set(id, resolve); ws.send(JSON.stringify({ id, method, params })); });
await send('Page.enable');
await send('Runtime.enable');
if (process.env.LAMP_CDP_DEBUG) await send('Network.enable');
// Dashboard confirm() dialogs are accepted by the demo, as a person would.
await send('Page.addScriptToEvaluateOnNewDocument', { source: 'window.confirm = () => true;' });
await send('Emulation.setDeviceMetricsOverride', { width: 1400, height: 1000, deviceScaleFactor: 1, mobile: false });

const evaluate = async (expression) => {
  const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (r.result && r.result.exceptionDetails) throw new Error(r.result.exceptionDetails.text + ' ' + JSON.stringify(r.result.exceptionDetails.exception || {}));
  return r.result && r.result.result ? r.result.result.value : null;
};

const rl = readline.createInterface({ input: process.stdin });
for await (const line of rl) {
  const cmd = JSON.parse(line);
  let out = {};
  try {
    if (cmd.op === 'goto') { await evaluate(`location.href = ${JSON.stringify(cmd.url)}; true`); await sleep(cmd.wait || 1500); }
    else if (cmd.op === 'eval') out.value = await evaluate(cmd.expr);
    else if (cmd.op === 'click') {
      out.value = await evaluate(`(() => { const el = document.querySelector(${JSON.stringify(cmd.selector)}); if (!el) return false; el.scrollIntoView({block:'center'}); el.click(); return true; })()`);
      await sleep(cmd.wait || 1500);
    } else if (cmd.op === 'shot') {
      const r = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: !!cmd.full });
      writeFileSync(cmd.path, Buffer.from(r.result.data, 'base64'));
      out.path = cmd.path;
    } else if (cmd.op === 'quit') { process.stdout.write(JSON.stringify({ ok: true }) + '\n'); break; }
    out.ok = true;
  } catch (e) { out = { ok: false, error: String(e.message || e) }; }
  process.stdout.write(JSON.stringify(out) + '\n');
}
if (pageTarget) await fetch(`${base}/json/close/${pageTarget.id}`).catch(() => {});
ws.close();
cleanup();
process.exit(0);
