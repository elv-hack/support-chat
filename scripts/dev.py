#!/usr/bin/env python3
"""Local stack supervisor. No sudo/Docker; binds every component to loopback."""
import argparse,json,os,pathlib,secrets,signal,socket,subprocess,sys,time
ROOT=pathlib.Path(__file__).resolve().parents[1];LOCAL=ROOT/'.local';os.chdir(ROOT)
p=argparse.ArgumentParser();p.add_argument('action',choices=['start','stop','status']);args=p.parse_args()
LOCAL.mkdir(exist_ok=True);pidfile=LOCAL/'processes.json'
secretsfile=LOCAL/'dev-secrets.json'
if not secretsfile.exists():secretsfile.write_text(json.dumps({k:secrets.token_urlsafe(24) for k in ['OPENIM_SECRET','REDIS_PASSWORD','ADMIN_PASSWORD']}));secretsfile.chmod(0o600)
secret=json.loads(secretsfile.read_text())
if 'DIRECTORY_TOKEN' not in secret:
    secret['DIRECTORY_TOKEN']=secrets.token_hex(32);secretsfile.write_text(json.dumps(secret));secretsfile.chmod(0o600)
env={**os.environ,**secret,'OPENIM_URL':'http://127.0.0.1:18002','DATA_DIR':str(LOCAL/'gateway-data'),'WEB_DIR':str(ROOT/'web/dist'),'DIRECTORY_URL':'http://127.0.0.1:8095','DIRECTORY_LISTEN':'127.0.0.1:8095','DIRECTORY_DATA_DIR':str(LOCAL/'directory-data'),'ALLOW_LOCAL_ROUTING':'1'}
pids=json.loads(pidfile.read_text()) if pidfile.exists() else {}
def alive(pid):
    try:os.kill(pid,0);return True
    except ProcessLookupError:return False

def port_open(port):
    try:
        with socket.create_connection(('127.0.0.1',port),timeout=.2):return True
    except OSError:return False

if args.action=='status':
    for name,port in [('MongoDB',37027),('Redis',16389),('OpenIM',18002),('gateway',8090),('directory',8095)]:print(f'{name}: {"listening" if port_open(port) else "stopped"} ({port})')
    sys.exit()
if args.action=='stop':
    for name in ['gateway','openim','redis','mongo','directory']:
        pid=pids.get(name)
        if pid and alive(pid):
            # Only signal the process if its Linux command line still matches our own recorded executable.
            cmdline=pathlib.Path(f'/proc/{pid}/cmdline')
            if cmdline.exists() and (str(LOCAL) in cmdline.read_bytes().decode(errors='replace') or name=='redis' and 'redis-server' in cmdline.read_bytes().decode(errors='replace')):
                os.kill(pid,signal.SIGTERM)
    pidfile.unlink(missing_ok=True);print('Stopped the recorded local stack.');sys.exit()
for folder in ['mongo','redis','logs']:(LOCAL/folder).mkdir(exist_ok=True)
mongods=list((LOCAL/'tools').glob('mongodb-*/bin/mongod'))
if not mongods:sys.exit('MongoDB binary missing. See README local setup.')
if not (LOCAL/'openim').exists() or not (LOCAL/'gateway').exists():sys.exit('Build .local/openim and .local/gateway first; see README.')
subprocess.run(['uv','run','--with','pyyaml','python','scripts/configure_openim.py','--source','../open-im-server/config','--dest',str(LOCAL/'config')],env=env,check=True)
conf=LOCAL/'redis.conf';conf.write_text(f'bind 127.0.0.1\nport 16389\nrequirepass {secret["REDIS_PASSWORD"]}\nappendonly yes\ndir {LOCAL/"redis"}\n');conf.chmod(0o600)
commands=[('mongo',37027,[str(mongods[0]),'--dbpath',str(LOCAL/'mongo'),'--bind_ip','127.0.0.1','--port','37027','--wiredTigerCacheSizeGB','0.5','--logpath',str(LOCAL/'logs/mongo.log'),'--logappend']),('redis',16389,['redis-server',str(conf)]),('openim',18002,[str(LOCAL/'openim'),'-c',str(LOCAL/'config')]),('directory',8095,[str(LOCAL/'gateway'),'directory']),('gateway',8090,[str(LOCAL/'gateway')])]
for name,port,command in commands:
    if port_open(port):print(f'{name}: already listening');continue
    logfile=open(LOCAL/'logs'/f'{name}.console.log','ab')
    proc=subprocess.Popen(command,cwd=ROOT,env=env,stdin=subprocess.DEVNULL,stdout=logfile,stderr=subprocess.STDOUT,start_new_session=True)
    logfile.close();pids[name]=proc.pid;pidfile.write_text(json.dumps(pids));pidfile.chmod(0o600)
    for _ in range(80):
        if port_open(port):break
        if proc.poll() is not None:sys.exit(f'{name} failed. Inspect .local/logs/{name}.console.log')
        time.sleep(.25)
    else:sys.exit(f'{name} did not become ready. Inspect .local/logs/{name}.console.log')
    print(f'{name}: ready')
print('Customer: http://127.0.0.1:8090/customer\nAgent: http://127.0.0.1:8090/agent\nInitial admin username: admin; password: .local/dev-secrets.json → ADMIN_PASSWORD')
