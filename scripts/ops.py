"""Single-host release operations. No destructive in-place data restore; all changes serialized."""
import argparse,contextlib,fcntl,hashlib,json,os,pathlib,re,secrets,shutil,subprocess,tarfile,time,urllib.request
from release import verify,digest,write_json
SERVICES=['gateway','openim','mongo','redis']
DATA=['mongo','redis','gateway']

def atomic(path,data):
    tmp=path.with_suffix('.tmp')
    with open(tmp,'w') as f:
        os.chmod(tmp,0o600);json.dump(data,f,ensure_ascii=False,indent=2);f.write('\n');f.flush();os.fsync(f.fileno())
    os.replace(tmp,path)
    fd=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY)
    try:os.fsync(fd)
    finally:os.close(fd)
def read_env(path):
    result={}
    for line in path.read_text().splitlines():
        line=line.strip()
        if not line or line.startswith('#'):continue
        if '=' not in line:raise ValueError('Invalid environment file')
        key,value=line.split('=',1)
        if not re.fullmatch(r'[A-Z_][A-Z_0-9]*',key):raise ValueError('Invalid environment key')
        result[key]=value
    return result

def safe_tar(path):
    with tarfile.open(path) as archive:
        for member in archive:
            p=pathlib.PurePosixPath(member.name)
            if p.is_absolute() or '..' in p.parts or member.issym() or member.islnk() or not (member.isfile() or member.isdir()):raise ValueError('Unsafe backup archive member')

