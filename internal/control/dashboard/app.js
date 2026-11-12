// Lamp host-served dashboard (OPS-001, OPS-003). Static: every byte shown
// comes from the public API with the viewer's own credential, so the page
// shows exactly what that caller is allowed to see.
'use strict';

const LISTS = {
  fleets: '/v1/fleets', hosts: '/v1/hosts', sandboxes: '/v1/sandboxes', sessions: '/v1/sessions',
  tasks: '/v1/tasks', budgets: '/v1/budgets', approvals: '/v1/approvals', shares: '/v1/shares',
  leases: '/v1/leases', browsers: '/v1/browsers', kits: '/v1/kits', operations: '/v1/operations',
  audit: '/v1/audit-records', sinks: '/v1/notification-sinks', notifications: '/v1/notifications',
};
const SECTIONS = [
  ['overview', 'Overview'], ['hosts', 'Hosts'], ['sandboxes', 'Sandboxes'], ['sessions', 'Sessions'],
  ['tasks', 'Tasks & budgets'], ['approvals', 'Approvals'], ['browsers', 'Browsers & kits'],
  ['sharing', 'Sharing'], ['operations', 'Operations'], ['notifications', 'Notifications'],
  ['audit', 'Audit'], ['features', 'Features & errors'],
];
const FEATURES = [
  ['Sandboxes', ['sandbox_create', 'sandbox_fork', 'sandbox_suspend', 'sandbox_resume']],
  ['Sessions & consoles', ['session_create', 'session_attach', 'session_input', 'session_teleport']],
  ['Sharing', ['share_create', 'share_revoke']],
  ['Budgets & tasks', ['budget_create', 'task_create', 'task_stop']],
  ['Approvals', ['approval_approve', 'approval_deny']],
  ['Private browser takeover', ['browser_create', 'browser_takeover', 'browser_return_control']],
  ['Kits & vaults', ['kit_install', 'browser_fill_password']],
  ['Audit', ['audit_record_list', 'audit_record_export']],
  ['Notifications', ['notification_sink_create', 'notification_retry', 'notification_acknowledge']],
  ['Fleet & hosts', ['host_list', 'fleet_update']],
  ['Updates', ['update_create', 'update_rollback']],
  ['Storage & backup', ['volume_create', 'backup_create']],
  ['CI', ['ci_run_create']], ['Triggers & schedules', ['schedule_create']],
];

let token = '';
try { token = sessionStorage.getItem('lamp.token') || ''; } catch (_) {}
const state = { data: {}, errors: {}, installed: null, events: [], cursor: '', live: false, leases: {}, views: {}, me: '' };
const $ = (s) => document.querySelector(s);
const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const online = (h) => h.state === 'ready' || h.state === 'active';
const cls = (st) => ({ ready: 'ok', active: 'ok', running: 'ok', delivered: 'ok', succeeded: 'ok', approved: 'ok', consumed: 'ok', agent_control: 'ok', human_control: 'warn', pending: 'warn', blocked: 'warn', preparing: 'warn', starting: 'warn', suspended: 'warn', sleeping: 'warn', stopping: 'warn', failed: 'bad', denied: 'bad', unavailable: 'bad', outcome_unknown: 'bad', expired: 'bad', revoked: 'bad', stopped: 'muted', completed: 'muted', deleted: 'muted', acknowledged: 'muted' }[st] || '');
const pill = (st) => `<span class="pill ${cls(st)}">${esc(st)}</span>`;
const short = (id) => `<code title="${esc(id)}">${esc(id)}</code>`;
const when = (t) => t ? `<span title="${esc(t)}">${esc(new Date(t).toLocaleString())}</span>` : '<span class="muted">—</span>';
const countdown = (t, label) => t ? `<span class="pill" data-deadline="${esc(t)}" data-label="${esc(label)}">${esc(label)}</span>` : '';
const json = (v) => `<pre>${esc(JSON.stringify(v, null, 2))}</pre>`;
const items = (k) => state.data[k] || [];
const byId = (k, id) => items(k).find((x) => x.id === id);

async function api(method, path, body, revision) {
  const headers = { Accept: 'application/json' };
  if (token) headers.Authorization = 'Bearer ' + token;
  if (method !== 'GET') {
    headers['Content-Type'] = 'application/json';
    headers['Idempotency-Key'] = crypto.randomUUID();
    if (revision !== undefined) headers['If-Match'] = String(revision);
  }
  const res = await fetch(path, { method, headers, body: method === 'GET' ? undefined : JSON.stringify(body || {}), cache: 'no-store', credentials: 'omit' });
  const who = res.headers.get('X-Lamp-Principal');
  if (who) state.me = who;
  let out = null;
  try { out = await res.json(); } catch (_) { out = { error: { code: 'DATA_UNAVAILABLE', message: `HTTP ${res.status} without a JSON body` } }; }
  if (!res.ok || out.error) {
    const e = new Error((out.error && out.error.message) || `HTTP ${res.status}`);
    e.code = (out.error && out.error.code) || 'HTTP_' + res.status; e.status = res.status; e.requestId = out.request_id;
    throw e;
  }
  return out;
}

async function listAll(path) {
  let all = [], cursor = '';
  for (let i = 0; i < 20; i++) {
    const out = await api('GET', path + '?limit=500' + (cursor ? '&cursor=' + encodeURIComponent(cursor) : ''));
    all = all.concat(out.items || []);
    if (!out.next_cursor) break;
    cursor = out.next_cursor;
  }
  return all;
}

