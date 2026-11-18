"""Portable build/release entry points. Never deploy from a mutable working tree."""
import argparse,hashlib,io,json,os,pathlib,re,shutil,subprocess,tarfile,tempfile,time
ROOT=pathlib.Path(__file__).resolve().parents[1]
RELEASES=ROOT/'artifacts/releases'
FILES=['VERSION.json','README.md','OPERATIONS.md','LIBRARY.md','.gitignore','.gitattributes','manage']
DIRS=['server','web/src','android/app/src','android/gradle','deploy','scripts','.github']
EXTRA=['web/package.json','web/package-lock.json','web/index.html','web/tsconfig.json','web/vite.config.ts','android/app/build.gradle','android/build.gradle','android/settings.gradle','android/gradle.properties','android/gradlew','android/gradlew.bat']

def run(args,cwd=ROOT,env=None,capture=False):
    result=subprocess.run([str(a) for a in args],cwd=cwd,env=env,check=False,text=True,stdout=subprocess.PIPE if capture else None)
    if result.returncode:raise RuntimeError(f'Command failed ({result.returncode}): {args[0]} {args[1] if len(args)>1 else ""}')
    return result.stdout.strip() if capture else ''
def digest(path):
    h=hashlib.sha256()
    with open(path,'rb') as f:
        for chunk in iter(lambda:f.read(1<<20),b''):h.update(chunk)
    return h.hexdigest()
def write_json(path,value):pathlib.Path(path).write_text(json.dumps(value,indent=2,ensure_ascii=False)+'\n')
def inputs():
    paths=[]
    for name in FILES+EXTRA:
        p=ROOT/name
        if p.is_file():paths.append(p)
    for name in DIRS:
        for p in (ROOT/name).rglob('*'):
            if p.is_file() and not p.is_symlink() and '__pycache__' not in p.parts and p.suffix not in {'.pyc','.apk','.jks','.keystore'} and p.name not in {'.env','local.properties'}:paths.append(p)
    return sorted(set(paths))
def fingerprint():
    h=hashlib.sha256()
    for p in inputs():h.update(p.relative_to(ROOT).as_posix().encode());h.update(bytes.fromhex(digest(p)))
    return h.hexdigest()
def checked_version(version):
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.]+)?',version):raise ValueError('Version must be semver such as 0.2.0')
    return version
def snapshot(path,commit):
    upstream=ROOT.parent/'open-im-server'
    if run(['git','rev-parse','HEAD'],upstream, capture=True)!=commit:raise ValueError('OpenIM revision differs from VERSION.json')
    if run(['git','status','--porcelain','--untracked-files=no'],upstream,capture=True):raise ValueError('OpenIM checkout has changes; commit/pin them before release')
    with tarfile.open(path,'w:gz') as archive:
        for p in inputs():archive.add(p,arcname='support-chat/'+p.relative_to(ROOT).as_posix(),recursive=False)
        data=subprocess.check_output(['git','archive',commit],cwd=upstream)
        with tarfile.open(fileobj=io.BytesIO(data)) as src:
            for member in src:
                # Even the upstream's public example .env is not a deployment credential source.
                if pathlib.PurePosixPath(member.name).name=='.env':continue
                member.name='open-im-server/'+member.name
                archive.addfile(member,src.extractfile(member) if member.isfile() else None)
def seal(directory,metadata):
    metadata['files']={p.name:{'sha256':digest(p),'size':p.stat().st_size} for p in sorted(directory.iterdir()) if p.is_file() and p.name!='release.json'}
    write_json(directory/'release.json',metadata)
def verify(directory,kind=None):
    directory=pathlib.Path(directory).resolve();m=json.loads((directory/'release.json').read_text())
    if m.get('format')!=1 or (kind and m.get('kind')!=kind):raise ValueError('Unsupported release format/kind')
    checked_version(m['version'])
    if not m.get('files'):raise ValueError('Empty release manifest')
    for name,expected in m['files'].items():
        if pathlib.Path(name).name!=name or name in {'.','..','release.json'}:raise ValueError('Unsafe release path')
        p=directory/name
        if not p.is_file() or p.is_symlink() or p.stat().st_size!=expected['size'] or digest(p)!=expected['sha256']:raise ValueError(f'Checksum mismatch: {name}')
    if kind=='bundle':
        for name in ['gateway','openim','mongo','redis','caddy']:
            if not re.fullmatch(r'sha256:[0-9a-f]{64}',m.get('images',{}).get(name,'')):raise ValueError('Invalid immutable image identifier')
        if not {'images.tar','compose.json','Caddyfile'}<=m['files'].keys():raise ValueError('Incomplete bundle')
    return m

def check():
    run(['go','test','-race','./...'],ROOT/'server');run(['go','vet','./...'],ROOT/'server')
    run(['python3','-m','unittest','discover','-s','scripts','-p','test_*.py'])
    run(['npm','ci'],ROOT/'web');run(['npm','run','build'],ROOT/'web')

