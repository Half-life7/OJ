#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

SOURCE_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
DATA_DIR=${OJ_DATA_DIR:-/var/lib/biue}
PROJECT=${OJ_PROJECT:-biue}
COMMAND=${1:-help}
ARCHIVE=${2:-}
HELPER="$SOURCE_DIR/deploy/maintenance.py"
ENV_FILE="$DATA_DIR/config/.env"
BACKUP_ROOT=${OJ_BACKUP_DIR:-$DATA_DIR/backups}
LAST_BACKUP=
COMPOSE=()
on_error() { local status=$?; printf '操作失败（%s），业务可能保持停止。检查 %s/logs；恢复前备份：%s\n' "$status" "$DATA_DIR" "${LAST_BACKUP:-无}" >&2; exit "$status"; }
trap on_error ERR
help() { printf '用法：sudo bash deploy.sh install|status|logs|stop|start|restart|update|backup|prune|restore BACKUP_DIRECTORY|uninstall\n配置：OJ_DATA_DIR、OJ_PROJECT、OJ_PUBLIC_URL、OJ_HTTP_PORT、OJ_HTTPS_PORT、OJ_PUBLIC_BIND、OJ_TLS_CERT、OJ_TLS_KEY、OJ_BACKUP_DIR。\ninstall 默认自动安装缺失的 Docker/Compose，可用 OJ_AUTO_INSTALL_DOCKER=0 禁止；已有配置不覆盖。卸载保留全部数据。\n'; }
[[ $COMMAND != help ]] || { help; exit 0; }
[[ $EUID == 0 ]] || { echo '请使用 sudo/root 执行。' >&2; exit 1; }
[[ $DATA_DIR =~ ^/[A-Za-z0-9_./-]+$ && $DATA_DIR != / && $DATA_DIR != /var && $DATA_DIR != /var/lib && $DATA_DIR != /tmp && $DATA_DIR != *'/../'* && $DATA_DIR != */.. ]] || { echo '数据目录必须为独立的绝对路径。' >&2; exit 1; }
[[ ! -L $DATA_DIR ]] || { echo '数据根目录不能是符号链接。' >&2; exit 1; }
DATA_DIR=$(realpath -m "$DATA_DIR")
case "$DATA_DIR" in /|/var|/var/lib|/etc|/tmp|/home|/root|/usr|/opt|/srv|/run|/bin|/sbin|/lib|/lib64) echo '拒绝使用系统目录作为数据根目录。' >&2; exit 1;; esac
case "$SOURCE_DIR/" in "$DATA_DIR/"*) echo '数据根目录不能包含程序源码目录。' >&2; exit 1;; esac
case "$DATA_DIR/" in "$SOURCE_DIR/"*) echo '数据必须保存在源码目录之外。' >&2; exit 1;; esac
BACKUP_ROOT=${OJ_BACKUP_DIR:-$DATA_DIR/backups}
BACKUP_ROOT=$(realpath -m "$BACKUP_ROOT")
case "$BACKUP_ROOT" in /|/var|/var/lib|/etc|/tmp|/home|/root|/usr|/opt|/srv|/run|/bin|/sbin|/lib|/lib64) echo '备份目录不安全。' >&2; exit 1;; esac
case "$BACKUP_ROOT/" in "$SOURCE_DIR/"*) echo '备份必须与源码分离，避免进入镜像构建上下文。' >&2; exit 1;; esac
for dir in config mysql redis problem-data runner-data uploads logs; do
  case "$BACKUP_ROOT/" in "$DATA_DIR/$dir/"*) echo '备份目录不能位于业务或配置归档目录内。' >&2; exit 1;; esac