class Ops:
    def __init__(self,root):self.root=pathlib.Path(root).resolve();self.manifests={}
    def metadata(self,release,kind='bundle'):
        key=str(pathlib.Path(release).resolve())
        if key not in self.manifests:self.manifests[key]=verify(release,kind)
        return self.manifests[key]
    def credential_hash(self):
        env=read_env(self.root/'.env');keys=['OPENIM_SECRET','MONGO_PASSWORD','REDIS_PASSWORD','ADMIN_USERNAME','ADMIN_PASSWORD']
        return hashlib.sha256(json.dumps({k:env.get(k,'') for k in keys},sort_keys=True).encode()).hexdigest()
    def state(self):return json.loads((self.root/'state.json').read_text()) if (self.root/'state.json').exists() else {'current':None,'previous':None,'pending':None}
    def save(self,state):atomic(self.root/'state.json',state)
    def init(self,project,port):
        if not re.fullmatch(r'[a-z][a-z0-9_-]{2,40}',project):raise ValueError('Invalid project name')
        if not 1024<=port<=65535:raise ValueError('Use an unprivileged gateway port')
        self.root.mkdir(parents=True,exist_ok=True);os.chmod(self.root,0o700)
        if (self.root/'config.json').exists() or (self.root/'.env').exists():raise ValueError('Runtime already initialized; no credentials overwritten')
        secret={k:secrets.token_hex(24) for k in ['OPENIM_SECRET','MONGO_PASSWORD','REDIS_PASSWORD','ADMIN_PASSWORD']}
        secret.update(ADMIN_USERNAME='admin',CHAT_DOMAIN='chat.example.com',GATEWAY_PORT=str(port),PUBLIC_HTTPS='0',DIRECTORY_URL='',DIRECTORY_TOKEN='')
        (self.root/'.env').write_text(''.join(k+'='+v+'\n' for k,v in secret.items()));os.chmod(self.root/'.env',0o600)
        atomic(self.root/'config.json',{'project':project});self.save({'current':None,'previous':None,'pending':None})
        print(f'Initialized {self.root}; edit .env for domain / PUBLIC_HTTPS. Initial admin password is ADMIN_PASSWORD in that file.')
    @contextlib.contextmanager
    def lock(self):
        if not (self.root/'config.json').exists():raise ValueError('Run init first')
        with open(self.root/'operation.lock','a') as lock:
            try:fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
            except BlockingIOError:raise ValueError('Another deployment/backup is running')
            yield
    def env(self,release):
        env=os.environ.copy();env.update(read_env(self.root/'.env'))
        m=self.metadata(release,'bundle');env.update({k.upper()+'_IMAGE':v for k,v in m['images'].items()})
        return env
    def project(self):return json.loads((self.root/'config.json').read_text())['project']
    def command(self,args,capture=False):
        r=subprocess.run([str(x) for x in args],check=False,text=True,stdout=subprocess.PIPE if capture else None)
        if r.returncode:raise RuntimeError(f'{args[0]} {args[1]} failed ({r.returncode}); no secrets included in this error')
        return r.stdout.strip() if capture else ''
    def compose(self,release,*args,capture=False):
        env=self.env(release);cmd=['docker','compose','--project-name',self.project(),'--env-file',str(self.root/'.env'),'-f',str(pathlib.Path(release)/'compose.json')]
        if env.get('PUBLIC_HTTPS')=='1':cmd+=['--profile','public']
        r=subprocess.run(cmd+list(args),env=env,text=True,stdout=subprocess.PIPE if capture else None,check=False)
        if r.returncode:raise RuntimeError(f'Compose {args[0]} failed ({r.returncode})')
        return r.stdout.strip() if capture else ''
    def audit(self,event,**fields):
        with open(self.root/'operations.jsonl','a') as f:f.write(json.dumps({'time':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),'event':event,**fields})+'\n')
    def stage(self,bundle):
        src=pathlib.Path(bundle).resolve();m=self.metadata(src,'bundle');dst=self.root/'releases'/m['version']
        if dst.exists():
            if digest(dst/'release.json')!=digest(src/'release.json'):raise ValueError('Same version has different contents')
            self.metadata(dst,'bundle')
        else:
            dst.parent.mkdir(exist_ok=True);tmp=dst.with_name('.'+dst.name+'.incoming')
            if tmp.exists():raise ValueError('Incomplete incoming directory exists; inspect it before retrying')
            shutil.copytree(src,tmp);self.metadata(tmp,'bundle');os.rename(tmp,dst)
        self.command(['docker','image','load','-i',dst/'images.tar'])
        for image in m['images'].values():
            found=self.command(['docker','image','inspect','--format','{{.Id}}',image],capture=True)
            if found!=image:raise ValueError('Loaded image identity mismatch')
        # Compose validation completes before any running service is stopped.
        self.compose(dst,'config','--quiet')
        return dst
    def up(self,release):
        self.compose(release,'up','-d','--no-build','--pull','never','--wait','--wait-timeout','180')
        cfg=read_env(self.root/'.env');m=self.metadata(release,'bundle')
        with urllib.request.urlopen('http://127.0.0.1:'+cfg.get('GATEWAY_PORT','8090')+'/api/ready',timeout=8) as r:ready=json.load(r)
        if ready.get('version')!=m['version'] or ready.get('schemaVersion')!=m['schemaVersion']:raise RuntimeError('Readiness version/schema mismatch')
    def stop(self,release):self.compose(release,'stop','--timeout','60',*SERVICES)
    def compatible(self,current,target):
        old,new=self.metadata(current,'bundle'),self.metadata(target,'bundle')
        if old['schemaVersion']!=new['schemaVersion']:raise ValueError('Schema change requires a separately rehearsed migration; automatic upgrade/rollback refused')
        for name in ['mongo','redis','openim','caddy']:
            if old['images'][name]!=new['images'][name]:raise ValueError(f'{name} image changed; infrastructure upgrades require a separate maintenance procedure')
    def backup(self,release,resume=True):
        self.metadata(release,'bundle');out=self.root/'backups'/(time.strftime('%Y%m%dT%H%M%SZ',time.gmtime())+'-'+secrets.token_hex(3));out.mkdir(parents=True)
        metadata={'format':1,'release':str(release),'releaseManifest':digest(pathlib.Path(release)/'release.json'),'credentialHash':self.credential_hash(),'project':self.project(),'files':{}}
        stopped=False
        try:
            stopped=True;self.stop(release)
            # Query actual containers rather than trusting a successful stop return code.
            ids=self.compose(release,'ps','-a','-q',*SERVICES,capture=True).splitlines()
            if len(ids)!=len(SERVICES):raise RuntimeError('Backup requires all four known service containers')
            for cid in ids:
                state=json.loads(self.command(['docker','inspect','--format','{{json .State}}',cid],capture=True))
                if state.get('Running') or state.get('OOMKilled') or state.get('ExitCode')==137:raise RuntimeError('Unclean stop; refusing to label backup consistent')
            helper=self.metadata(release,'bundle')['images']['openim']
            for name in DATA:
                filename=name+'.tar'
                self.command(['docker','run','--rm','--network','none','--user','0','--mount',f'type=volume,src={self.project()}_{name},dst=/source,readonly','--mount',f'type=bind,src={out},dst=/backup','--entrypoint','tar',helper,'-C','/source','-cpf','/backup/'+filename,'.'])
                metadata['files'][filename]={'sha256':digest(out/filename),'size':(out/filename).stat().st_size}
            atomic(out/'backup.json',metadata);self.audit('backup',path=str(out));print(f'Consistent backup: {out}')
            return out
        finally:
            # A standalone backup always brings the old version back, even if copying a volume failed.
            if resume and stopped:self.up(release)
    def deploy(self,bundle):
        state=self.state()
        if state.get('pending'):raise ValueError('Interrupted operation detected; run recover first')
        target=self.stage(bundle);current=state['current']
        if current and pathlib.Path(current)==target:raise ValueError('This version is already current')
        if current:self.compatible(current,target)
        else:
            names=self.command(['docker','volume','ls','--format','{{.Name}}'],capture=True).splitlines()
            if any(self.project()+'_'+n in names for n in DATA):raise ValueError('Existing data volumes without release state; refusing to adopt silently')
        state['pending']={'target':str(target),'from':current,'backup':None};self.save(state)
        try:
            if current:
                backup=self.backup(current,resume=False);state['pending']['backup']=str(backup);self.save(state)
            self.up(target)
            self.save({'current':str(target),'previous':current,'pending':None});self.audit('deployed',version=target.name)
        except BaseException:
            self.audit('deploy_failed',target=str(target))
            try:
                self.stop(target)
                if current:
                    self.up(current)
                    self.save({'current':current,'previous':state['previous'],'pending':None});self.audit('code_rollback',to=current)
                else:self.audit('initial_install_recovery_required')
            except BaseException:self.audit('recovery_required')
            raise
    def rollback(self):
        s=self.state()
        if s.get('pending'):raise ValueError('Run recover first')
        if not s['current'] or not s['previous']:raise ValueError('No previous version to roll back to')
        # Uses the same backup/check/health path and preserves all current data.
        self.deploy(s['previous'])
    def recover(self):
        s=self.state();pending=s.get('pending')
        if not pending:raise ValueError('No interrupted operation')
        self.stop(pending['target'])
        if pending.get('restore') and not pending.get('restoreComplete'):raise ValueError('Restore interrupted before all volumes were extracted. Inspect this isolated project; retry into another empty runtime, never start partial data.')
        target=pending['from'] or pending['target']
        self.up(target)
        self.save({'current':target,'previous':s['previous'],'pending':None});self.audit('recovered',to=target)
    def restore(self,bundle,backup):
        s=self.state()
        if s['current'] or s.get('pending'):raise ValueError('Restore is only supported into a new empty runtime')
        src=pathlib.Path(backup).resolve();m=json.loads((src/'backup.json').read_text())
        if m.get('format')!=1 or set(m.get('files',{}))!={'mongo.tar','redis.tar','gateway.tar'}:raise ValueError('Invalid backup manifest')
        if m['credentialHash']!=self.credential_hash():raise ValueError('Restore requires the original credentials; copy the original .env, then change only domain/port if needed')
        if m['releaseManifest']!=digest(pathlib.Path(bundle)/'release.json'):raise ValueError('Restore requires the exact original release bundle')
        for name,info in m['files'].items():
            p=src/name
            if p.is_symlink() or digest(p)!=info['sha256'] or p.stat().st_size!=info['size']:raise ValueError('Backup checksum mismatch')
            safe_tar(p)
        names=self.command(['docker','volume','ls','--format','{{.Name}}'],capture=True).splitlines()
        if any(self.project()+'_'+n in names for n in DATA):raise ValueError('Target data volumes must be absent; never overwrite a database')
        target=self.stage(bundle);helper=self.metadata(target,'bundle')['images']['openim']
        self.save({'current':None,'previous':None,'pending':{'target':str(target),'from':None,'restore':str(src)}})
        for name in DATA:
            volume=self.project()+'_'+name;self.command(['docker','volume','create','--label',f'com.docker.compose.project={self.project()}','--label',f'com.docker.compose.volume={name}',volume])
            self.command(['docker','run','--rm','--network','none','--user','0','--mount',f'type=volume,src={volume},dst=/target','--mount',f'type=bind,src={src},dst=/backup,readonly','--entrypoint','tar',helper,'-C','/target','-xpf','/backup/'+name+'.tar'])
        state=self.state();state['pending']['restoreComplete']=True;self.save(state)
        self.up(target);self.save({'current':str(target),'previous':None,'pending':None});self.audit('restored_to_empty_project',backup=str(src))