async function load() {
  const keys = Object.keys(LISTS).filter((k) => !(k === 'hosts' && state.installed && !state.installed.host_list));
  await Promise.all(keys.map(async (k) => {
    try { state.data[k] = await listAll(LISTS[k]); delete state.errors[k]; }
    catch (e) {
      if (e.status === 401) { signedOut(); throw e; }
      state.errors[k] = e;
      if (e.code === 'NOT_FOUND') state.data[k] = [];
    }
  }));
  state.loadedAt = new Date();
  $('#who').textContent = state.me ? 'signed in as ' + state.me : '';
  render();
}

async function signIn() {
  try {
    const docs = await api('GET', '/v1/docs?topic=architecture');
    state.installed = docs.installed_operations || {};
  } catch (e) {
    if (e.status === 401 || e.code === 'UNAUTHENTICATED' || e.code === 'SCOPE_DENIED') return signedOut(e.message);
    state.errors.docs = e;
  }
  $('#signin').hidden = true; $('#signout').hidden = !token;
  await load().catch(() => {});
  if (!state.live) watch();
}

function signedOut(message) {
  state.live = false; setLive();
  $('#signin').hidden = false; $('#signout').hidden = true; $('#app').innerHTML = ''; $('#decide').innerHTML = '';
  $('#signin-error').textContent = message || '';
}

// Live updates: long-poll the event feed, then refresh the resource lists.
async function watch() {
  state.live = true; setLive();
  let refresh = null;
  while (state.live) {
    try {
      const out = await api('GET', '/v1/events?limit=200&wait_ms=' + (state.liveOk ? 20000 : 0) + (state.cursor ? '&cursor=' + encodeURIComponent(state.cursor) : ''));
      state.cursor = out.next_cursor || state.cursor;
      state.liveOk = true; setLive();
      if ((out.items || []).length) {
        state.events = out.items.concat(state.events).slice(0, 200);
        clearTimeout(refresh);
        refresh = setTimeout(() => load().catch(() => {}), 250);
      }
    } catch (e) {
      if (e.status === 401) return signedOut();
      if (e.code === 'CURSOR_EXPIRED') { state.cursor = ''; load().catch(() => {}); continue; }
      state.liveOk = false; state.errors.events = e; setLive();
      await new Promise((r) => setTimeout(r, 3000));
    }
  }
}
function setLive() {
  const el = $('#live');
  el.textContent = state.live && state.liveOk ? 'live' : state.live ? 'reconnecting' : 'offline';
  el.className = 'pill ' + (state.live && state.liveOk ? 'ok' : state.live ? 'warn' : 'bad');
}

// ---------------------------------------------------------------- actions
async function act(label, fn) {
  try { await fn(); flash(label + ' done', 'ok'); }
  catch (e) { flash(`${label} failed: ${e.code}: ${e.message}${e.requestId ? ' (request ' + e.requestId + ')' : ''}`, 'bad'); }
  load().catch(() => {});
}
function flash(text, kind) {
  const d = document.createElement('div');
  d.className = 'card ' + kind; d.textContent = text;
  $('#errors').prepend(d);
  setTimeout(() => d.remove(), kind === 'bad' ? 20000 : 4000);
}
const ACTIONS = {
  approve: (id) => {
    const a = byId('approvals', id);
    if (a.labels && a.labels.source === 'kit') {
      // Vault fills are approved at the kit backend with the exact plan shown.
      const b = byId('browsers', a.labels.browser_id);
      return api('POST', `/v1/browsers/${a.labels.browser_id}:fill-password`, { approval_id: a.labels.kit_approval_id, plan: a.action_plan.arguments, wait_ms: 30000 }, b.revision);
    }
    return api('POST', `/v1/approvals/${id}:approve`, { action_digest: a.action_digest }, a.revision);
  },
  deny: (id) => { const a = byId('approvals', id); return api('POST', `/v1/approvals/${id}:deny`, { reason: 'denied on dashboard' }, a.revision); },
  'task-stop': (id) => { const t = byId('tasks', id); return api('POST', `/v1/tasks/${id}:stop`, { reason: 'owner' }, t.revision); },
  'share-revoke': (id) => { const s = byId('shares', id); return api('POST', `/v1/shares/${id}:revoke`, { reason: 'revoked on dashboard' }, s.revision); },
  'op-cancel': (id) => { const o = byId('operations', id); return api('POST', `/v1/operations/${id}:cancel`, {}, o.revision); },
  suspend: (id) => { const s = byId('sandboxes', id); return api('POST', `/v1/sandboxes/${id}:suspend`, {}, s.revision); },
  resume: (id) => { const s = byId('sandboxes', id); return api('POST', `/v1/sandboxes/${id}:resume`, {}, s.revision); },
  takeover: (id) => { const b = byId('browsers', id); return api('POST', `/v1/browsers/${id}:takeover`, {}, b.revision); },
  'return-control': (id) => { const b = byId('browsers', id); return api('POST', `/v1/browsers/${id}:return-control`, {}, b.revision); },
  view: async (id) => {
    const b = byId('browsers', id);
    const out = await api('POST', `/v1/browsers/${id}:action`, { action: 'screenshot', wait_ms: 30000 }, b.revision);
    state.views[id] = (out.result && out.result.observations) || out;
  },
  'browser-type': async (id, el) => {
    const b = byId('browsers', id), card = el.closest('.card');
    const selector = card.querySelector('[name=selector]').value, text = card.querySelector('[name=text]').value;
    await api('POST', `/v1/browsers/${id}:action`, { action: 'type', arguments: { selector, text }, wait_ms: 30000 }, b.revision);
  },
  // Delivery attempts advance the revision every few seconds; act on the current one.
  'note-retry': async (id) => { const n = (await api('GET', `/v1/notifications/${id}`)).data; return api('POST', `/v1/notifications/${id}:retry`, {}, n.revision); },
  'note-ack': async (id) => { const n = (await api('GET', `/v1/notifications/${id}`)).data; return api('POST', `/v1/notifications/${id}:acknowledge`, {}, n.revision); },
  'sink-test': (id) => { const s = byId('sinks', id); return api('POST', `/v1/notification-sinks/${id}:test`, {}, s.revision); },
  'sink-delete': (id) => { const s = byId('sinks', id); return api('DELETE', `/v1/notification-sinks/${id}`, {}, s.revision); },
  'sink-create': (_, el) => {
    const f = el.closest('.card');
    const types = [...f.querySelectorAll('[name=etype]:checked')].map((x) => x.value);
    return api('POST', '/v1/notification-sinks', { plugin_id: 'webhook', destination_ref: f.querySelector('[name=dest]').value, event_types: types, enabled: true });
  },
  share: (id, el) => {
    const card = el.closest('.card'), subject = card.querySelector('[name=subject]').value.trim();
    const actions = ['session.get', 'session.watch'].concat(card.querySelector('[name=cantype]').checked ? ['session.input'] : []);
    return api('POST', '/v1/shares', { resource_kind: 'session', resource_id: id, subject_id: subject, actions, expires_at: new Date(Date.now() + 8 * 3600000).toISOString() });
  },
  answer: async (id, el) => {
    const text = el.closest('.card').querySelector('[name=answer]').value + '\n';
    let lease = state.leases[id];
    if (!lease) {
      const out = await api('POST', '/v1/leases', { type: 'input', resource_id: id, expires_at: new Date(Date.now() + 4 * 60000).toISOString(), wait_ms: 10000 });
      lease = state.leases[id] = { id: out.result && out.result.lease_id, seq: 0 };
    }
    const data = btoa(String.fromCharCode(...new TextEncoder().encode(text)));
    // Output observations advance the session revision constantly: send
    // against the current one, retrying once if it moved in between.
    for (let attempt = 0; ; attempt++) {
      const s = (await api('GET', `/v1/sessions/${id}`)).data;
      try {
        await api('POST', `/v1/sessions/${id}:input`, { lease_id: lease.id, location_epoch: s.location_epoch || 0, sequence: lease.seq, data_base64: data, wait_ms: 10000 }, s.revision);
        break;
      } catch (e) { if (e.code !== 'REVISION_CONFLICT' || attempt > 2) throw e; }
    }
    lease.seq++;
  },
};
document.addEventListener('click', (ev) => {
  const b = ev.target.closest('button[data-act]');
  if (!b) return;
  ev.preventDefault();
  const name = b.dataset.act;
  if (b.dataset.confirm && !confirm(b.dataset.confirm)) return;
  act(b.textContent.trim(), () => ACTIONS[name](b.dataset.id, b));
});
const btn = (actName, id, label, extra = '') => `<button data-act="${actName}" data-id="${esc(id)}" ${extra}>${esc(label)}</button>`;

