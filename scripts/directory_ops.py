#!/usr/bin/env python3
"""Independent HTTPS directory lifecycle; credentials never enter APKs or public JSON."""
import argparse,fcntl,json,os,pathlib,re,secrets,shutil,subprocess,time,urllib.request
from release import verify
from ops import read_env

def init(root,bundle,domain):
    if not re.fullmatch(r'[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?',domain) or '.' not in domain:raise ValueError('Use a DNS hostname without scheme, port or path')
    manifest=verify(bundle,'bundle')
    if root.exists() and any(root.iterdir()):raise ValueError('Directory runtime must be empty; never overwrite its token or state')
    root.mkdir(parents=True,exist_ok=True);root.chmod(0o700)
    for source,target in [('directory.compose.json','compose.json'),('Directory.Caddyfile','Caddyfile')]:shutil.copy2(bundle/source,root/target)
    env={'DIRECTORY_DOMAIN':domain,'DIRECTORY_TOKEN':secrets.token_hex(32),'GATEWAY_IMAGE':manifest['images']['gateway'],'CADDY_IMAGE':manifest['images']['caddy'],'COMPOSE_PROJECT_NAME':'support-chat-directory'}
    (root/'.env').write_text(''.join(k+'='+v+'\n' for k,v in env.items()));(root/'.env').chmod(0o600)
    (root/'release.json').write_text(json.dumps({'bundle':str(bundle),'version':manifest['version']},indent=2)+'\n')
    print(f'Initialized {root}. Keep .env private; copy DIRECTORY_TOKEN into the business runtime and set DIRECTORY_URL=https://{domain}.')

def operate(root,action):
    env={**os.environ,**read_env(root/'.env')}
    cmd=['docker','compose','--env-file',str(root/'.env'),'-f',str(root/'compose.json')]
    def run(args):subprocess.run([str(a) for a in args],env=env,check=True)
    if action=='up':
        bundle=pathlib.Path(json.loads((root/'release.json').read_text())['bundle']);manifest=verify(bundle,'bundle')
        if env['GATEWAY_IMAGE']!=manifest['images']['gateway'] or env['CADDY_IMAGE']!=manifest['images']['caddy']:raise ValueError('Immutable image selection differs from verified bundle')
        run(['docker','load','-i',bundle/'images.tar']);run(cmd+['config','--quiet']);run(cmd+['up','-d','--pull','never','--wait','--wait-timeout','120'])
        with urllib.request.urlopen('https://'+env['DIRECTORY_DOMAIN']+'/health',timeout=15) as response:
            if json.load(response).get('ok') is not True:raise ValueError('Directory HTTPS health check failed')
    elif action=='status':run(cmd+['ps'])
    elif action=='backup':
        out=root/'backups'/(time.strftime('%Y%m%dT%H%M%SZ',time.gmtime())+'-'+secrets.token_hex(3));out.mkdir(parents=True,mode=0o700)
        # Atomic rename in the service makes this single-file read a coherent snapshot even during publication.
        with open(out/'route.json','xb') as file:
            subprocess.run(cmd+['exec','-T','directory','cat','/data/route.json'],env=env,stdout=file,check=True)
        data=json.loads((out/'route.json').read_text())
        if data.get('revision',0)<1:raise ValueError('No published route to back up')
        shutil.copy2(root/'.env',out/'.env');shutil.copy2(root/'release.json',out/'release.json');print(f'Private directory backup: {out}')

def main():
    os.umask(0o077)
    p=argparse.ArgumentParser();p.add_argument('action',choices=['init','up','status','backup']);p.add_argument('--root',default='/srv/support-chat-directory');p.add_argument('--bundle');p.add_argument('--domain');a=p.parse_args();root=pathlib.Path(a.root).resolve()
    if a.action=='init':
        if not a.bundle or not a.domain:raise ValueError('init requires --bundle and --domain')
        init(root,pathlib.Path(a.bundle).resolve(),a.domain);return
    with open(root/'operation.lock','a') as lock:
        fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB);operate(root,a.action)
if __name__=='__main__':
    try:main()
    except (OSError,ValueError,subprocess.CalledProcessError) as error:raise SystemExit(f'Directory operation failed: {type(error).__name__}. Check configuration and service state; no credentials were printed.')
