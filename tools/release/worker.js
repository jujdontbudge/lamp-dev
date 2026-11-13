// SPDX-License-Identifier: Apache-2.0
// lamp.eilmer.si: a Cloudflare Worker (free plan) in front of the public GitLab project.
//   /install.sh                  the latest release's installer, served from the package registry
//   /releases/v<version>/<file>  302 to that file in the project's generic package registry
//   ?go-get=1 (any path)         go-import meta for the Go module lamp.eilmer.si
//   anything else                a small index page
// Deploy: tools/release/deploy_worker.sh
const MODULE = 'lamp.eilmer.si';
const REPO = 'https://gitlab.com/johansardar/lamp';
const PACKAGES = 'https://gitlab.com/api/v4/projects/88830487/packages/generic/lamp';
const LATEST = '0.1.2';

function page(version) {
  return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<meta name="go-import" content="${MODULE} git ${REPO}.git">
<meta name="go-source" content="${MODULE} ${REPO} ${REPO}/-/tree/main{/dir} ${REPO}/-/blob/main{/dir}/{file}#L{line}">
<title>Lamp</title></head>
<body style="font-family:system-ui,sans-serif;max-width:40rem;margin:2rem auto;padding:0 1rem">
<h1>Lamp</h1>
<p>Developer preview: Linux virtual machines on Linux with KVM and Apple Silicon macOS 26+. Optional agents, fleets and CI require owner enrollment.</p>
<pre>curl -fsSL https://${MODULE}/install.sh | sh</pre>
<p><a href="${REPO}">Source on GitLab</a> &middot; <a href="${REPO}/-/releases/v${version}">Release ${version}</a>
&middot; <a href="/releases/v${version}/SHA256SUMS">SHA256SUMS</a>
&middot; <a href="${REPO}/-/blob/v${version}/docs/install.md#engine-setup">Install and engine setup</a></p>
</body></html>
`;
}

export default {
  async fetch(request) {
    const url = new URL(request.url);
    if (url.hostname === 'account.eilmer.si') {
      return new Response('<!doctype html><html lang="en"><meta charset="utf-8"><title>Lamp accounts</title><h1>Lamp accounts</h1><p>Hosted sign-ups are unavailable. Accounts are invite-only.</p><p><a href="https://lamp.eilmer.si">Install Lamp on your own machines</a>.</p></html>', { headers: { 'content-type': 'text/html; charset=utf-8' } });
    }
    if (url.searchParams.get('go-get') === '1' || url.pathname === '/' || url.pathname === '/index.html') {
      return new Response(page(LATEST), { headers: { 'content-type': 'text/html; charset=utf-8' } });
    }
    if (url.pathname === '/releases/latest.json') {
      return Response.json({ version: LATEST }, { headers: { 'cache-control': 'no-store' } });
    }
    if (url.pathname === '/install.sh') {
      const r = await fetch(`${PACKAGES}/${LATEST}/install.sh`);
      if (!r.ok) return new Response('installer unavailable\n', { status: 502 });
      return new Response(r.body, { headers: { 'content-type': 'text/x-shellscript; charset=utf-8', 'cache-control': 'max-age=300' } });
    }
    const m = url.pathname.match(/^\/releases\/v(\d+\.\d+\.\d+)\/([A-Za-z0-9._-]+)$/);
    if (m) return Response.redirect(`${PACKAGES}/${m[1]}/${m[2]}`, 302);
    return new Response('not found\n', { status: 404, headers: { 'content-type': 'text/plain' } });
  },
};
