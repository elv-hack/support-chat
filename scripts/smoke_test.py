#!/usr/bin/env python3
"""Run against a real OpenIM-backed gateway. Creates isolated smoke_* accounts; disables them on success."""
import argparse,base64,io,json,os,secrets,time,urllib.request,urllib.error,uuid,wave
p=argparse.ArgumentParser();p.add_argument('--url',default='http://127.0.0.1:8090');args=p.parse_args();base=args.url
checks=[]
def request(path,token='',method='GET',data=None,expected=200,raw=False):
    headers={}
    if token:headers['Authorization']='Bearer '+token
    if isinstance(data,dict):data=json.dumps(data).encode();headers['Content-Type']='application/json'
    req=urllib.request.Request(base+'/api'+path,data=data,headers=headers,method=method)
    try:
        res=urllib.request.urlopen(req,timeout=30);status=res.status;payload=res.read();cache=res.headers.get('Cache-Control')
    except urllib.error.HTTPError as e:status=e.code;payload=e.read();cache=e.headers.get('Cache-Control')
    assert status==expected,(method,path,status,payload[:300]);assert cache=='no-store',(path,cache)
    return payload if raw else json.loads(payload)
def login(name,password,role):return request('/login',method='POST',data={'username':name,'password':password,'role':role})['token']
def upload(t,c,data,kind,name):
    boundary=secrets.token_hex(16);b=(f'--{boundary}\r\nContent-Disposition: form-data; name="kind"\r\n\r\n{kind}\r\n--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name}"\r\nContent-Type: application/octet-stream\r\n\r\n').encode()+data+f'\r\n--{boundary}--\r\n'.encode()
    req=urllib.request.Request(base+'/api/attachments?customer='+c,data=b,headers={'Authorization':'Bearer '+t,'Content-Type':'multipart/form-data; boundary='+boundary})
    try:return urllib.request.urlopen(req,timeout=30)
    except urllib.error.HTTPError as e:return e

def send(t,c,**kw):
    payload={'id':str(uuid.uuid4()),'text':'','attachmentId':'',**kw};request('/messages?customer='+c,t,'POST',payload,201);return payload

def history(t,c):return request('/messages?customer='+c,t)['messages']
def wait_messages(t,c,n):
    until=time.monotonic()+20
    while time.monotonic()<until:
        found=history(t,c)
        if len(found)==n:return found
        time.sleep(.4)
    raise AssertionError(f'Expected {n} messages, got {found}')

def wav(seconds):
    out=io.BytesIO()
    with wave.open(out,'wb') as w:w.setnchannels(1);w.setsampwidth(2);w.setframerate(16000);w.writeframes(b'\0\0'*16000*seconds)
    return out.getvalue()
admin=login(os.getenv('ADMIN_USERNAME','admin'),os.environ['ADMIN_PASSWORD'],'agent')
password=secrets.token_urlsafe(16);suffix=secrets.token_hex(4);clients=[]
for i in range(2):
    name=f'smoke_{suffix}_{i}';c=request('/customers',admin,'POST',{'username':name,'name':f'验收客户 {i+1}','password':password},201);clients.append((c,login(name,password,'customer')))
a,at=clients[0];b,bt=clients[1]
checks.append('账号创建和双方登录')
request('/customers',at,expected=403);request('/messages?customer='+b['id'],at,expected=403)
checks.append('客户不能列出账号或读取另一客户会话')
payload=send(at,a['id'],text='你好，这是 OpenIM 真实收发验收。')
request('/messages?customer='+a['id'],at,'POST',payload)
rows=wait_messages(admin,a['id'],1);assert rows[0]['text']==payload['text'];assert rows[0]['sender']==a['id']
request('/messages?customer='+b['id'],bt,'POST',payload,409)
checks.append('OpenIM 历史拉取、重试幂等和跨账号 ID 防重放')
send(admin,a['id'],text='已收到，我是客服。');rows=wait_messages(at,a['id'],2);assert rows[-1]['sender']=='support_agent'
assert next(c for c in request('/customers',admin) if c['id']==a['id'])['unread']==1
checks.append('客服回复由 OpenIM 投递')
image=base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Z4L8AAAAASUVORK5CYII=')
res=upload(at,a['id'],image,'image','tiny.png');assert res.status==201;att=json.load(res);send(at,a['id'],attachmentId=att['id']);rows=wait_messages(admin,a['id'],3)
request('/attachments/'+att['id'],bt,expected=404);assert request('/attachments/'+att['id'],admin,raw=True)==image
res=upload(at,a['id'],b'private text file','file','note.txt');assert res.status==201;fileatt=json.load(res);send(at,a['id'],attachmentId=fileatt['id']);assert request('/attachments/'+fileatt['id'],admin)['text']=='private text file'
res=upload(at,a['id'],wav(1),'voice','voice.wav');assert res.status==201,(res.status,res.read());voice=json.load(res);assert .99<=voice['duration']<=1.01;send(at,a['id'],attachmentId=voice['id']);assert request('/attachments/'+voice['id'],admin,raw=True)[:4]==b'RIFF'
res=upload(at,a['id'],wav(61),'voice','too-long.wav');assert res.status==400,(res.status,res.read())
res=upload(at,a['id'],b'<svg onload="alert(1)"/>','image','unsafe.svg');assert res.status==400
checks.append('私有图片、文件、语音；拒绝 61 秒音频和伪造图片')
rows=wait_messages(admin,a['id'],5)
assert next(c for c in request('/customers',admin) if c['id']==a['id'])['unread']==4
request('/read?customer='+a['id'],admin,'POST',{'seq':rows[-1]['seq']})
assert next(c for c in request('/customers',admin) if c['id']==a['id'])['unread']==0
image_seq=next(m['seq'] for m in rows if m.get('attachment',{}).get('id')==att['id'])
request('/messages?customer='+a['id'],at,'DELETE',{'seq':image_seq},403)
request('/messages?customer='+a['id'],admin,'DELETE',{'seq':image_seq});assert all(m['seq']!=image_seq for m in history(at,a['id']));request('/attachments/'+att['id'],at,expected=404)
checks.append('客服单条删除立即同步且旧附件读取失效')
request('/messages?customer='+a['id'],admin,'DELETE',{'all':True});wait_messages(at,a['id'],0);request('/attachments/'+voice['id'],at,expected=404)
checks.append('清空记录及附件访问撤销')
request('/customers/'+a['id']+'/kick',admin,'POST');request('/me',at,expected=401);at=login(a['username'],password,'customer')
newpassword=secrets.token_urlsafe(16);request('/customers/'+a['id']+'/password',admin,'POST',{'password':newpassword});request('/me',at,expected=401);request('/login',method='POST',data={'username':a['username'],'password':password,'role':'customer'},expected=401);at=login(a['username'],newpassword,'customer')
request('/customers/'+a['id']+'/disable',admin,'POST');request('/me',at,expected=401);request('/login',method='POST',data={'username':a['username'],'password':newpassword,'role':'customer'},expected=401)
request('/customers/'+b['id']+'/disable',admin,'POST')
checks.append('强制下线、改密、停用使旧会话失效')
print(json.dumps({'passed':len(checks),'checks':checks,'testCustomers':[a['id'],b['id']]},ensure_ascii=False,indent=2))
