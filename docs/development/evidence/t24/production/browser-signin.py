import asyncio,base64,json,os,pathlib,re,time,urllib.request,urllib.error,sys
from playwright.async_api import async_playwright
os.umask(0o077);root=pathlib.Path.home()/'.config/lamp/hosted-t24';actors=json.loads((root/'test-accounts.json').read_text());d=json.loads((root/'deployment.json').read_text())
def mailbox(actor,path):
 req=urllib.request.Request('https://api.mail.tm'+path,headers={'Authorization':'Bearer '+actors[actor]['mail_token'],'User-Agent':'Lamp-T24/1.0'})
 with urllib.request.urlopen(req,timeout=20) as r:return json.load(r)
async def signin(page,actor,origin):
 old={m['id'] for m in await asyncio.to_thread(lambda:mailbox(actor,'/messages')['hydra:member'])}
 await page.goto(origin+'/',wait_until='domcontentloaded',timeout=30000)
 await page.get_by_role('button',name='Sign in with Eilmer',exact=True).click()
 await page.wait_for_url(d['account_origin']+'/**')
 await page.locator('input[name=email]').fill(actors[actor]['email'])
 await page.get_by_role('button',name='Send sign-in code',exact=True).click()
 await page.wait_for_load_state('domcontentloaded')
 if await page.locator('input[name=code]').count()==0:
  print(json.dumps({'actor':actor,'stage':'send-code','status_page':(await page.locator('body').inner_text())[:180]}));raise RuntimeError('sign-in code form not reached')
 deadline=time.monotonic()+150;code=None
 while time.monotonic()<deadline:
  messages=await asyncio.to_thread(lambda:mailbox(actor,'/messages')['hydra:member'])
  for m in messages:
   if m['id'] in old:continue
   msg=await asyncio.to_thread(mailbox,actor,'/messages/'+m['id'])
   match=re.search(r'\b(\d{6})\b',msg.get('subject','')+' '+msg.get('text',''))
   if match:code=match[1];break
  if code:break
  await asyncio.sleep(2)
 if code is None:raise RuntimeError('email delivery not observed; no code logged')
 try:
  await page.locator('input[name=code]').fill(code)
  await page.get_by_role('button',name='Verify Eilmer account',exact=True).click()
 except Exception:
  raise RuntimeError('Email verification action failed; no code recorded') from None
 await page.wait_for_load_state('domcontentloaded')
 if not page.url.startswith(origin+'/'):
  print(json.dumps({'actor':actor,'stage':'verify','status_page':(await page.locator('body').inner_text())[:180]}));raise RuntimeError('account callback not completed')
 return page
async def main():
 actor=sys.argv[1] if len(sys.argv)>1 else 'alice'
 async with async_playwright() as p:
  browser=await p.chromium.connect_over_cdp('http://127.0.0.1:9335');ctx=await browser.new_context(accept_downloads=True);page=await ctx.new_page()
  try:
   await signin(page,actor,d['relay_origin'])
   await ctx.storage_state(path=str(root/('browser-'+actor+'.json')))
   cookies=await ctx.cookies()
   print(json.dumps({'actor':actor,'signed_in':True,'issuer':'https://clerk.eilmer.si','subject':actors[actor]['clerk_subject'],'url':page.url,'cookies':[{'name':x['name'],'domain':x['domain'],'secure':x['secure'],'httpOnly':x['httpOnly'],'sameSite':x['sameSite']} for x in cookies],'javascript_visible_cookie_names':await page.evaluate('document.cookie.split(";").filter(x=>x.trim()).map(x=>x.split("=")[0].trim())')}))
   async with page.expect_download() as download:
    await page.get_by_role('button',name='Download private CLI account token',exact=True).click()
   download=await download.value
   await download.save_as(str(root/(actor+'-relay-token')));(root/(actor+'-relay-token')).chmod(0o600)
   print(json.dumps({'actor':actor,'opaque_relay_token_saved_privately':True,'clerk_bearer_shared_with_product_browser':False}))
  finally:
   await ctx.close();await browser.close()
if __name__=='__main__':asyncio.run(main())
