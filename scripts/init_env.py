#!/usr/bin/env python3
import pathlib,secrets
p=pathlib.Path(__file__).resolve().parents[1]/'.env'
if p.exists():raise SystemExit('.env already exists; not overwritten.')
values={k:secrets.token_hex(24) for k in ['OPENIM_SECRET','MONGO_PASSWORD','REDIS_PASSWORD','ADMIN_PASSWORD']}
values.update(ADMIN_USERNAME='admin',CHAT_DOMAIN='chat.example.com')
p.write_text(''.join(f'{k}={v}\n' for k,v in values.items()));p.chmod(0o600)
print('Created .env. Set CHAT_DOMAIN to your domain. Initial login: ADMIN_USERNAME / ADMIN_PASSWORD in .env.')