// ---------------------------------------------------------------- views
function route() {
  const [, section = 'overview', id = ''] = (location.hash || '#/overview').split('/');
  const known = SECTIONS.find(([k]) => k === section);
  // Action links name tasks/sessions/approvals; tasks live in the tasks section.
  return { section: known ? section : 'overview', id: decodeURIComponent(id) };
}

function hostsView() {
  const hosts = items('hosts');
  const fleet = items('fleets')[0];
  const sb = items('sandboxes');
  let out = '';
  if (fleet) out += `<div class="card"><h2>Fleet ${short(fleet.id)} ${pill(fleet.state)}</h2><div class="kv"><b>name</b><span>${esc(fleet.name || '')}</span><b>mode</b><span>${esc(fleet.mode || '')}</span><b>updated</b>${when(fleet.updated_at)}</div></div>`;
  if (!hosts.length) {
    out += `<div class="card"><h2>This host</h2><p class="muted">${state.errors.hosts ? 'Host list unavailable: ' + esc(state.errors.hosts.code + ' ' + state.errors.hosts.message) : 'Fleet of one: this host serves the dashboard. Enroll more hosts to see them here.'}</p></div>`;
  }
  for (const h of hosts) {
    const here = sb.filter((s) => s.host_id === h.id);
    const stranded = here.filter((s) => s.state === 'suspended' && !online(h));
    const cap = h.capacity || {}, res = h.reserved || {};
    const bar = (k) => cap[k] ? `<div class="row"><span class="muted" style="width:70px">${k}</span><div class="bar sp"><i style="width:${Math.min(100, 100 * (res[k] || 0) / cap[k]).toFixed(0)}%"></i></div><span>${res[k] || 0}/${cap[k]}</span></div>` : '';
    out += `<div class="card" id="hosts-${esc(h.id)}"><div class="row"><h2>${esc(h.name || h.id)}</h2>${pill(h.state)}<span class="sp"></span><span class="muted">${esc([h.os, h.arch].filter(Boolean).join(' / '))}</span></div>
      <div class="kv"><b>id</b>${short(h.id)}<b>heartbeat</b>${when(h.heartbeat_at)}<b>engines</b><span>${esc((h.engine_ids || []).join(', ') || '—')}</span><b>roles</b><span>${esc((h.roles || []).join(', ') || '—')}</span><b>sandboxes</b><span>${here.length}</span></div>
      ${bar('cpu_millis')}${bar('memory_mib')}${bar('disk_mib')}
      ${stranded.length ? `<p class="warn">Saved state stranded on this ${esc(h.state)} host: ${stranded.map((s) => short(s.id)).join(' ')}. It returns when the host wakes; it is kept until ${stranded.map((s) => when(s.saved_state_until)).join(', ')}.</p>` : ''}</div>`;
  }
  return `<div class="grid">${out}</div>`;
}

