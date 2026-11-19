#!/usr/bin/env python3
"""Isolated real-OpenIM browser acceptance for dark theme and all message replacements."""
import argparse,base64,io,json,os,pathlib,secrets,shutil,time,urllib.request,urllib.error,wave
from playwright.sync_api import sync_playwright,expect

def main():
 p=argparse.ArgumentParser();p.add_argument('--url',default='http://127.0.0.1:8092');p.add_argument('--output',default='artifacts/edit-ui');a=p.parse_args();base=a.url.rstrip('/');out=pathlib.Path(a.output);out.mkdir(parents=True,exist_ok=True)
 def api(path,token='',data=None,method=None,expected=200):
  req=urllib.request.Request(base+'/api'+path,data=json.dumps(data).encode() if data is not None else None,method=method,headers={'Content-Type':'application/json','Authorization':'Bearer '+token})
  try:
   with urllib.request.urlopen(req,timeout=20) as r:status=r.status;raw=r.read()
  except urllib.error.HTTPError as e:status=e.code;raw=e.read()
  assert status==expected,(path,status,raw[:100]);return json.loads(raw) if raw else None
 name=os.getenv('ADMIN_USERNAME','admin');password=os.environ['ADMIN_PASSWORD'];admin=api('/login',data={'username':name,'password':password,'role':'agent'})['token'];suffix=secrets.token_hex(4);username='editqa_'+suffix;userpass=secrets.token_urlsafe(18)
 customer=api('/customers',admin,{'username':username,'password':userpass,'name':'编辑功能验收'},expected=201);cid=customer['id'];checks=[];errors=[]
 png=base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Z4L8AAAAASUVORK5CYII=')
 audio=io.BytesIO()
 with wave.open(audio,'wb') as w:w.setnchannels(1);w.setsampwidth(2);w.setframerate(16000);w.writeframes(b'\0\0'*16000)
 try:
  with sync_playwright() as pw:
   browser=pw.chromium.launch(executable_path=shutil.which('chromium'),headless=True,args=['--no-sandbox','--use-fake-ui-for-media-stream','--use-fake-device-for-media-stream']);ctx=browser.new_context(viewport={'width':390,'height':844},permissions=['microphone']);agent=ctx.new_page();client=ctx.new_page()
   for page in [agent,client]:page.set_default_timeout(18000);page.on('pageerror',lambda e:errors.append(str(e)))
   agent.goto(base+'/agent');expect(agent.locator('canvas.ascii-background')).to_have_count(1);assert agent.evaluate('getComputedStyle(document.documentElement).colorScheme')=='dark';agent.screenshot(path=str(out/'login-mobile.png'))
   assert agent.locator('canvas').evaluate('(c)=>c.width<=960 && c.height<=900')
   agent.emulate_media(reduced_motion='reduce');agent.evaluate('()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))');before=agent.locator('canvas').evaluate('(c)=>c.toDataURL()');agent.wait_for_timeout(650);assert agent.locator('canvas').evaluate('(c)=>c.toDataURL()')==before;agent.emulate_media(reduced_motion='no-preference');checks.append('dark default, bounded canvas, reduced-motion stops animation')
   def login(page,role,login_name,pw):
    page.goto(base+'/'+role);page.get_by_label('账号',exact=True).fill(login_name);page.get_by_label('密码',exact=True).fill(pw)
    with page.expect_response(lambda r:r.url.endswith('/api/'+role+'/login') and r.request.method=='POST') as response:page.get_by_role('button',name='登录',exact=False).first.click()
    expect(page.locator('.workspace')).to_be_visible();return response.value.json()['token']
   admin=login(agent,'agent',name,password);token=login(client,'customer',username,userpass);agent.locator('.customer-row').filter(has_text=username).click()
   assert client.get_by_text('只属于你和客服的对话').count()==0;expect(client.get_by_label('录制语音').locator('svg')).to_have_count(1)
   client.get_by_label('消息内容').fill('需要修改的客户原消息');client.get_by_role('button',name='发送 ↑',exact=True).click();expect(agent.get_by_text('需要修改的客户原消息',exact=True)).to_be_visible()
   def history():return api('/messages?customer='+cid,token)['messages']
   original=history()[0]
   agent.locator('.message-row').first.get_by_role('button',name='编辑',exact=True).click();agent.get_by_label('消息正文').fill('客服修订后的文字');agent.get_by_role('button',name='保存修改',exact=True).click();expect(client.get_by_text('客服修订后的文字',exact=True)).to_be_visible();expect(client.get_by_text('客服已编辑',exact=True)).to_be_visible();assert client.get_by_text('需要修改的客户原消息',exact=True).count()==0
   changed=history()[0];assert changed['id']==original['id'] and changed['seq']==original['seq'] and changed['sender']==original['sender'] and changed['sentAt']==original['sentAt'];checks.append('agent edits customer text in place; both sides show revision label')
   previous=None
   for kind,label,filename,mime,buffer in [('image','选择替换图片','new.png','image/png',png),('voice','上传替换语音（最长 60 秒）','new.wav','audio/wav',audio.getvalue()),('file','选择替换文件','new.txt','text/plain','替换文件内容'.encode())]:
    agent.locator('.message-row').first.get_by_role('button',name='编辑',exact=True).click();agent.get_by_label('内容类型').select_option(kind);agent.get_by_label(label,exact=True).set_input_files({'name':filename,'mimeType':mime,'buffer':buffer});expect(agent.get_by_text('替换内容已准备好',exact=False)).to_be_visible();agent.get_by_role('button',name='保存修改',exact=True).click();expect(agent.get_by_role('dialog',name='编辑聊天记录')).not_to_be_visible()
    if kind=='image':expect(client.locator('.message-row img')).to_be_visible()
    elif kind=='voice':expect(client.locator('.message-row audio')).to_be_visible();expect(client.locator('.message-row img')).to_have_count(0)
    else:expect(client.get_by_text('new.txt',exact=True)).to_be_visible();expect(client.locator('.message-row audio')).to_have_count(0)
    updated=history()[0];assert updated['kind']==kind and updated['seq']==original['seq'];assert updated['attachment']['id']!=previous
    if previous:api('/attachments/'+previous,token,expected=404)
    previous=updated['attachment']['id']
   checks.append('image/voice/file replacements synchronize and revoke previous attachment access')
   client.get_by_text('new.txt',exact=True).click();expect(client.get_by_text('替换文件内容',exact=True)).to_be_visible();client.get_by_role('button',name='关闭预览').click()
   # Exercise microphone recording inside the edit modal, without emitting an extra chat message.
   agent.locator('.message-row').first.get_by_role('button',name='编辑',exact=True).click();agent.get_by_label('内容类型').select_option('voice');agent.get_by_role('button',name='录制替换语音').click();expect(agent.get_by_role('button',name='结束录制')).to_be_visible();agent.wait_for_timeout(1100);agent.get_by_role('button',name='结束录制').click();expect(agent.get_by_text('替换内容已准备好',exact=False)).to_be_visible();agent.screenshot(path=str(out/'edit-voice-mobile.png'));agent.get_by_role('button',name='保存修改',exact=True).click();expect(client.locator('.message-row audio')).to_be_visible();assert len(history())==1;api('/attachments/'+previous,token,expected=404);checks.append('replacement microphone recording is saved as an edit, not a second message')
   agent.get_by_label('消息内容').fill('客服发送的消息也可以编辑');agent.get_by_role('button',name='发送 ↑',exact=True).click();expect(client.get_by_text('客服发送的消息也可以编辑',exact=True)).to_be_visible();agent.locator('.message-row').last.get_by_role('button',name='编辑',exact=True).click();agent.get_by_label('消息正文').fill('客服已更新服务说明');agent.get_by_role('button',name='保存修改',exact=True).click();expect(client.get_by_text('客服已更新服务说明',exact=True)).to_be_visible()
   assert client.get_by_role('button',name='编辑',exact=True).count()==0;assert client.get_by_role('button',name='删除',exact=True).count()==0;checks.append('agent can edit either sender; customer has no edit/delete controls')
   client.screenshot(path=str(out/'customer-mobile.png'));agent.screenshot(path=str(out/'agent-mobile.png'));agent.set_viewport_size({'width':1280,'height':900});agent.screenshot(path=str(out/'agent-desktop.png'))
   client.reload();expect(client.get_by_label('账号',exact=True)).to_be_visible();token=login(client,'customer',username,userpass);expect(client.get_by_text('客服已更新服务说明',exact=True)).to_be_visible();expect(client.locator('.message-row audio')).to_be_visible();checks.append('re-login refetches revised text and media from server')
   agent.locator('.message-row').first.get_by_role('button',name='删除',exact=True).click();agent.get_by_role('button',name='确认删除',exact=True).click();expect(client.locator('.message-row')).to_have_count(1);agent.get_by_role('button',name='清空聊天',exact=True).click();agent.get_by_role('button',name='确认删除',exact=True).click();expect(client.locator('.message-row')).to_have_count(0);checks.append('single deletion and clear remove edited messages on both sides')
   assert client.evaluate('localStorage.length===0 && sessionStorage.length===0');assert not ctx.cookies();assert not errors,errors;checks.append('no browser storage/cookies or uncaught errors');browser.close()
  (out/'result.json').write_text(json.dumps({'passed':len(checks),'checks':checks},ensure_ascii=False,indent=2)+'\n');print(json.dumps({'passed':len(checks),'checks':checks},ensure_ascii=False,indent=2))
 finally:
  admin=api('/login',data={'username':name,'password':password,'role':'agent'})['token'];api('/customers/'+cid+'/disable',admin,method='POST')
if __name__=='__main__':main()
