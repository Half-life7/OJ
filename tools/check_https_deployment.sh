#!/usr/bin/env bash
set -Eeuo pipefail
[[ ${OJ_PROJECT:-} == biue-deploy5-* ]] || exit 1
export OJ_DATA_DIR="${OJ_DATA_DIR%/production}/recovery"
export OJ_PROJECT="$OJ_PROJECT-recovery"
SOURCE="${OJ_DATA_DIR%/recovery}/code"
cp /source/deploy.sh "$SOURCE/deploy.sh"
cp /source/deploy/maintenance.py "$SOURCE/deploy/maintenance.py"
openssl req -x509 -newkey rsa:2048 -nodes -days 2 -subj /CN=127.0.0.1 -addext subjectAltName=IP:127.0.0.1 -keyout "$OJ_DATA_DIR/probe-public.key" -out "$OJ_DATA_DIR/probe-public.pem" >/dev/null 2>&1
python3 "$SOURCE/deploy/maintenance.py" public-url "$OJ_DATA_DIR/config/.env" https://127.0.0.1:28443
# The initial install used the default HTTPS port; change it only in test data.
python3 - "$OJ_DATA_DIR/config/.env" <<'PY'
import sys
from pathlib import Path
p=Path(sys.argv[1]);p.write_text(p.read_text().replace('PUBLIC_HTTPS_PORT=443\n','PUBLIC_HTTPS_PORT=28443\n'))
PY
export OJ_TLS_CERT="$OJ_DATA_DIR/probe-public.pem" OJ_TLS_KEY="$OJ_DATA_DIR/probe-public.key"
bash "$SOURCE/deploy.sh" install
touch "$OJ_DATA_DIR/https-ready"
while [[ ! -f $OJ_DATA_DIR/https-finish ]]; do sleep 2; done
printf 'DEPLOYMENT_HTTPS_PASSED\n'