function opFor(kind, id) {
  return items('operations').filter((o) => o.resource && o.resource.id === id || (o.resource_ids || []).some((r) => r.id === id)).sort((a, b) => (b.created_at || '').localeCompare(a.created_at || ''))[0];
}
function opLine(o) {
  if (!o) return '';
  const p = o.progress;
  return `<div class="row"><span class="muted">${esc(o.operation_name || 'operation')}</span>${pill(o.state)}${o.phase ? `<span class="pill">${esc(o.phase)}</span>` : ''}${p ? `<div class="bar" style="width:120px"><i style="width:${(100 * p.completed / Math.max(1, p.total)).toFixed(0)}%"></i></div><span class="muted">${p.completed}/${p.total} ${esc(p.unit || '')}</span>` : ''}</div>
    ${o.error ? `<p class="err">${esc(o.error.code)}: ${esc(o.error.message)}</p>` : ''}`;
}

function sandboxCard(s) {
  const r = s.readiness || {}, checks = r.checks || [];
  const idle = s.idle_countdown || {};
  const task = s.task_id && byId('tasks', s.task_id);
  const host = items('hosts').find((h) => h.id === s.host_id);
  const stranded = s.state === 'suspended' && host && !online(host);
  const elig = s.suspend_eligibility;
  return `<div class="card" id="sandboxes-${esc(s.id)}"><div class="row"><h2>${esc(s.labels && s.labels.name || s.id)}</h2>${pill(s.state)}<span class="sp"></span>${s.boundary ? `<span class="pill">${esc(s.boundary)}</span>` : ''}</div>
    <div class="kv"><b>id</b>${short(s.id)}<b>host</b><span>${esc(s.host_id || 'this host')}</span><b>engine</b><span>${esc(s.engine_id || '—')} ${esc(s.guest_os || '')}</span><b>task</b><span>${s.task_id ? `<a href="#/tasks/${esc(s.task_id)}">${esc(s.task_id)}</a>` : '—'}</span><b>created</b>${when(s.created_at)}</div>
    ${checks.length ? `<h3>Setup</h3><div class="row">${checks.map((c) => `<span class="pill ${cls(c.state === 'passed' ? 'ready' : c.state)}" title="${esc(c.error ? c.error.code + ': ' + c.error.message : '')}">${esc(c.name)}: ${esc(c.state)}</span>`).join('')}</div>${checks.filter((c) => c.error).map((c) => `<p class="err">${esc(c.name)} failed — ${esc(c.error.code)}: ${esc(c.error.message)}</p>`).join('')}` : ''}
    ${r.agent_ready_at ? `<p class="ok">Setup finished; agent ready ${when(r.agent_ready_at)}</p>` : r.command_started_at ? `<p class="ok">Ready; command started ${when(r.command_started_at)}</p>` : ''}
    ${opLine(opFor('sandbox', s.id))}
    <h3>Countdowns</h3><div class="row">
      ${idle.fires_at ? countdown(idle.fires_at, `idle → ${idle.action}`) : `<span class="muted">idle ${esc(idle.action || '')} after ${esc(idle.grace_seconds || '?')} s quiet${elig && !elig.eligible ? ' — busy: ' + esc((elig.reasons || []).join(', ')) : ''}</span>`}
      ${task && task.deadline_at ? countdown(task.deadline_at, 'task budget') : ''}
      ${idle.expires_at ? countdown(idle.expires_at, 'expires') : ''}
      ${s.saved_state_until ? countdown(s.saved_state_until, 'saved state kept') : ''}</div>
    ${stranded ? `<p class="warn">Saved state is on ${esc(host.name || host.id)}, which is ${esc(host.state)}. It cannot resume until that host is back.</p>` : ''}
    <div class="row" style="margin-top:8px">${s.state === 'ready' ? btn('suspend', s.id, 'Suspend') : ''}${s.state === 'suspended' ? btn('resume', s.id, 'Resume') : ''}</div></div>`;
}

function sessionCard(s) {
  const blocked = s.status === 'blocked';
  const shares = items('shares').filter((x) => x.session_id === s.id || x.resource_id === s.id);
  return `<div class="card ${blocked ? 'decision' : ''}" id="sessions-${esc(s.id)}"><div class="row"><h2>${esc(s.labels && s.labels.name || s.id)}</h2>${pill(s.state)}${s.status ? pill(s.status) : ''}<span class="sp"></span><span class="muted">${esc(s.harness_id || '')}</span></div>
    <div class="kv"><b>id</b>${short(s.id)}<b>sandbox</b><span>${s.sandbox_id ? `<a href="#/sandboxes/${esc(s.sandbox_id)}">${esc(s.sandbox_id)}</a>` : '—'}</span><b>task</b><span>${s.task_id ? `<a href="#/tasks/${esc(s.task_id)}">${esc(s.task_id)}</a>` : '—'}</span><b>observed</b>${when(s.observed_at)}<b>location</b><span>${esc(s.location ? (s.location.host_id || '') + ' ' + (s.location.sandbox_id || '') : '—')}</span><b>shared with</b><span>${shares.length ? shares.map((x) => esc(x.subject_id) + ' (' + esc((x.actions || []).includes('session.input') ? 'watch + type' : 'watch only') + ')').join(', ') : 'nobody'}</span></div>
    ${s.status_reason ? `<p class="${blocked ? 'warn' : 'muted'}">${esc(s.status_reason)}</p>` : ''}
    ${s.state === 'active' && s.owner_id === state.me ? `<div class="row" style="margin:6px 0"><input name="subject" placeholder="share with principal id"><label><input type="checkbox" name="cantype"> can type</label>${btn('share', s.id, 'Share')}</div>` : ''}
    ${s.state === 'active' ? `<div class="row"><input name="answer" class="sp" placeholder="${blocked ? 'Answer the agent…' : 'Send input…'}"><button data-act="answer" data-id="${esc(s.id)}" class="${blocked ? 'primary' : ''}">Send</button></div>` : ''}</div>`;
}

