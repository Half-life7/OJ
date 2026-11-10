"""Run shell syntax and destructive-path guards in a read-only test container."""
import subprocess
from pathlib import Path
root=str(Path(__file__).resolve().parents[1])
base=['docker','run','--rm','--network','none','--read-only','--mount','type=bind,source='+root+',target=/source,readonly']
syntax=subprocess.run(base+['biue-deploy-test:0.1.0','-c','for file in /source/deploy.sh /source/tools/check_*.sh; do bash -n "$file" || exit; done'],capture_output=True,text=True,encoding='utf-8',errors='replace',timeout=30)
assert syntax.returncode==0,syntax.stderr
for values,expected in [({'OJ_DATA_DIR':'/'},'数据目录'),({'OJ_DATA_DIR':'/etc'},'系统目录'),({'OJ_DATA_DIR':'/source/data'},'源码目录之外'),({'OJ_DATA_DIR':'/var/lib/biue','OJ_BACKUP_DIR':'/source/backups'},'备份必须与源码分离'),({'OJ_DATA_DIR':'/var/lib/biue','OJ_BACKUP_DIR':'/var/lib/biue/config/backups'},'业务或配置归档目录')]:
    args=base[:]
    for k,v in values.items():args+=['-e',k+'='+v]
    r=subprocess.run(args+['biue-deploy-test:0.1.0','/source/deploy.sh','install'],capture_output=True,text=True,encoding='utf-8',errors='replace',timeout=20)
    assert r.returncode!=0 and expected in r.stdout+r.stderr,(values,r.stdout,r.stderr)
print('All Bash syntax and system/source/backup path guards passed in read-only Linux containers')
