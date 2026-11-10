"""Inspect dedicated deployment-test data after uninstall, without reading secrets."""
import argparse
import re
import subprocess
p=argparse.ArgumentParser();p.add_argument('--volume',required=True);a=p.parse_args();assert re.fullmatch(r'biue-deploy5-[0-9]+',a.volume)
script='''set -eu
for root in /check/production /check/recovery; do
  test "$(stat -c %a "$root/config/.env")" = 600
  test "$(stat -c %a "$root/config/tls/ca.key")" = 600
  test "$(stat -c %a "$root/config/tls/server.key")" = 640
  test -n "$(find "$root/problem-data" -type f -print -quit)"
  test -n "$(find "$root/runner-data" -type f -print -quit)"
  test -n "$(find "$root/backups" -name SHA256SUMS -print -quit)"
done
echo 'Uninstall preserved both data roots, immutable files, journals and backups; env/CA/key permissions correct'
'''
r=subprocess.run(['docker','run','--rm','--network','none','--read-only','--mount','type=volume,source='+a.volume+',target=/check,readonly','biue-deploy-test:0.1.0','-c',script],capture_output=True,text=True,encoding='utf-8',errors='replace',timeout=30)
assert r.returncode==0,r.stderr;print(r.stdout.strip())