function taskCard(t) {
  const b = t.budget_id && byId('budgets', t.budget_id);
  const pct = b && b.token_limit ? Math.min(100, 100 * ((b.used_tokens || 0) + (b.reserved_tokens || 0)) / b.token_limit) : 0;
  const children = items('tasks').filter((x) => x.parent_task_id === t.id);
  return `<div class="card" id="tasks-${esc(t.id)}"><div class="row"><h2>${esc(t.labels && t.labels.name || t.id)}</h2>${pill(t.state)}<span class="sp"></span>${t.deadline_at && t.state === 'running' ? countdown(t.deadline_at, 'time left') : ''}</div>
    <div class="kv"><b>id</b>${short(t.id)}${t.parent_task_id ? `<b>parent</b><a href="#/tasks/${esc(t.parent_task_id)}">${esc(t.parent_task_id)}</a>` : ''}<b>sandboxes</b><span>${(t.sandbox_ids || items('sandboxes').filter((s) => s.task_id === t.id).map((s) => s.id)).map((id) => `<a href="#/sandboxes/${esc(id)}">${esc(id)}</a>`).join(' ') || '—'}</span><b>sessions</b><span>${(t.session_ids || []).map((id) => `<a href="#/sessions/${esc(id)}">${esc(id)}</a>`).join(' ') || '—'}</span><b>sub-tasks</b><span>${children.length}</span><b>deadline</b>${when(t.deadline_at)}</div>
    ${b ? `<h3>Budget ${short(b.id)} ${pill(b.state)}</h3><div class="kv"><b>time</b><span>${b.wall_time_ms ? (b.wall_time_ms / 60000).toFixed(1) + ' min' : '—'} (one clock for every descendant)</span><b>tokens</b><span>${b.token_limit ? `${b.used_tokens || 0} used + ${b.reserved_tokens || 0} reserved of ${b.token_limit} (${esc(b.token_mode)})` : 'no token cap'}</span>${b.watchdog ? `<b>watchdog</b><span>${esc(JSON.stringify(b.watchdog))}</span>` : ''}</div>${b.token_limit ? `<div class="bar"><i style="width:${pct.toFixed(0)}%"></i></div>` : ''}${b.token_mode === 'best_effort' ? '<p class="muted">Opaque routes are counted best-effort; usage there may be unknown.</p>' : ''}` : ''}
    ${(t.exposures || []).length ? `<p class="warn">Credential exposures: ${t.exposures.map(esc).join(', ')}</p>` : ''}
    <div class="row" style="margin-top:8px">${t.state === 'running' || t.state === 'active' || t.state === 'blocked' ? btn('task-stop', t.id, 'Stop task', 'class="danger" data-confirm="Stop this task and every descendant?"') : ''}</div></div>`;
}

function effect(a) {
  const p = a.action_plan || {};
  const rows = [];
  if (p.operation) rows.push(['operation', `<b>${esc(p.operation)}</b>`]);
  const ref = p.resource_ref || a.resource_ref;
  if (ref) rows.push(['on', `${esc(ref.kind)} ${short(ref.id)}`]);
  const dest = p.destination || a.destination;
  if (dest) rows.push(['destination', esc(typeof dest === 'string' ? dest : JSON.stringify(dest))]);
  if (p.form_ref) rows.push(['form', esc(JSON.stringify(p.form_ref))]);
  if (p.secret_refs && p.secret_refs.length) rows.push(['secrets used', esc(p.secret_refs.join(', ')) + ' <span class="muted">(names only; values never reach this page)</span>']);
  if (p.redacted_fields && p.redacted_fields.length) rows.push(['redacted', esc(p.redacted_fields.join(', '))]);
  if (p.diff_blob_id) rows.push(['diff', short(p.diff_blob_id)]);
  if (a.sandbox_id) rows.push(['sandbox', `<a href="#/sandboxes/${esc(a.sandbox_id)}">${esc(a.sandbox_id)}</a> gen ${esc(a.generation ?? '')}`]);
  if (a.task_id) rows.push(['task', `<a href="#/tasks/${esc(a.task_id)}">${esc(a.task_id)}</a>`]);
  if (a.action_digest) rows.push(['digest', `<code>${esc(a.action_digest)}</code>`]);
  return `<div class="kv">${rows.map(([k, v]) => `<b>${k}</b><span>${v}</span>`).join('')}</div>${p.arguments ? '<h3>Exact arguments</h3>' + json(p.arguments) : ''}`;
}
function approvalCard(a) {
  const pending = a.state === 'pending';
  return `<div class="card ${pending ? 'decision' : ''}" id="approvals-${esc(a.id)}"><div class="row"><h2>${pending ? 'Needs your decision' : 'Approval'}</h2>${pill(a.state)}<span class="sp"></span>${pending && a.expires_at ? countdown(a.expires_at, 'expires') : ''}</div>
    <h3>What happens if you approve</h3>${effect(a)}
    <div class="kv"><b>id</b>${short(a.id)}<b>requested</b>${when(a.created_at)}${a.consumed_at ? `<b>consumed</b>${when(a.consumed_at)}` : ''}</div>
    ${pending ? `<div class="row" style="margin-top:8px">${btn('approve', a.id, 'Approve this exact action', 'class="primary"')}${btn('deny', a.id, 'Deny', 'class="danger"')}</div>` : ''}</div>`;
}

