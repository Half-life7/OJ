#!/usr/bin/env bash
set -Eeuo pipefail
# Run as root in deploy/test/Dockerfile.cli with /source read-only and a Docker
# socket for stat only. All daemon-changing commands are mocked; Compose config
# is evaluated by the real CLI. No service or existing data is changed.
scratch=$(mktemp -d /tmp/biue-network-check.XXXXXXXX)
trap 'status=$?; for log in "$scratch"/*.log; do [[ ! -f $log ]] || tail -n 25 "$log"; done; exit "$status"' ERR
export OJ_TEST_STATE="$scratch/state" REAL_DOCKER
REAL_DOCKER=$(command -v docker)
mkdir -p "$scratch/bin" "$OJ_TEST_STATE"
cat > "$scratch/bin/docker" <<'PY'
#!/usr/bin/env python3
import hashlib, json, os
from pathlib import Path
import subprocess, sys
args = sys.argv[1:]
state = Path(os.environ['OJ_TEST_STATE'])
with (state/'commands.jsonl').open('a') as f:
    f.write(json.dumps(args)+'\n')
if args[:2] == ['compose', 'version']:
    print('2.40.3'); sys.exit(0)
if args[0] == 'info': sys.exit(0)
if args[0] == 'context':
    print('unix:///var/run/docker.sock' if args[1] == 'inspect' else 'default'); sys.exit(0)
if args[:2] == ['image', 'inspect']:
    image = args[2]
    if image == os.getenv('OJ_TEST_MISSING_RUNTIME'): sys.exit(1)
    if image.startswith('biue-') or os.getenv('OJ_TEST_LOCAL') == '1': sys.exit(0)
    sys.exit(0 if (state/hashlib.sha256(image.encode()).hexdigest()).exists() else 1)
if args[0] == 'pull':
    if os.getenv('OJ_TEST_NO_NETWORK') == '1': sys.exit('unexpected network access')
    image = args[1]
    if os.getenv('OJ_TEST_PULL_FAIL') == '1': sys.exit('simulated registry timeout')
    if os.getenv('OJ_TEST_RETRY') == '1' and not (state/'retried').exists():
        (state/'retried').touch(); sys.exit('simulated temporary registry failure')
    (state/hashlib.sha256(image.encode()).hexdigest()).touch(); sys.exit(0)
if args[:2] == ['image', 'save']:
    Path(args[args.index('--output')+1]).write_bytes(b'fake-image-archive'); sys.exit(0)
if args[0] == 'inspect': print('healthy'); sys.exit(0)
if args[0] != 'compose': sys.exit('unexpected Docker command')
operation = next((v for v in args[1:] if v in ('config', 'build', 'ps', 'up', 'run', 'stop', 'exec', 'down')), None)
if operation == 'config':
    result = subprocess.run([os.environ['REAL_DOCKER'], *args])
    sys.exit(result.returncode)
if operation == 'build':
    if os.getenv('OJ_TEST_NO_NETWORK') == '1': sys.exit('unexpected build')
    sys.exit(0)
if operation == 'ps': print('fixture-container'); sys.exit(0)
if operation == 'up' and not ('--no-build' in args and '--pull' in args and 'never' in args):
    sys.exit('up permits implicit build/pull')
if operation == 'run' and not ('--pull' in args and 'never' in args):
    sys.exit('run permits implicit pull')
if operation in ('up', 'run', 'stop', 'exec', 'down'): sys.exit(0)
sys.exit('unexpected Compose operation')
PY
chmod +x "$scratch/bin/docker"
export PATH="$scratch/bin:$PATH" OJ_PROJECT=biue-network-fixture OJ_DATA_DIR="$scratch/data"
export OJ_ADMIN_PASSWORD=fixture-only-password OJ_MIN_DISK_KB=1 OJ_MIN_MEMORY_KB=1 OJ_AUTO_INSTALL_DOCKER=0
export OJ_NETWORK_PROFILE=cn OJ_PULL_RETRIES=2 OJ_PULL_TIMEOUT=5 OJ_TEST_RETRY=1
# Ambient values must not override the explicitly saved source selection.
export DOCKERHUB_LIBRARY=unreachable.example.edu/library GO_PROXY=https://unreachable.example.edu
bash /source/deploy.sh install > "$scratch/install.log" 2>&1
grep -q '安装完成' "$scratch/install.log"
env -u DOCKERHUB_LIBRARY -u GO_PROXY docker compose --env-file "$OJ_DATA_DIR/config/.env" -f /source/deploy/docker-compose.yml -f /source/deploy/docker-compose.production.yml config --format json > "$scratch/cn-compose.json"
python3 /source/tools/check_image_sources.py "$scratch/cn-compose.json"
python3 /source/tools/check_2d_compose.py < "$scratch/cn-compose.json"
env_before=$(sha256sum "$OJ_DATA_DIR/config/.env")
unset OJ_NETWORK_PROFILE OJ_TEST_RETRY
export OJ_SKIP_BUILD=1 OJ_TEST_LOCAL=1 OJ_TEST_NO_NETWORK=1
bash /source/deploy.sh install > "$scratch/offline.log" 2>&1
[[ $env_before == "$(sha256sum "$OJ_DATA_DIR/config/.env")" ]]
bash /source/deploy.sh update > "$scratch/update.log" 2>&1
bash /source/deploy.sh export-images "$scratch/offline-images.tar" > "$scratch/export.log" 2>&1
(cd "$scratch" && sha256sum -c offline-images.tar.sha256)
cp "$OJ_TEST_STATE/commands.jsonl" "$scratch/before-missing.jsonl"
export OJ_TEST_MISSING_RUNTIME=biue-lang-javascript:0.1.0
if bash /source/deploy.sh update > "$scratch/missing.log" 2>&1; then echo 'missing image accepted'; exit 1; fi
grep -q '缺少运行镜像：biue-lang-javascript:0.1.0' "$scratch/missing.log"
python3 - "$scratch" <<'PY'
import json, sys
from pathlib import Path
root = Path(sys.argv[1])
commands = [json.loads(v) for v in (root/'state/commands.jsonl').read_text().splitlines()]
pulls = [v[1] for v in commands if v[0] == 'pull']
assert len(pulls) == 10, pulls  # Nine sources plus one transient failure.
assert all(v.startswith('m.daocloud.io/docker.io/library/') for v in pulls), pulls
assert sum('build' in v for v in commands) == 1, 'offline install/update built images'
before = len((root/'before-missing.jsonl').read_text().splitlines())
assert not any('stop' in v for v in commands[before:]), 'missing image stopped the live deployment'
PY
unset OJ_TEST_MISSING_RUNTIME OJ_SKIP_BUILD OJ_TEST_LOCAL OJ_TEST_NO_NETWORK
export OJ_TEST_PULL_FAIL=1 OJ_DOCKERHUB_LIBRARY=unreachable.example.edu/library OJ_PULL_RETRIES=1
if bash /source/deploy.sh update > "$scratch/failed-pull.log" 2>&1; then echo 'failed pull accepted'; exit 1; fi
grep -q '镜像拉取失败：unreachable.example.edu/library/' "$scratch/failed-pull.log"
printf 'Resolved Compose sources, bounded retry, offline install/update/export, missing-image fail-before-stop and failure diagnostics passed. Fixtures: %s\n' "$scratch"