def main(argv=None):
    os.umask(0o077)
    p=argparse.ArgumentParser();p.add_argument('command',choices=['init','deploy','backup','rollback','recover','restore','status','logs']);p.add_argument('bundle',nargs='?');p.add_argument('--root',default='/srv/support-chat');p.add_argument('--project',default='support-chat-prod');p.add_argument('--port',type=int,default=8090);p.add_argument('--backup');args=p.parse_args(argv)
    ops=Ops(args.root)
    if args.command=='init':ops.init(args.project,args.port);return
    with ops.lock():
        if args.command=='deploy':
            if not args.bundle:raise ValueError('deploy requires a bundle directory')
            ops.deploy(args.bundle)
        elif args.command=='rollback':ops.rollback()
        elif args.command=='recover':ops.recover()
        elif args.command=='restore':
            if not args.bundle or not args.backup:raise ValueError('restore requires bundle and --backup')
            ops.restore(args.bundle,args.backup)
        elif args.command=='status':
            s=ops.state();print(json.dumps(s,indent=2))
            if s['current']:ops.compose(s['current'],'ps')
        elif args.command=='logs':
            s=ops.state()
            if not s['current']:raise ValueError('No deployed release')
            ops.compose(s['current'],'logs','--tail','100','gateway','openim')
        else:
            s=ops.state()
            if s.get('pending') or not s['current']:raise ValueError('Backup requires a stable deployed release')
            ops.backup(s['current'])
if __name__=='__main__':
    try:main()
    except (ValueError,RuntimeError,OSError) as error:raise SystemExit(f'ERROR: {error}')
