"""Assert the first downgrade guard fails safely with existing SPJ snapshots."""
import subprocess
from pathlib import Path
from check_contests import compose,sql
assert int(sql("SELECT COUNT(*) FROM problem_versions WHERE JSON_UNQUOTE(JSON_EXTRACT(snapshot_json,'$.checker_mode'))='spj'"))>0
statement=Path('migrations/000009_extensions.down.sql').read_text().split(';',1)[0]
shell='MYSQL_PWD="$MYSQL_PASSWORD" exec mysql --user="$MYSQL_USER" --database="$MYSQL_DATABASE" --batch --execute="$1"'
r=subprocess.run(compose+['exec','-T','mysql','sh','-c',shell,'sh',statement],capture_output=True,text=True,encoding='utf-8',errors='replace',timeout=20)
assert r.returncode!=0 and 'ERROR 3819 ' in r.stderr,'downgrade guard did not reject extended snapshots as expected'
assert sql("SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS WHERE CONSTRAINT_SCHEMA=DATABASE() AND CONSTRAINT_NAME='ck_v9_rollback_guard'")=='0'
assert sql('SELECT CONCAT(version,":",dirty) FROM schema_migrations')=='9:0'
print('Downgrade rejected SPJ snapshots without changing schema or migration state')
