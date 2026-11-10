#!/usr/bin/env bash
set -Eeuo pipefail
# Executed inside the isolated Debian controller, with a host Docker socket.
# OJ_DATA_DIR must be a dedicated Docker volume mountpoint visible to the daemon.
[[ ${OJ_PROJECT:-} == biue-deploy5-* ]] || exit 1
export OJ_ADMIN_PASSWORD=${BIUE_SMOKE_PASSWORD:?test administrator password required}
export OJ_AUTO_INSTALL_DOCKER=1
if command -v go >/dev/null || command -v mysql >/dev/null || command -v redis-cli >/dev/null; then echo '测试控制器不应安装应用开发/数据库运行时'; exit 1; fi
TEST_SOURCE="${OJ_DATA_DIR%/production}/code"
mkdir -p "$TEST_SOURCE"
tar -C /source --exclude=.cache --exclude=.git --exclude=oj-data --exclude='*/node_modules' --exclude='*/.env' --exclude=.env -cf - . | tar -C "$TEST_SOURCE" -xf -
bash -n "$TEST_SOURCE/deploy.sh"
bash "$TEST_SOURCE/deploy.sh" install
sha256sum "$OJ_DATA_DIR/config/.env" > "$OJ_DATA_DIR/env-before"
bash "$TEST_SOURCE/deploy.sh" install
sha256sum -c "$OJ_DATA_DIR/env-before"
touch "$OJ_DATA_DIR/install-ready"
printf 'DEPLOYMENT_INSTALL_READY\n'
while [[ ! -f $OJ_DATA_DIR/continue ]]; do sleep 2; done
bash "$TEST_SOURCE/deploy.sh" backup
archive=$(find "$OJ_DATA_DIR/backups" -mindepth 1 -maxdepth 1 -type d ! -name '.*' | sort | tail -n1)
printf '%s\n' "$archive" > "$OJ_DATA_DIR/test-backup-path"
bash "$TEST_SOURCE/deploy.sh" update
bash "$TEST_SOURCE/deploy.sh" restart
touch "$OJ_DATA_DIR/update-ready"
while [[ ! -f $OJ_DATA_DIR/restore-continue ]]; do sleep 2; done
bash "$TEST_SOURCE/deploy.sh" restore "$archive"
touch "$OJ_DATA_DIR/restore-ready"
printf 'DEPLOYMENT_RESTORE_READY\n'
while [[ ! -f $OJ_DATA_DIR/finish ]]; do sleep 2; done
bash "$TEST_SOURCE/deploy.sh" uninstall
[[ -f $OJ_DATA_DIR/config/.env && -f $archive/SHA256SUMS ]]
printf 'DEPLOYMENT_UNINSTALL_PRESERVED_DATA\n'
original=$OJ_DATA_DIR
export OJ_DATA_DIR="${original%/production}/recovery"
export OJ_PROJECT="$OJ_PROJECT-recovery"
bash "$TEST_SOURCE/deploy.sh" install
bash "$TEST_SOURCE/deploy.sh" restore "$archive"
touch "$OJ_DATA_DIR/cross-restore-ready"
printf 'DEPLOYMENT_CROSS_INSTANCE_READY\n'
while [[ ! -f $OJ_DATA_DIR/finish ]]; do sleep 2; done
bash "$TEST_SOURCE/deploy.sh" uninstall
printf 'DEPLOYMENT_CROSS_INSTANCE_PRESERVED_DATA\n'
