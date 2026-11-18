#!/usr/bin/env python3
"""Opt-in real gateway/browser acceptance. Requires ADMIN_PASSWORD and Playwright + Chromium.
Creates a disposable customer and templates, removes templates and disables the customer on exit.
Run with: uv run --with playwright python scripts/library_ui_test.py --url http://127.0.0.1:8092
"""
import argparse,json,os,pathlib,secrets,shutil,struct,time,urllib.request,zlib
from playwright.sync_api import sync_playwright,expect

def main():
 p=argparse.ArgumentParser();p.add_argument('--url',default='http://127.0.0.1:8090');p.add_argument('--output',default='artifacts/library-ui');a=p.parse_args()
 base=a.url.rstrip('/');out=pathlib.Path(a.output);out.mkdir(parents=True,exist_ok=True)
 def api(path,token='',data=None,method=None):
  req=urllib.request.Request(base+'/api'+path,data=json.dumps(data).encode() if data is not None else None,method=method,headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
  with urllib.request.urlopen(req,timeout=30) as r:return json.load(r)
 def login(name,password,role):return api('/login',data={'username':name,'password':password,'role':role})['token']
 admin_name=os.getenv('ADMIN_USERNAME','admin');password=os.environ['ADMIN_PASSWORD'];admin=login(admin_name,password,'agent')
 suffix=secrets.token_hex(4);username='libraryqa_'+suffix;customer_password=secrets.token_urlsafe(20)
 customer=api('/customers',admin,{'username':username,'name':'图文功能验收','password':customer_password});cid=customer['id'];token=login(username,customer_password,'customer')
 reply_title='验收·服务时间 '+suffix;intro_title='验收·服务方案 '+suffix;checks=[];errors=[]
 # Deterministic PNG fixture: service overview graphic, generated without external resources.
 w,h=480,200;pixels=bytearray()
 for y in range(h):
  pixels.append(0)
  for x in range(w):
   color=(237,243,226)
   if 20<x<460 and 18<y<52:color=(50,83,60)
   for left in [20,174,328]:
    if left<x<left+132 and 72<y<178:color=(255,255,255)
    if left+12<x<left+110 and 90<y<101:color=(113,143,89)
    if left+12<x<left+85 and 121<y<128:color=(194,207,179)
    if left+12<x<left+105 and 143<y<150:color=(194,207,179)
   pixels.extend(color)
 def chunk(kind,data):return struct.pack('>I',len(data))+kind+data+struct.pack('>I',zlib.crc32(kind+data)&0xffffffff)
 png=b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('>IIBBBBB',w,h,8,2,0,0,0))+chunk(b'IDAT',zlib.compress(bytes(pixels)))+chunk(b'IEND',b'')
 def history(n):
  for _ in range(50):
   rows=api('/messages?customer='+cid,token)['messages']
   if len(rows)==n:return rows
   time.sleep(.3)
  raise AssertionError(f'Expected {n} messages, got {len(rows)}')
 try:
  with sync_playwright() as playwright:
   browser=playwright.chromium.launch(executable_path=os.getenv('CHROMIUM_PATH') or shutil.which('chromium'),headless=True,args=['--no-sandbox'])
   context=browser.new_context(viewport={'width':390,'height':844});page=context.new_page();page.set_default_timeout(15000)
   page.on('pageerror',lambda e:errors.append(str(e)))
   def login_ui():
    page.goto(base+'/agent');page.get_by_label('账号',exact=True).fill(admin_name);page.get_by_label('密码',exact=True).fill(password);page.get_by_role('button',name='登录',exact=False).first.click();expect(page.locator('.sidebar')).to_be_visible()
   login_ui();page.get_by_role('button',name='回复与业务资料').click()
   page.get_by_role('button',name='＋ 新建回复').click();page.get_by_label('标题',exact=True).fill(reply_title);page.get_by_label('分类（可选）').fill('售前/常见问题');page.get_by_label('快捷回复正文').fill('您好，服务时间为工作日 9:00–18:00。您可以先描述需求。');page.get_by_role('button',name='保存模板').click()
   expect(page.get_by_text('模板已保存',exact=True)).to_be_visible();expect(page.locator('.library-card').filter(has_text=reply_title).get_by_role('button',name='一键发送')).to_be_disabled();checks.append('no selected customer: manage templates without sending')
   page.get_by_role('button',name='关闭资料库',exact=True).click();page.locator('.customer-row').filter(has_text=username).click();page.get_by_role('button',name='☷ 快捷回复',exact=True).click()
   page.locator('.library-card').filter(has_text=reply_title).get_by_role('button',name='一键发送',exact=True).click();expect(page.get_by_text('已向 图文功能验收 发送 1 条消息',exact=True)).to_be_visible();assert history(1)[0]['text'].startswith('您好');checks.append('one-click reply reaches real OpenIM customer history')
   page.get_by_role('tab',name='业务介绍',exact=True).click();page.get_by_role('button',name='＋ 新建介绍').click();page.get_by_label('标题',exact=True).fill(intro_title);page.get_by_label('分类（可选）').fill('企业服务/标准方案')
   page.get_by_label('章节标题',exact=True).fill('方案特点');page.get_by_label('章节 1 文字 1').fill('先了解你的业务，再制定合适的服务方案。')
   page.get_by_label('添加业务图片').set_input_files({'name':'overview.png','mimeType':'image/png','buffer':png});expect(page.locator('.library-block img')).to_be_visible()
   page.get_by_role('button',name='＋ 添加章节').click();page.get_by_label('章节标题',exact=True).nth(1).fill('办理流程');page.get_by_label('章节 2 文字 1').fill('确认需求 → 提交资料 → 专属服务。')
   page.get_by_role('button',name='上移章节 2',exact=True).click();expect(page.get_by_label('章节标题',exact=True).first).to_have_value('办理流程');page.get_by_role('button',name='下移章节 1',exact=True).click()
   page.get_by_role('button',name='保存模板').click();expect(page.get_by_text('模板已保存',exact=True)).to_be_visible();checks.append('multi-section text/image template edits and reordering persist')
   page.locator('.library-card').filter(has_text=intro_title).get_by_role('button',name='选择章节并发送').click();page.get_by_label('发送本章').nth(1).uncheck();page.get_by_label('章节 1 文字 1').fill('这是为您调整的本次介绍，原模板保持不变。')
   page.locator('.library-body').evaluate('(el)=>el.scrollTop=0');page.screenshot(path=str(out/'intro-mobile.png'))
   page.get_by_role('button',name='发送给 图文功能验收',exact=True).click();expect(page.get_by_text('已向 图文功能验收 发送 3 条消息',exact=True)).to_be_visible();rows=history(4)
   assert [x['kind'] for x in rows]==['text','text','text','image'];assert rows[1]['text']=='方案特点' and rows[2]['text'].startswith('这是为您调整');assert not any(x.get('text')=='办理流程' for x in rows)
   with urllib.request.urlopen(urllib.request.Request(base+'/api/attachments/'+rows[3]['attachment']['id'],headers={'Authorization':'Bearer '+token}),timeout=10) as r:assert r.read()==png
   checks.append('selected chapter only, edited send snapshot and private image bytes delivered')
   page.locator('.library-card').filter(has_text=intro_title).get_by_role('button',name='编辑模板',exact=True).click();expect(page.get_by_label('章节 1 文字 1')).to_have_value('先了解你的业务，再制定合适的服务方案。');page.get_by_role('button',name='返回列表',exact=True).click()
   page.get_by_role('button',name='关闭资料库',exact=True).click();page.get_by_role('button',name='返回会话列表',exact=True).click();page.get_by_role('button',name='退出登录',exact=True).click();login_ui()
   page.get_by_role('button',name='回复与业务资料').click();page.get_by_role('tab',name='业务介绍').click();page.get_by_label('搜索资料').fill(intro_title);expect(page.locator('.library-card')).to_have_count(1);page.get_by_label('筛选资料分类').select_option('企业服务');expect(page.locator('.library-card')).to_have_count(1)
   checks.append('re-login refetches saved templates; hierarchical category/search work')
   page.get_by_role('button',name='复制',exact=True).click();expect(page.get_by_label('标题',exact=True)).to_have_value(intro_title+' 副本');page.get_by_label('标题',exact=True).fill('未保存的修改');page.get_by_role('button',name='返回列表',exact=True).click();expect(page.get_by_text('放弃尚未保存的修改？',exact=True)).to_be_visible();page.get_by_role('button',name='放弃修改',exact=True).click();checks.append('copy and unsaved-edit discard confirmation')
   page.get_by_label('搜索资料').fill('');page.screenshot(path=str(out/'library-mobile.png'));page.set_viewport_size({'width':1280,'height':900});page.screenshot(path=str(out/'library-desktop.png'))
   assert page.evaluate('localStorage.length===0 && sessionStorage.length===0');assert not context.cookies();assert not errors,errors;checks.append('no browser storage/cookies; no uncaught UI errors')
   browser.close()
  (out/'result.json').write_text(json.dumps({'passed':len(checks),'checks':checks},ensure_ascii=False,indent=2)+'\n');print(json.dumps({'passed':len(checks),'checks':checks},ensure_ascii=False,indent=2))
 finally:
  admin=login(admin_name,password,'agent')
  for entry in api('/library/entries',admin):
   if suffix in entry['title']:api('/library/entries/'+entry['id']+'?version='+str(entry['version']),admin,method='DELETE')
  api('/customers/'+cid+'/disable',admin,method='POST')
if __name__=='__main__':main()