function browserCard(b) {
  const v = state.views[b.id];
  const human = b.state === 'human_control';
  return `<div class="card" id="browsers-${esc(b.id)}"><div class="row"><h2>Browser ${esc(b.labels && b.labels.name || '')}</h2>${pill(b.state)}<span class="sp"></span></div>
    <div class="kv"><b>id</b>${short(b.id)}<b>sandbox</b><span>${esc(b.sandbox_id || '—')}</span><b>kit</b><span>${esc(b.kit_id || '—')}</span><b>profile</b><span>${esc(b.profile_ref || 'fresh')}</span></div>
    ${b.warning ? `<p class="warn">${esc(b.warning === 'AGENT_CAN_READ_COPIED_LOGINS' ? 'The agent can read the copied logins in this browser.' : b.warning)}</p>` : ''}
    ${human ? '<p class="warn">You have private control: the agent cannot see or act in this browser until you return it.</p>' : ''}
    <div class="row">${b.state === 'agent_control' ? btn('takeover', b.id, 'Take over privately', 'class="primary"') : ''}${human ? btn('view', b.id, 'Refresh private view') + btn('return-control', b.id, 'Return to agent') : ''}</div>
    ${human ? `<div class="row" style="margin-top:6px"><input name="selector" placeholder="CSS selector"><input name="text" type="password" placeholder="private text" autocomplete="off">${btn('browser-type', b.id, 'Type privately')}</div>` : ''}
    ${v && v.png ? `<img class="view" alt="private browser view" src="data:image/png;base64,${esc(v.png)}">${v.url ? `<p class="muted">${esc(v.url)}</p>` : ''}` : v ? json(v) : ''}</div>`;
}

function notificationsView(id) {
  const sinks = items('sinks'), notes = items('notifications').slice().sort((a, b) => (b.created_at || '').localeCompare(a.created_at || ''));
  const kinds = ['task.done', 'task.stuck', 'task.needs_decision'];
  let out = `<div class="card"><h2>Add a sink</h2><p class="muted">Signed webhook to a destination enrolled on this host (destination URLs and secrets stay in the host's private config).</p>
    <div class="row"><input name="dest" placeholder="destination_ref">${kinds.map((k) => `<label><input type="checkbox" name="etype" value="${k}" checked> ${k}</label>`).join(' ')}${btn('sink-create', '', 'Create sink', 'class="primary"')}</div></div>`;
  for (const s of sinks) {
    out += `<div class="card" id="notifications-${esc(s.id)}"><div class="row"><h2>Sink ${esc(s.destination_ref)}</h2>${pill(s.state)}<span class="sp"></span><span class="pill">${esc(s.plugin_id)}</span></div>
      <div class="kv"><b>id</b>${short(s.id)}<b>events</b><span>${esc((s.event_types || []).join(', '))}</span></div>
      <div class="row" style="margin-top:6px">${btn('sink-test', s.id, 'Send test')}${btn('sink-delete', s.id, 'Delete', 'class="danger"')}</div></div>`;
  }
  out = `<div class="grid">${out}</div><h3>Deliveries</h3><div class="card"><table><tr><th>event</th><th>summary</th><th>state</th><th>attempts</th><th>next / last</th><th></th></tr>`;
  for (const n of notes) {
    const last = (n.attempts || []).slice(-1)[0];
    out += `<tr id="notifications-${esc(n.id)}" class="${n.id === id ? 'hl' : ''}"><td>${esc(n.event_type)}<br>${short(n.id)}</td><td>${esc(n.summary)}${n.action_link ? `<br><a href="${esc(n.action_link)}">open</a>` : ''}</td><td>${pill(n.state)}</td>
      <td>${esc(n.attempt_count || 0)}<details><summary class="muted">history</summary>${(n.attempts || []).map((a) => `<div>${when(a.at)} ${a.error ? `<span class="err">${esc(a.error)}</span>` : `<span class="ok">HTTP ${esc(a.status)}</span>`}</div>`).join('') || '<span class="muted">not yet attempted</span>'}</details></td>
      <td>${n.next_attempt_at ? countdown(n.next_attempt_at, 'retry') : last ? when(last.at) : ''}</td>
      <td>${n.state === 'failed' || n.state === 'pending' ? btn('note-retry', n.id, 'Retry now') : ''}${n.state !== 'acknowledged' ? btn('note-ack', n.id, 'Acknowledge') : ''}</td></tr>`;
  }
  return out + `</table>${notes.length ? '' : '<p class="muted">No notifications yet.</p>'}</div>`;
}

function auditView() {
  const recs = items('audit').slice().sort((a, b) => (b.occurred_at || b.created_at || '').localeCompare(a.occurred_at || a.created_at || '')).slice(0, 300);
  return `<div class="card"><table><tr><th>time</th><th>type</th><th>sandbox</th><th>decision</th><th>detail</th></tr>${recs.map((r) => `<tr><td>${when(r.occurred_at || r.created_at)}</td><td>${esc(r.type || r.record_type || r.kind)}</td><td>${esc(r.sandbox_id || '')}</td><td>${r.decision ? pill(r.decision) : ''}</td><td><details><summary class="muted">${esc(r.id)}</summary>${json(r)}</details></td></tr>`).join('')}</table>${recs.length ? '' : '<p class="muted">No audit records visible.</p>'}</div>`;
}