done
for dir in config logs mysql redis problem-data runner-data uploads; do [[ ! -L $DATA_DIR/$dir ]] || { echo '持久化子目录不能是符号链接。'; exit 1; }; done
[[ $PROJECT =~ ^[a-z0-9][a-z0-9_-]{0,49}$ ]] || { echo '项目名不合法。' >&2; exit 1; }
[[ ! -L $DATA_DIR ]] || { echo '数据根目录不能是符号链接。' >&2; exit 1; }
install -d -m 700 "$DATA_DIR" "$DATA_DIR/config" "$DATA_DIR/logs" "$BACKUP_ROOT"
DATA_DIR=$(realpath -e "$DATA_DIR")
ENV_FILE="$DATA_DIR/config/.env"
exec 9>"$DATA_DIR/.maintenance.lock"
flock -n 9 || { echo '另一个维护操作正在进行。' >&2; exit 1; }
exec > >(tee -a "$DATA_DIR/logs/deploy-$(date -u +%Y%m%d).log") 2>&1
printf '%s command=%s project=%s\n' "$(date -u +%FT%TZ)" "$COMMAND" "$PROJECT"

install_docker() {
  [[ ${OJ_AUTO_INSTALL_DOCKER:-0} == 1 ]] || { echo 'Docker/Compose 未安装；设置 OJ_AUTO_INSTALL_DOCKER=1 后重试可从官方 APT 仓库自动安装。'; return 1; }
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl python3 openssl
  install -d -m 0755 /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/docker.asc
  chmod 0644 /etc/apt/keyrings/docker.asc
  printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' "$(dpkg --print-architecture)" "$ID" "${UBUNTU_CODENAME:-$VERSION_CODENAME}" > /etc/apt/sources.list.d/biue-docker.list
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}
# os-release is system-owned metadata, never an application env file.
source /etc/os-release
[[ $ID == ubuntu || $ID == debian ]] || { echo '自动部署目前支持 Ubuntu 与 Debian。'; exit 1; }
if [[ $COMMAND == install ]]; then OJ_AUTO_INSTALL_DOCKER=${OJ_AUTO_INSTALL_DOCKER:-1}; fi
if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then install_docker; fi
if [[ $COMMAND == install ]] && { ! command -v python3 >/dev/null || ! command -v openssl >/dev/null; }; then
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y python3 openssl ca-certificates curl coreutils util-linux gzip tar
fi
for binary in python3 openssl gzip tar sha256sum flock; do command -v "$binary" >/dev/null || { echo "缺少依赖 $binary；请安装 python3 openssl coreutils util-linux gzip tar。"; exit 1; }; done
if ! docker info >/dev/null 2>&1; then
  command -v systemctl >/dev/null && systemctl enable --now docker
  docker info >/dev/null
