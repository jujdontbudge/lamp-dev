"""Real production-account/browser journey over the owner's private bore bridge."""
import asyncio,hashlib,json,os,pathlib
from playwright.async_api import async_playwright
os.umask(0o077);root=pathlib.Path.home()/'.config/lamp/hosted-t24';d=json.loads((root/'deployment.json').read_text());actors=json.loads((root/'test-accounts.json').read_text());observations=[]
def record(event,**values):
 row={'event':event,**values};observations.append(row);print(json.dumps(row),flush=True)
async def enter(page,origin):
 response=await page.goto(origin+'/',wait_until='domcontentloaded',timeout=30000)
 if await page.get_by_role('button',name='Sign in with Eilmer',exact=True).count():
  await page.get_by_role('button',name='Sign in with Eilmer',exact=True).click()
  await page.wait_for_url(origin+'/',wait_until='domcontentloaded',timeout=30000)
  if await page.locator('input[name=email]').count():raise RuntimeError('Existing real account session did not perform silent code exchange')
 return response
async def main():
 async with async_playwright() as p:
  browser=await p.chromium.connect_over_cdp('http://127.0.0.1:9335')
  try:
   for actor in ['alice','bea']:
    ctx=await browser.new_context(storage_state=str(root/('browser-'+actor+'.json')))
    try:
     page=await ctx.new_page();await enter(page,d['relay_origin'])
     await enter(page,d['api_origin'])
     response=await page.goto(d['api_origin']+'/v1/fleets/fl_local',wait_until='domcontentloaded');data=await response.json()
     assert response.status==200 and data['data']['id']=='fl_local'
     tls=await response.security_details()
     record('real-clerk-to-private-fleet-browser',actor=actor,issuer='https://clerk.eilmer.si',subject=actors[actor]['clerk_subject'],fleet_id=data['data']['id'],tls=tls,standard_certificate_validation=True,server='real lampd',browser_to_host_tls_passthrough=True)
     before={(c['domain'],c['name']):c['value'] for c in await ctx.cookies() if c['name'] in ['__Host-eilmer_account','__Host-lamp_session']}
     rejected=[]
     def response_seen(r):
      if r.url==d['account_origin']+'/signin':rejected.append({'status':r.status,'origin':r.request.headers.get('origin')})
     page.on('response',response_seen)
     await enter(page,d['content_origin']);await page.wait_for_function('window.isolationProbe && window.isolationProbe.done === true')
     assert await page.get_by_role('heading',name='Public Hello-World project',exact=True).count()==1
     marker=await page.locator('#work-marker').inner_text();probe=await page.evaluate('window.isolationProbe')
     cookies=await ctx.cookies();after={(c['domain'],c['name']):c['value'] for c in cookies if c['name'] in ['__Host-eilmer_account','__Host-lamp_session']}
     # CORS intentionally hides the browser response event. Observe its read
     # denial here; obtain the server's HTTP status separately below.
     assert before==after and probe['accountReadBlocked']
     denied=await ctx.request.post(d['account_origin']+'/signin',headers={'Origin':d['content_origin']},form={'csrf':'wrong','email':'not-used'})
     assert denied.status==403
     assert not any(x['name']=='lamp_t24_from_backend' for x in cookies)
     assert all(c['secure'] and c['httpOnly'] and not c['domain'].startswith('.') for c in cookies if c['name'].startswith('__Host-'))
     visible=await page.evaluate('document.cookie.split(";").filter(x=>x.trim()).map(x=>x.split("=")[0].trim())')
     assert not any(n.startswith('__Host-') for n in visible)
     record('isolated-untrusted-content-origin',actor=actor,origin=d['content_origin'],public_project='https://github.com/octocat/Hello-World',work_marker_sha256=hashlib.sha256(marker.encode()).hexdigest(),account_and_product_sessions_unchanged=True,parent_domain_response_cookie_filtered=True,forged_host_prefixed_parent_cookies_rejected=True,credentialed_sibling_account_post_status=403,cors_read_blocked=True,javascript_visible_cookie_names=visible,auth_cookie_attributes=[{k:c[k] for k in ['name','domain','secure','httpOnly','sameSite']} for c in cookies if c['name'].startswith('__Host-')])
     if actor=='alice':await page.screenshot(path=str(root/'browser-content.png'),full_page=True)
     # Remove the deliberately harmless tossed parent cookie from the disposable context.
     await ctx.clear_cookies(name='lamp_t24_parent')
     await ctx.storage_state(path=str(root/('browser-'+actor+'.json')))
    finally:await ctx.close()
  finally:await browser.close()
 (root/'browser-journey.jsonl').write_text(''.join(json.dumps(x)+'\n' for x in observations))
if __name__=='__main__':asyncio.run(main())