function featuresView() {
  const inst = state.installed || {};
  const rows = FEATURES.map(([name, ops]) => {
    const have = ops.filter((o) => inst[o]);
    const st = have.length === ops.length ? 'ok' : have.length ? 'warn' : 'muted';
    return `<tr><td>${esc(name)}</td><td class="${st}">${have.length === ops.length ? 'available' : have.length ? 'partial' : 'not installed on this host'}</td><td class="muted">${ops.map((o) => `<span class="${inst[o] ? '' : 'bad'}">${esc(o)}</span>`).join(' ')}</td></tr>`;
  }).join('');
  const errs = Object.entries(state.errors).map(([k, e]) => `<tr><td>${esc(k)}</td><td class="err">${esc(e.code)}</td><td>${esc(e.message)}${e.requestId ? ` <span class="muted">request ${esc(e.requestId)}</span>` : ''}</td></tr>`).join('');
  const failed = items('operations').filter((o) => o.error).slice(-50).map((o) => `<tr><td>${short(o.id)} ${esc(o.operation_name || '')}</td><td class="err">${esc(o.error.code)}</td><td>${esc(o.error.message)}${o.error.retryable ? ' <span class="muted">(retryable)</span>' : ''}</td></tr>`).join('');
  return `<div class="card"><h2>Feature availability on this host</h2><p class="muted">From the host's installed operations, not a static list.</p><table>${rows}</table></div>
    <div class="card"><h2>Operational errors</h2><table>${errs || '<tr><td class="ok">Every list loaded.</td></tr>'}${failed}</table></div>
    <div class="card"><h2>Recent events</h2><table>${state.events.slice(0, 60).map((e) => `<tr><td>${when(e.occurred_at)}</td><td>${esc(e.type)}</td><td>${esc(e.resource_kind)} ${short(e.resource_id)}</td></tr>`).join('')}</table></div>`;
}

function operationsView() {
  const ops = items('operations').slice().sort((a, b) => (b.created_at || '').localeCompare(a.created_at || '')).slice(0, 200);
  return `<div class="card"><table><tr><th>operation</th><th>state</th><th>progress</th><th>error</th><th></th></tr>${ops.map((o) => `<tr id="operations-${esc(o.id)}"><td>${esc(o.operation_name || '')}<br>${short(o.id)}</td><td>${pill(o.state)}${o.phase ? ' ' + esc(o.phase) : ''}</td><td>${o.progress ? `${o.progress.completed}/${o.progress.total} ${esc(o.progress.unit || '')}` : ''}</td><td>${o.error ? `<span class="err">${esc(o.error.code)}: ${esc(o.error.message)}</span>` : ''}${o.revocation_status ? `<br><span class="muted">revocation ${esc(JSON.stringify(o.revocation_status))}</span>` : ''}</td><td>${['accepted', 'preparing', 'running'].includes(o.state) ? btn('op-cancel', o.id, 'Cancel') : ''}</td></tr>`).join('')}</table></div>`;
}

function sharingView() {
  const shares = items('shares'), leases = items('leases');
  return `<div class="card"><h2>Shares</h2><table><tr><th>share</th><th>with</th><th>can</th><th>state</th><th>expires</th><th></th></tr>${shares.map((s) => `<tr><td>${short(s.id)}<br><span class="muted">${esc(s.session_id || s.resource_id || s.sandbox_id || '')}</span></td><td>${esc(s.subject_id || '')}</td><td>${esc((s.actions || []).join(', '))}</td><td>${pill(s.state)}</td><td>${s.expires_at ? countdown(s.expires_at, 'ends') : ''}</td><td>${s.state === 'active' ? btn('share-revoke', s.id, 'Revoke', 'class="danger"') : ''}</td></tr>`).join('')}</table>${shares.length ? '' : '<p class="muted">Nothing is shared.</p>'}</div>
    <div class="card"><h2>Input leases (who may type)</h2><table>${leases.map((l) => `<tr><td>${short(l.id)}</td><td>${esc(l.resource_id || '')}</td><td>${esc(l.holder_id || '')}</td><td>${pill(l.state)}</td><td>${l.expires_at ? countdown(l.expires_at, 'ends') : ''}</td></tr>`).join('')}</table></div>`;
}

function decisionsBanner() {
  const pending = items('approvals').filter((a) => a.state === 'pending');
  const blocked = items('sessions').filter((s) => s.status === 'blocked' && s.state === 'active');
  const stuck = items('tasks').filter((t) => t.state === 'blocked');
  if (!pending.length && !blocked.length && !stuck.length) return '';
  return `<div class="card decision"><h2>Waiting on you</h2>${pending.map((a) => `<div><a href="#/approvals/${esc(a.id)}">Approve or deny: ${esc((a.action_plan && a.action_plan.operation) || 'action')}</a></div>`).join('')}${blocked.map((s) => `<div><a href="#/sessions/${esc(s.id)}">Session ${esc(s.id)} asks: ${esc(s.status_reason || 'input needed')}</a></div>`).join('')}${stuck.map((t) => `<div><a href="#/tasks/${esc(t.id)}">Task ${esc(t.id)} is stuck</a></div>`).join('')}</div>`;
}