fi
[[ -z ${DOCKER_HOST:-} || $DOCKER_HOST == unix:///var/run/docker.sock ]] || { echo '部署脚本只支持本机 Docker Socket。'; exit 1; }
if [[ -z ${DOCKER_HOST:-} ]]; then
  endpoint=$(docker context inspect --format '{{.Endpoints.docker.Host}}' "$(docker context show)")
  [[ $endpoint == unix:///var/run/docker.sock ]] || { echo '当前 Docker context 不是本机 Socket。'; exit 1; }
fi
compose_version=$(docker compose version --short | sed 's/^v//')
[[ $(printf '2.24.4\n%s\n' "$compose_version" | sort -V | head -n1) == 2.24.4 ]] || { echo '需要 Docker Compose >=2.24.4。'; exit 1; }
get() { python3 "$HELPER" get "$ENV_FILE" "$1"; }
configure_compose() {
  python3 "$HELPER" validate "$ENV_FILE"
  # The saved configuration is authoritative. An operator's ambient variables
  # must not silently replace persisted database credentials or public ports.
  local clean_env=(env) key
  while IFS= read -r key; do clean_env+=(-u "$key"); done < <(python3 "$HELPER" keys "$ENV_FILE")
  COMPOSE=("${clean_env[@]}" docker compose --env-file "$ENV_FILE" -p "$PROJECT" -f "$SOURCE_DIR/deploy/docker-compose.yml" -f "$SOURCE_DIR/deploy/docker-compose.production.yml")
  if [[ $(get PUBLIC_URL) == https://* ]]; then COMPOSE+=(-f "$SOURCE_DIR/deploy/docker-compose.https.yml"); fi
  "${COMPOSE[@]}" config --quiet
}
dc() { "${COMPOSE[@]}" "$@"; }
wait_ready() {
  local services=(mysql redis web dispatcher worker runner nginx) started=$SECONDS
  while (( SECONDS-started < ${OJ_HEALTH_TIMEOUT:-300} )); do
    local ready=1 service id state
    for service in "${services[@]}"; do
      id=$(dc ps -q "$service")
      if [[ -z $id ]]; then ready=0; break; fi
      state=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$id")
      [[ $state == healthy ]] || { ready=0; break; }
    done
    if ((ready)); then return 0; fi
    sleep 2
  done
  dc ps -a
  echo '服务健康检查超时。'; return 1
}
up() { dc up -d mysql redis; dc run --rm migrate migrate up; dc up -d nginx web dispatcher worker; wait_ready; }
prepare_files() {
  local dir
  for dir in problem-data runner-data uploads; do install -d -m 0750 -o 10001 -g 10001 "$DATA_DIR/$dir"; done
  install -d -m 0750 "$DATA_DIR/mysql" "$DATA_DIR/redis" "$DATA_DIR/config/tls"
  install -d -m 0755 "$DATA_DIR/config/public-tls"
  if [[ ! -f $DATA_DIR/config/tls/ca.pem ]]; then
    openssl req -x509 -newkey rsa:3072 -nodes -days 3650 -subj /CN=Biue-Internal-CA -keyout "$DATA_DIR/config/tls/ca.key" -out "$DATA_DIR/config/tls/ca.pem" >/dev/null 2>&1
    openssl req -newkey rsa:3072 -nodes -subj /CN=runner -keyout "$DATA_DIR/config/tls/server.key" -out "$DATA_DIR/config/tls/server.csr" >/dev/null 2>&1
    printf 'subjectAltName=DNS:runner,DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' > "$DATA_DIR/config/tls/server.ext"
    openssl x509 -req -in "$DATA_DIR/config/tls/server.csr" -CA "$DATA_DIR/config/tls/ca.pem" -CAkey "$DATA_DIR/config/tls/ca.key" -CAcreateserial -days 825 -extfile "$DATA_DIR/config/tls/server.ext" -out "$DATA_DIR/config/tls/server.pem" >/dev/null 2>&1
  fi
  chmod 0644 "$DATA_DIR/config/tls/ca.pem" "$DATA_DIR/config/tls/server.pem"
  chown root:10001 "$DATA_DIR/config/tls/server.key"; chmod 0640 "$DATA_DIR/config/tls/server.key"
  chmod 0600 "$DATA_DIR/config/tls/ca.key"
  if [[ $(get PUBLIC_URL) == https://* ]]; then
    if [[ -n ${OJ_TLS_CERT:-} && -n ${OJ_TLS_KEY:-} ]]; then
      install -m 0644 "$OJ_TLS_CERT" "$DATA_DIR/config/public-tls/server.pem"
      install -m 0640 -g 101 "$OJ_TLS_KEY" "$DATA_DIR/config/public-tls/server.key"
    fi
    [[ -f $DATA_DIR/config/public-tls/server.pem && -f $DATA_DIR/config/public-tls/server.key ]] || { echo 'HTTPS 需要 OJ_TLS_CERT/OJ_TLS_KEY（或已保存的证书）。'; return 1; }
    chown root:101 "$DATA_DIR/config/public-tls/server.key"; chmod 0640 "$DATA_DIR/config/public-tls/server.key"
    openssl x509 -in "$DATA_DIR/config/public-tls/server.pem" -noout >/dev/null
    openssl pkey -in "$DATA_DIR/config/public-tls/server.key" -noout >/dev/null
    cert_public=$(openssl x509 -in "$DATA_DIR/config/public-tls/server.pem" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum)
    key_public=$(openssl pkey -in "$DATA_DIR/config/public-tls/server.key" -pubout -outform DER | sha256sum)
    [[ $cert_public == "$key_public" ]] || { echo 'HTTPS 证书与私钥不匹配。'; return 1; }
  fi
  python3 "$HELPER" nginx "$ENV_FILE" "$SOURCE_DIR/deploy/nginx/nginx.conf" "$DATA_DIR/config/nginx.conf"
  chmod 0644 "$DATA_DIR/config/nginx.conf"
  dc run --rm --no-deps --user 0 --cap-add CHOWN --entrypoint sh redis -c 'chown redis:redis /data'
}
quiesce() { dc stop -t 60 nginx web worker dispatcher runner; }
backup() {
  local resume=${1:-yes} stamp destination temporary
  stamp=$(date -u +%Y%m%dT%H%M%SZ)-$(openssl rand -hex 4)
  destination="$BACKUP_ROOT/$stamp"; temporary="$BACKUP_ROOT/.$stamp.pending"
  install -d -m 0700 "$temporary"
  quiesce
  dc exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_PASSWORD" exec mysqldump --user="$MYSQL_USER" --single-transaction --quick --triggers --no-tablespaces --set-gtid-purged=OFF "$MYSQL_DATABASE"' | gzip -n > "$temporary/database.sql.gz"
  tar -czf "$temporary/files.tar.gz" -C "$DATA_DIR" config problem-data runner-data uploads
  (cd "$temporary" && sha256sum database.sql.gz files.tar.gz > SHA256SUMS)
  gzip -t "$temporary/database.sql.gz" "$temporary/files.tar.gz"
  python3 "$HELPER" verify "$temporary"
  mv -- "$temporary" "$destination"; LAST_BACKUP=$destination
  printf '备份完成：%s\n' "$destination"
  if [[ $resume == yes ]]; then up; fi
}
case "$COMMAND" in
  install)
    available_kb=$(df -Pk "$DATA_DIR" | awk 'NR==2 {print $4}')
    (( available_kb >= ${OJ_MIN_DISK_KB:-5242880} )) || { echo '可用磁盘不足（至少 5 GiB）。'; exit 1; }
    memory_kb=$(awk '/MemTotal/ {print $2}' /proc/meminfo)
    (( memory_kb >= ${OJ_MIN_MEMORY_KB:-2097152} )) || { echo '可用内存不足（至少 2 GiB）。'; exit 1; }
    python3 "$HELPER" generate "$ENV_FILE" "$DATA_DIR" "$(stat -c '%g' /var/run/docker.sock)"
    configure_compose
    # Docker checks host binding atomically when creating the ingress service.
    # Report existing foreign port listeners before expensive image builds.
    if [[ -z $(dc ps -q nginx) ]] && command -v ss >/dev/null; then
      for port in "$(get PUBLIC_HTTP_PORT)"; do
        [[ -z $(ss -Hln sport = ":$port") ]] || { echo "入口端口 $port 已被占用。"; exit 1; }
      done
    fi
    dc build
    prepare_files
    dc up -d mysql redis
    dc run --rm migrate migrate up
    if [[ ! -f $DATA_DIR/config/admin-initialized ]]; then
      if [[ -z ${OJ_ADMIN_PASSWORD:-} ]]; then
        [[ -t 0 ]] || { echo '非交互安装请通过 OJ_ADMIN_PASSWORD 环境变量提供初始密码。'; exit 1; }
        read -rs -p '设置初始管理员密码（不回显）：' OJ_ADMIN_PASSWORD; printf '\n'
      fi
      export OJ_ADMIN_PASSWORD
      dc run --rm -e OJ_ADMIN_PASSWORD migrate admin-init --username "${OJ_ADMIN_USERNAME:-admin}"
      unset OJ_ADMIN_PASSWORD
      touch "$DATA_DIR/config/admin-initialized"
    fi
    up
    printf '安装完成：%s\n账号：%s（密码已通过安全输入设置）\n后续：sudo bash deploy.sh status / backup / update\n' "$(get PUBLIC_URL)" "${OJ_ADMIN_USERNAME:-admin}"
    ;;
  status|logs|stop|start|restart|update|backup|restore|uninstall|prune)
    [[ -f $ENV_FILE ]] || { echo '尚未安装，请先运行 install。'; exit 1; }
    configure_compose
    case "$COMMAND" in
      status) dc ps -a ;;
      logs) dc logs --tail 200 ;;
      stop) dc stop -t 60 ;;
      start) up ;;
      restart) quiesce; up ;;
      update) backup no; dc build; up ;;
      backup) backup yes ;;
      prune) python3 "$HELPER" prune "$BACKUP_ROOT" "${OJ_BACKUP_KEEP:-7}" ;;
      uninstall) dc down --remove-orphans; echo '已卸载容器和网络，全部数据、配置和备份均保留。' ;;
      restore)
        [[ -n $ARCHIVE ]] || { echo 'restore 需要备份目录参数。'; exit 1; }
        ARCHIVE=$(realpath -e "$ARCHIVE")
        [[ ! -L $ARCHIVE ]] || exit 1
        for dir in config problem-data runner-data uploads; do
          case "$ARCHIVE/" in "$DATA_DIR/$dir/"*) echo '恢复源不能位于本次将被替换的目录中。'; exit 1;; esac
        done
        python3 "$HELPER" verify "$ARCHIVE"
        gzip -t "$ARCHIVE/database.sql.gz" "$ARCHIVE/files.tar.gz"
        backup no
        saved="$DATA_DIR/pre-restore-$(date -u +%Y%m%dT%H%M%SZ)-$(openssl rand -hex 4)"
        install -d -m 0700 "$saved"
        for dir in config problem-data runner-data uploads; do [[ ! -L $DATA_DIR/$dir ]] || exit 1; mv -- "$DATA_DIR/$dir" "$saved/$dir"; done
        tar --extract --gzip --file "$ARCHIVE/files.tar.gz" --directory "$DATA_DIR" --no-same-owner --no-same-permissions
        python3 "$HELPER" relocate "$ENV_FILE" "$DATA_DIR" "$(stat -c '%g' /var/run/docker.sock)"
        # The existing container's credentials open one root connection. Both
        # account changes and the dump execute within that same connection.
        { python3 "$HELPER" restore-sql "$ENV_FILE"; printf 'USE `%s`;\n' "$(get MYSQL_DATABASE)"; gzip -dc "$ARCHIVE/database.sql.gz"; printf '\nUPDATE users SET token_version=token_version+1; UPDATE auth_sessions SET revoked_at=UTC_TIMESTAMP(6) WHERE revoked_at IS NULL; INSERT INTO audit_logs(actor_user_id,action,object_type,object_id,request_id,details,created_at) VALUES (NULL,"maintenance.restore","system",0,"","{}",UTC_TIMESTAMP(6));\n'; } | dc exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --user=root'
        dc exec -T redis sh -c 'REDISCLI_AUTH="$REDIS_PASSWORD" exec redis-cli FLUSHDB' >/dev/null
        for dir in problem-data runner-data uploads; do chown -R 10001:10001 "$DATA_DIR/$dir"; chmod 0750 "$DATA_DIR/$dir"; done
        configure_compose
        prepare_files
        up
        printf '恢复完成；访问地址：%s\n恢复前自动备份：%s\n原文件保留：%s\n' "$(get PUBLIC_URL)" "$LAST_BACKUP" "$saved"
        ;;
    esac
    ;;
  *) help; exit 1 ;;
esac