def build(args):
    cfg=json.loads((ROOT/'VERSION.json').read_text());version=checked_version(args.version or cfg['version']);code=args.version_code or cfg['versionCode']
    if not 0<code<2100000000:raise ValueError('Invalid Android version code')
    target=RELEASES/version
    if target.exists():raise ValueError('Release version already exists; use a new version')
    before=fingerprint();check();RELEASES.mkdir(parents=True,exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.building-',dir=RELEASES) as work:
        tmp=pathlib.Path(work);snapshot(tmp/'source.tar.gz',cfg['openimCommit'])
        run(['go','build','-mod=readonly','-trimpath','-ldflags',f'-X main.buildVersion={version}','-o',tmp/'gateway','.'],ROOT/'server')
        run(['go','build','-mod=readonly','-trimpath','-o',tmp/'openim','./cmd'],ROOT.parent/'open-im-server')
        with tarfile.open(tmp/'web.tar.gz','w:gz') as archive:archive.add(ROOT/'web/dist',arcname='web')
        if args.android!='none':
            env=os.environ.copy()
            if env.get('ANDROID_JAVA_HOME'):env['JAVA_HOME']=env['ANDROID_JAVA_HOME']
            elif pathlib.Path('/usr/lib/jvm/java-21-openjdk').exists():env['JAVA_HOME']='/usr/lib/jvm/java-21-openjdk'
            mode=args.android.capitalize()
            cmd=['./gradlew',f'assembleCustomer{mode}',f'assembleAgent{mode}',f'-PappVersion={version}',f'-PappVersionCode={code}','--console=plain']
            if args.server_url:cmd.append('-PserverUrl='+args.server_url)
            run(cmd,ROOT/'android',env=env)
            for role in ['customer','agent']:
                src=ROOT/f'android/app/build/outputs/apk/{role}/{args.android}/app-{role}-{args.android}.apk'
                if not src.exists():raise ValueError(f'Expected signed APK missing: {src}')
                shutil.copy2(src,tmp/f'{role}-{args.android}.apk')
        if fingerprint()!=before:raise ValueError('Source changed during build; discard and rebuild')
        seal(tmp,{'format':1,'kind':'build','version':version,'versionCode':code,'schemaVersion':cfg['schemaVersion'],'openimCommit':cfg['openimCommit'],'sourceFingerprint':before,'android':args.android,'builtAt':time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())})
        os.rename(tmp,target)
    print(f'Build ready: {target}')

def bundle(args):
    src=pathlib.Path(args.release).resolve();m=verify(src,'build');target=src.parent/(m['version']+'-bundle')
    if target.exists():raise ValueError('Bundle already exists; never overwrite a published version')
    run(['docker','info','--format','{{.ServerVersion}}'])
    base=verify(args.base,'bundle') if args.base else None
    if base:run(['docker','image','load','-i',pathlib.Path(args.base).resolve()/'images.tar'])
    with tempfile.TemporaryDirectory(prefix='.bundling-',dir=src.parent) as work, tempfile.TemporaryDirectory() as extraction:
        tmp=pathlib.Path(work);tree=pathlib.Path(extraction)
        with tarfile.open(src/'source.tar.gz') as archive:archive.extractall(tree,filter='data')
        app=tree/'support-chat';images={}
        openim_fingerprint=hashlib.sha256((m['openimCommit']+digest(app/'scripts/configure_openim.py')+digest(app/'deploy/OpenIM.Dockerfile')+digest(app/'deploy/OpenIM.Dockerfile.dockerignore')).encode()).hexdigest()
        for service,file in [('gateway','Gateway.Dockerfile'),('openim','OpenIM.Dockerfile')]:
            if service=='openim' and base and base.get('openimBuildFingerprint')==openim_fingerprint:
                images[service]=base['images'][service];run(['docker','image','inspect',images[service]],capture=True);continue
            tag=f'support-chat/{service}:{m["version"]}'
            run(['docker','build','--pull','--build-arg',f'APP_VERSION={m["version"]}','-t',tag,'-f',app/'deploy'/file,tree])
            images[service]=run(['docker','image','inspect','--format','{{.Id}}',tag],capture=True)
        for name,tag in {'mongo':'mongo:7.0','redis':'redis:7.4-alpine','caddy':'caddy:2-alpine'}.items():
            if base:
                image=base['images'][name];run(['docker','image','inspect',image],capture=True)
            else:
                run(['docker','pull',tag]);image=run(['docker','image','inspect','--format','{{.Id}}',tag],capture=True)
            images[name]=image
        run(['docker','image','save','-o',tmp/'images.tar',*images.values()])
        shutil.copy2(app/'deploy/compose.production.json',tmp/'compose.json');shutil.copy2(app/'deploy/Caddyfile',tmp/'Caddyfile')
        # Include the exact operations runner that produced this bundle; deploying requires Python and Docker only.
        for file in ['ops.py','release.py']:shutil.copy2(app/'scripts'/file,tmp/file)
        for file in src.glob('*.apk'):shutil.copy2(file,tmp/file.name)
        seal(tmp,{**{k:v for k,v in m.items() if k!='files'},'kind':'bundle','images':images,'openimBuildFingerprint':openim_fingerprint})
        os.rename(tmp,target)
    print(f'Deployment bundle ready: {target}')

def main(argv=None):
    p=argparse.ArgumentParser();sub=p.add_subparsers(dest='command',required=True)
    sub.add_parser('check');b=sub.add_parser('build');b.add_argument('--version');b.add_argument('--version-code',type=int);b.add_argument('--android',choices=['none','debug','release'],default='debug');b.add_argument('--server-url')
    b=sub.add_parser('bundle');b.add_argument('release');b.add_argument('--base',help='Reuse MongoDB/Redis/Caddy image IDs from a prior bundle')
    a=p.parse_args(argv)
    if a.command=='check':check()
    elif a.command=='build':build(a)
    else:bundle(a)
if __name__=='__main__':main()