function overviewView() {
  const c = (k, f = () => true) => items(k).filter(f).length;
  const live = (s) => !['deleted', 'stopped', 'completed', 'ended'].includes(s.state);
  const tile = (label, n, href, sub = '') => `<a class="card" href="#/${href}" style="text-decoration:none;color:inherit"><div class="muted">${label}</div><div style="font-size:26px">${n}</div><div class="muted">${sub}</div></a>`;
  return `<div class="grid">
    ${tile('Hosts', c('hosts') || 1, 'hosts', c('hosts', (h) => h.state !== 'ready') ? c('hosts', (h) => h.state !== 'ready') + ' not ready' : 'all ready')}
    ${tile('Sandboxes', c('sandboxes', live), 'sandboxes', `${c('sandboxes', (s) => s.state === 'ready')} ready · ${c('sandboxes', (s) => s.state === 'suspended')} saved · ${c('sandboxes', (s) => s.state === 'preparing' || s.state === 'creating')} setting up`)}
    ${tile('Sessions', c('sessions', live), 'sessions', `${c('sessions', (s) => s.status === 'blocked')} waiting on you`)}
    ${tile('Tasks', c('tasks', (t) => t.state === 'running' || t.state === 'active'), 'tasks', `${c('tasks', (t) => t.state === 'blocked')} stuck`)}
    ${tile('Approvals', c('approvals', (a) => a.state === 'pending'), 'approvals', 'pending')}
    ${tile('Browsers', c('browsers', (b) => b.state !== 'closed'), 'browsers', `${c('browsers', (b) => b.state === 'human_control')} under your control`)}
    ${tile('Shares', c('shares', (s) => s.state === 'active'), 'sharing', 'active')}
    ${tile('Notifications', c('notifications', (n) => n.state === 'failed'), 'notifications', 'failed deliveries')}
    </div>
    <h3>Sandboxes</h3><div class="grid">${items('sandboxes').filter(live).map(sandboxCard).join('') || '<p class="muted">No sandboxes.</p>'}</div>
    <h3>Sessions</h3><div class="grid">${items('sessions').filter(live).map(sessionCard).join('') || '<p class="muted">No sessions.</p>'}</div>`;
}

function render() {
  const { section, id } = route();
  $('#nav').innerHTML = SECTIONS.map(([k, label]) => `<a href="#/${k}" class="${k === section ? 'on' : ''}">${label}</a>`).join('');
  $('#decide').innerHTML = decisionsBanner();
  const all = (k, card, f = () => true) => `<div class="grid">${items(k).filter(f).map(card).join('') || '<p class="muted">Nothing here.</p>'}</div>`;
  const views = {
    overview: overviewView, hosts: hostsView,
    sandboxes: () => all('sandboxes', sandboxCard), sessions: () => all('sessions', sessionCard),
    tasks: () => all('tasks', taskCard, (t) => t.state !== 'deleted'), approvals: () => all('approvals', approvalCard),
    browsers: () => all('browsers', browserCard) + `<h3>Kits</h3><div class="card"><table>${items('kits').map((k) => `<tr><td>${short(k.id)}</td><td>${esc(k.name || k.kit_ref || '')}</td><td>${pill(k.state)}</td><td>${esc(k.error ? k.error.code + ': ' + k.error.message : '')}</td></tr>`).join('')}</table></div>`,
    sharing: sharingView, operations: operationsView, notifications: () => notificationsView(id),
    audit: auditView, features: featuresView,
  };
  // Keep typed input across live re-renders.
  const typed = [...document.querySelectorAll('#app input')].map((i) => [i.closest('[id]') && i.closest('[id]').id, i.name, i.value, i.type === 'checkbox' ? i.checked : null]);
  const focus = document.activeElement && document.activeElement.closest('#app') ? [document.activeElement.closest('[id]') && document.activeElement.closest('[id]').id, document.activeElement.name] : null;
  $('#app').innerHTML = views[section]();
  for (const [card, name, value, checked] of typed) {
    const el = card ? document.querySelector(`#${CSS.escape(card)} input[name="${name}"]`) : document.querySelector(`#app input[name="${name}"]`);
    if (el) { if (checked === null) el.value = value; else if (el.value !== undefined && checked !== null && el.type === 'checkbox') { /* keep */ } }
  }
  if (focus && focus[0]) { const el = document.querySelector(`#${CSS.escape(focus[0])} input[name="${focus[1]}"]`); if (el) el.focus(); }
  if (id) {
    const el = document.getElementById(`${section}-${id}`);
    if (el && !el.classList.contains('hl')) { el.classList.add('hl'); if (!state.scrolled || state.scrolled !== location.hash) { el.scrollIntoView({ block: 'center' }); state.scrolled = location.hash; } }
    else if (!el && state.loadedAt) $('#app').insertAdjacentHTML('afterbegin', `<div class="card warn">${esc(id)} is not visible to you here (it may be gone, or outside what this key may see).</div>`);
  }
  $('#footer').textContent = state.loadedAt ? `Last observation ${state.loadedAt.toLocaleTimeString()} — shown as observed; nothing here is inferred.` : '';
  tick();
}

function tick() {
  for (const el of document.querySelectorAll('[data-deadline]')) {
    const ms = new Date(el.dataset.deadline) - Date.now();
    const s = Math.max(0, Math.round(ms / 1000));
    const fmt = s >= 86400 ? `${Math.floor(s / 86400)}d ${Math.floor(s % 86400 / 3600)}h` : s >= 3600 ? `${Math.floor(s / 3600)}h ${Math.floor(s % 3600 / 60)}m` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
    el.textContent = `${el.dataset.label} ${ms <= 0 ? 'now' : fmt}`;
    el.className = 'pill ' + (ms <= 0 ? 'bad' : ms < 60000 ? 'warn' : '');
  }
}
setInterval(tick, 1000);
// Host liveness has no event of its own (a sleeping peer cannot announce it),
// so the lists are also re-read every 10 s while signed in.
setInterval(() => { if (state.live && !document.hidden) load().catch(() => {}); }, 10000);

window.addEventListener('hashchange', () => { state.scrolled = ''; render(); });
document.addEventListener('DOMContentLoaded', () => {
  $('#signin-form').addEventListener('submit', (ev) => {
    ev.preventDefault();
    token = $('#token').value.trim(); $('#token').value = '';
    try { sessionStorage.setItem('lamp.token', token); } catch (_) {}
    signIn();
  });
  $('#signout').addEventListener('click', () => { token = ''; try { sessionStorage.removeItem('lamp.token'); } catch (_) {} signedOut(); });
  signIn();
});
