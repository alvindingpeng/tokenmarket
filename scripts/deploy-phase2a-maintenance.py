#!/usr/bin/env python3
"""Authorized root wrapper: pause ONLY prod.example.com, drain, deploy, restore ingress on success."""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import time
import urllib.request

SITE=Path('/etc/nginx/sites-available/switchyard-tt')
MARKER='    location / {\n        proxy_pass http://127.0.0.1:8088;'

def run(*args):
    return subprocess.run(args,check=True,capture_output=True,text=True).stdout

def main():
    stage=Path(sys.argv[1]).resolve()
    candidate=stage/'switchyard-phase2a'
    checksum=sys.argv[2]
    original=SITE.read_text()
    if original.count(MARKER)!=1: raise RuntimeError('unexpected target nginx configuration')
    backup=stage/'nginx.original'
    backup.write_text(original);backup.chmod(0o600)
    paused=False
    deployed=False
    try:
        SITE.write_text(original.replace(MARKER,'    location / {\n        return 503;\n        proxy_pass http://127.0.0.1:8088;'))
        run('nginx','-t');run('systemctl','reload','nginx');paused=True
        # Verify new ingress is closed; localhost backend remains available for drain/health.
        for _ in range(20):
            probe=run('curl','-sk','--noproxy','*','--max-time','10','--resolve','prod.example.com:443:127.0.0.1','-o','/dev/null','-w','%{http_code}','https://prod.example.com/')
            if probe=='503': break
            time.sleep(0.25)
        else: raise RuntimeError('maintenance ingress was not confirmed: '+probe)
        for _ in range(120):
            with urllib.request.urlopen('http://127.0.0.1:8088/healthz',timeout=5) as response: health=json.load(response)
            if health.get('active_requests')==0: break
            time.sleep(1)
        else: raise RuntimeError('requests did not drain; aborting before install')
        result=run('python3',str(stage/'production-financial-release.py'),'--candidate',str(candidate),'--sha256',checksum,'--audit-script',str(stage/'audit-financial-history.py'),'--traffic-paused')
        print(result,flush=True)
        deployed=True
    finally:
        # Failed-after-install leaves backend stopped and ingress paused for manual compatible recovery.
        active=subprocess.run(['systemctl','is-active','--quiet','switchyard']).returncode==0
        if deployed or active or not paused:
            SITE.write_text(original)
            run('nginx','-t');run('systemctl','reload','nginx')
            print(json.dumps({'ingress_restored':True,'deployed':deployed}),flush=True)
        else:
            print(json.dumps({'ingress_restored':False,'manual_recovery_required':True,'nginx_backup':str(backup)}),flush=True)

if __name__=='__main__': main()
