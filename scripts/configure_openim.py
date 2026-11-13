#!/usr/bin/env python3
"""Generate isolated standalone OpenIM configuration; never modify the upstream checkout."""
import argparse, os, pathlib, shutil
import yaml
p=argparse.ArgumentParser();p.add_argument('--source', required=True);p.add_argument('--dest', required=True);p.add_argument('--docker', action='store_true');args=p.parse_args()
src=pathlib.Path(args.source);dst=pathlib.Path(args.dest);dst.mkdir(parents=True,exist_ok=True)
secret=os.environ['OPENIM_SECRET'];redis_password=os.environ['REDIS_PASSWORD']
for source in src.glob('*.yml'):
    data=yaml.safe_load(source.read_text()) or {}
    if isinstance(data,dict):
        if isinstance(data.get('prometheus'),dict):data['prometheus']['enable']=False
        if isinstance(data.get('rpc'),dict):data['rpc']['listenIP']='127.0.0.1';data['rpc']['registerIP']='127.0.0.1'
        if source.name=='share.yml':data['secret']=secret;data['queue']='redis'
        elif source.name=='mongodb.yml':
            data.update(uri='' if args.docker else 'mongodb://127.0.0.1:37027/support_openim',address=['mongo:27017' if args.docker else '127.0.0.1:37027'],database='support_openim',username='',password='',authSource='')
            if args.docker:data.update(username='root',password=os.environ['MONGO_PASSWORD'],authSource='admin')
        elif source.name=='redis.yml':data.update(address=['redis:6379' if args.docker else '127.0.0.1:16389'],password=redis_password)
        elif source.name=='openim-rpc-third.yml':data['object']['enable']=''
        elif source.name=='openim-api.yml':data['api'].update(listenIP='0.0.0.0' if args.docker else '127.0.0.1',ports=[18002])
        elif source.name=='openim-msggateway.yml':data['listenIP']='127.0.0.1';data['longConnSvr']['ports']=[18001]
        elif source.name=='log.yml':data.update(storageLocation=str(dst.parent/'logs'),remainLogLevel=3,isStdout=False)
        elif source.name=='openim-crontask.yml':
            data['retainChatRecords']=0
            # Preserve message history until the operator explicitly deletes it.
            for key,value in data.items():
                if isinstance(value,dict) and 'enable' in value:value['enable']=False
    (dst/source.name).write_text(yaml.safe_dump(data,allow_unicode=True,sort_keys=False))
    (dst/source.name).chmod(0o600)
print(f'OpenIM configuration generated in {dst}')
