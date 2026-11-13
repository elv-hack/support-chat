#!/usr/bin/env python3
"""Explicit Docker acceptance in two NEW projects; never touches existing deployment data."""
import argparse,base64,json,os,pathlib,secrets,subprocess,time,urllib.request,uuid
from ops import Ops,read_env,atomic

def request(base,path,token='',data=None,raw=False):
    headers={'Authorization':'Bearer '+token}
    if data is not None:headers['Content-Type']='application/json';data=json.dumps(data).encode()
    with urllib.request.urlopen(urllib.request.Request(base+'/api'+path,data=data,headers=headers),timeout=30) as response:
        return response.read() if raw else json.load(response)

def main():
    p=argparse.ArgumentParser();p.add_argument('bundle');p.add_argument('next_bundle');p.add_argument('--work-root',required=True);p.add_argument('--port',type=int,default=28090);a=p.parse_args()
    root=pathlib.Path(a.work_root).resolve()
    if root.exists():raise ValueError('Rehearsal directory must not exist; use a new directory')
    root.mkdir(parents=True,mode=0o700);project='chat-rehearsal-'+secrets.token_hex(4)
    primary=Ops(root/'primary');restore=Ops(root/'restore');checks=[]
    primary.init(project,a.port);restore.init(project+'-restore',a.port+1)
    try:
        with primary.lock():
            primary.deploy(a.bundle);checks.append('initial deployment readiness')
            env=read_env(primary.root/'.env');base='http://127.0.0.1:'+str(a.port)
            subprocess.run(['python3',str(pathlib.Path(__file__).with_name('smoke_test.py')),'--url',base],env={**os.environ,**env},check=True)
            checks.append('real OpenIM functional smoke suite')
            def login(url):return request(url,'/login',data={'username':env['ADMIN_USERNAME'],'password':env['ADMIN_PASSWORD'],'role':'agent'})['token']
            token=login(base);name='retained_'+secrets.token_hex(4)
            customer=request(base,'/customers',token,{'username':name,'name':'恢复验收','password':secrets.token_urlsafe(20)})
            cid=customer['id'];text='retain through deployment '+str(uuid.uuid4())
            request(base,'/messages?customer='+cid,token,{'id':str(uuid.uuid4()),'text':text,'attachmentId':''})
            image=base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Z4L8AAAAASUVORK5CYII=')
            boundary=secrets.token_hex(16)
            body=(f'--{boundary}\r\nContent-Disposition: form-data; name="kind"\r\n\r\nimage\r\n--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="test.png"\r\nContent-Type: application/octet-stream\r\n\r\n').encode()+image+f'\r\n--{boundary}--\r\n'.encode()
            with urllib.request.urlopen(urllib.request.Request(base+'/api/attachments?customer='+cid,data=body,headers={'Authorization':'Bearer '+token,'Content-Type':'multipart/form-data; boundary='+boundary}),timeout=30) as response:attachment=json.load(response)
            request(base,'/messages?customer='+cid,token,{'id':str(uuid.uuid4()),'text':'','attachmentId':attachment['id']})
            def retained(url):
                token=login(url)
                for _ in range(40):
                    rows=request(url,'/messages?customer='+cid,token)['messages']
                    if len(rows)==2:break
                    time.sleep(.5)
                assert len(rows)==2 and rows[0]['text']==text,'Chat data was not preserved'
                assert request(url,'/attachments/'+attachment['id'],token,raw=True)==image,'Attachment bytes changed'
            retained(base)
            primary.deploy(a.next_bundle);retained(base);checks.append('upgrade preserves account, OpenIM history and attachment bytes')
            primary.rollback();retained(base);checks.append('code rollback preserves current data')
            backup=primary.backup(primary.state()['current']);retained(base);checks.append('cold backup resumes service')
            # Same credentials, different bind port and project. Only new empty volumes may be used.
            cfg=read_env(primary.root/'.env');cfg['GATEWAY_PORT']=str(a.port+1);cfg['PUBLIC_HTTPS']='0'
            (restore.root/'.env').write_text(''.join(k+'='+v+'\n' for k,v in cfg.items()))
            with restore.lock():
                restore.restore(primary.state()['current'],backup);retained('http://127.0.0.1:'+str(a.port+1))
                checks.append('restore into empty project preserves account, history and attachment bytes')
        atomic(root/'result.json',{'passed':True,'checks':checks});print(json.dumps({'passed':True,'checks':checks},indent=2))
    except BaseException:
        atomic(root/'result.json',{'passed':False,'completedChecks':checks});raise
    finally:
        # Stop rehearsal containers; deliberately retain volumes/backups for diagnosis.
        for ops in [primary,restore]:
            s=ops.state();release=s['current'] or (s.get('pending') or {}).get('target')
            if release:
                try:ops.stop(release)
                except Exception:pass
if __name__=='__main__':
    os.umask(0o077);main()
