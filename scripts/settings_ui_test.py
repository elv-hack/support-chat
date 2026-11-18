#!/usr/bin/env python3
"""Real OpenIM/browser acceptance for branding, permission management and page refresh.
Run against an isolated gateway copy with ADMIN_PASSWORD, using uv run --with playwright.
"""
import argparse,json,os,pathlib,secrets,shutil,urllib.request
from playwright.sync_api import sync_playwright,expect

def main():
 p=argparse.ArgumentParser();p.add_argument('--url',default='http://127.0.0.1:8092');p.add_argument('--output',default='artifacts/settings-ui');a=p.parse_args();base=a.url.rstrip('/');out=pathlib.Path(a.output);out.mkdir(parents=True,exist_ok=True)
 def api(path,token='',data=None,method=None):
  r=urllib.request.Request(base+'/api'+path,data=json.dumps(data).encode() if data is not None else None,method=method,headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'})
  with urllib.request.urlopen(r,timeout=30) as response:return json.load(response)
 name=os.getenv('ADMIN_USERNAME','admin');password=os.environ['ADMIN_PASSWORD']
 def admin_login():return api('/login',data={'username':name,'password':password,'role':'agent'})['token']
 admin=admin_login();original=api('/admin/settings',admin);suffix=secrets.token_hex(4);username='settingsqa_'+suffix;customer_password=secrets.token_urlsafe(20)
 customer=api('/customers',admin,{'username':username,'name':'权限验收','password':customer_password});checks=[];errors=[]
 try:
  with sync_playwright() as pw:
   browser=pw.chromium.launch(executable_path=shutil.which('chromium'),headless=True,args=['--no-sandbox']);ctx=browser.new_context(viewport={'width':390,'height':844});agent=ctx.new_page();client=ctx.new_page()
   for page in [agent,client]:page.set_default_timeout(20000);page.on('pageerror',lambda e:errors.append(str(e)))
   def login(page,role,login_name,login_password):
    page.goto(base+'/'+role);page.get_by_label('账号',exact=True).fill(login_name);page.get_by_label('密码',exact=True).fill(login_password);page.get_by_role('button',name='登录',exact=False).first.click();expect(page.locator('.workspace')).to_be_visible()
   login(agent,'agent',name,password);login(client,'customer',username,customer_password)
   agent.get_by_role('button',name='应用设置').click();agent.get_by_label('应用内名称',exact=True).fill('验收服务站');agent.get_by_label('客服显示名称').fill('验收顾问');agent.get_by_label('客户公告').fill('这是配置功能验收公告');agent.get_by_role('button',name='发布显示设置').click();expect(agent.get_by_text('显示设置已发布，客户页面会自动读取')).to_be_visible()
   expect(client.get_by_text('这是配置功能验收公告',exact=True)).to_be_visible(timeout=20000);expect(client.get_by_role('heading',name='验收顾问')).to_be_visible();checks.append('agent display edits reach logged-in customer without reinstall')
   agent.screenshot(path=str(out/'display-mobile.png'));agent.get_by_role('tab',name='连接与更新').click();expect(agent.get_by_text('独立固定入口',exact=True)).to_be_visible()
   agent.get_by_label('新的业务地址').fill(base);agent.get_by_label('我已完成数据迁移并核对新服务').check();agent.get_by_label('客服密码确认').fill(password);agent.get_by_role('button',name='验证新服务并发布地址').click();expect(agent.get_by_text('业务地址已发布。App 下次打开或检查更新时会验证新服务并重新登录。')).to_be_visible();agent.screenshot(path=str(out/'connection-mobile.png'));checks.append('agent publishes validated business route through independent directory')
   agent.get_by_role('button',name='关闭应用设置').click();agent.locator('.customer-row').filter(has_text=username).click();agent.get_by_role('button',name='账号管理',exact=True).click()
   for kind in ['文字','图片','语音','文件']:agent.get_by_label('允许发送'+kind).uncheck()
   agent.get_by_label('客户称呼',exact=True).fill('只读客户');agent.screenshot(path=str(out/'permissions-mobile.png'));agent.get_by_role('button',name='保存称呼与权限').click()
   expect(client.get_by_text('当前为只读权限，可查看消息和客服回复。')).to_be_visible();expect(client.get_by_label('消息内容')).to_be_disabled();expect(client.get_by_label('发送图片或文件')).to_be_disabled();expect(client.get_by_label('录制语音')).to_be_disabled();checks.append('all four permissions become read-only in active customer session')
   agent.get_by_label('消息内容').fill('只读账号仍可收到客服消息');agent.get_by_role('button',name='发送 ↑',exact=True).click();expect(client.get_by_text('只读账号仍可收到客服消息',exact=True)).to_be_visible();checks.append('read-only customer still receives agent messages through OpenIM')
   client.get_by_role('button',name='更新',exact=True).click();client.get_by_role('button',name='更新并重新登录').click();expect(client.get_by_label('账号',exact=True)).to_be_visible();expect(client.get_by_label('密码',exact=True)).to_have_value('');checks.append('page refresh drops login and reloads updated branding')
   assert client.evaluate('localStorage.length===0 && sessionStorage.length===0');assert not ctx.cookies();assert not errors,errors;checks.append('no stored credentials/messages or uncaught page errors')
   agent.get_by_role('button',name='返回会话列表',exact=True).click();agent.get_by_role('button',name='应用设置').click();agent.set_viewport_size({'width':1280,'height':900});agent.screenshot(path=str(out/'display-desktop.png'));browser.close()
  (out/'result.json').write_text(json.dumps({'passed':len(checks),'checks':checks},ensure_ascii=False,indent=2)+'\n');print(json.dumps({'passed':len(checks),'checks':checks},ensure_ascii=False,indent=2))
 finally:
  admin=admin_login();latest=api('/admin/settings',admin);original['revision']=latest['revision'];api('/admin/settings',admin,original,method='PUT');api('/customers/'+customer['id']+'/disable',admin,method='POST')
if __name__=='__main__':main()
