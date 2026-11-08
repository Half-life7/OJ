# Biue OJ

面向编程练习与校内竞赛的在线评测系统，采用 Go 模块化单体架构。本次交付是**工程基础与账号认证**，完整需求见 [plan.md](plan.md)，分批计划见 [实施计划](docs/IMPLEMENTATION_PLAN.md)，实际改动与验证记录见 [修订日志](CHANGELOG.md)。

## 当前功能

- 永久账号开放注册，账号 ID 独立于可选学号；身份档案保存唯一学号及审核状态。
- 登录、退出、修改密码、查看个人资料；持久化会话撤销、改密后旧会话失效、Cookie、CSRF 与 Redis 限流。
- 中文响应式首页、注册、登录、个人中心，使用 Go Template、原生 CSS/JavaScript，无前端构建步骤。
- 版本化数据库迁移、超级管理员初始化、结构化日志、`/health` 与 `/ready`。
- 开发用 Docker Compose：MySQL、Redis、一次性迁移、Web、Nginx；数据库端口不映射宿主机，业务容器无 Docker Socket。
- 独立的测试数据 ZIP32 校验库：输入输出配对、分值、路径与资源限制、CRC 和稳定摘要；尚未接入上传接口或数据库版本管理。

题目、四语言判题、比赛、三种计分、题单、讨论、题解和三个全局榜仍在后续批次。完整生产一键安装、升级、备份恢复与 Linux 部署验收也尚未交付。

## 开发环境

- Go 1.25.4 或兼容工具链（模块声明 Go 1.25）。直接和间接依赖版本固定在 `go.mod`，校验记录保存在 `go.sum`；首次运行需要下载这些依赖。
- 可连接的 MySQL 8 数据库与 Redis；本机直接运行时需提前创建数据库和专用用户。
- 容器方式需要可运行 Linux 容器的 Docker Engine 与 Docker Compose v2。
- Go 服务**不会自动读取 `.env`**。本机运行需显式设置环境变量；Compose 通过 `--env-file` 与服务环境配置传入。

## 本机直接运行

以下示例假定 MySQL/Redis 已在本机监听，并已创建 `biue` 数据库和 `biue` 数据库用户。请使用实际的服务密码。

PowerShell 设置环境变量：

```powershell
function New-RandomSecret {
    $buffer = New-Object byte[] 32
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $generator.GetBytes($buffer); [Convert]::ToBase64String($buffer) }
    finally { $generator.Dispose() }
}
function Read-SecretValue([string] $Prompt) {
    $value = Read-Host $Prompt -AsSecureString
    $credential = New-Object System.Management.Automation.PSCredential('config', $value)
    $credential.GetNetworkCredential().Password
}
$env:APP_ENV = 'development'
$env:APP_PORT = '8080'
$env:MYSQL_HOST = '127.0.0.1'
$env:MYSQL_PORT = '3306'
$env:MYSQL_DATABASE = 'biue'
$env:MYSQL_USER = 'biue'
$env:MYSQL_PASSWORD = Read-SecretValue 'MySQL 专用用户密码'
$env:REDIS_ADDR = '127.0.0.1:6379'
$env:REDIS_PASSWORD = Read-SecretValue 'Redis 密码'
$env:JWT_SECRET = New-RandomSecret
$env:CSRF_SECRET = New-RandomSecret
$env:APP_ENCRYPTION_KEY = New-RandomSecret
$env:PUBLIC_URL = 'http://localhost:8080'
$env:TIMEZONE = 'Asia/Shanghai'
$env:TRUSTED_PROXY_CIDRS = ''
go mod download
go run ./cmd/ojctl migrate up
go run ./cmd/ojctl migrate version
go run ./cmd/web
```

Linux/macOS shell 对应设置：

```bash
export APP_ENV=development APP_PORT=8080
export MYSQL_HOST=127.0.0.1 MYSQL_PORT=3306 MYSQL_DATABASE=biue MYSQL_USER=biue
read -r -s -p 'MySQL password: ' MYSQL_PASSWORD; echo; export MYSQL_PASSWORD
export REDIS_ADDR=127.0.0.1:6379
read -r -s -p 'Redis password: ' REDIS_PASSWORD; echo; export REDIS_PASSWORD
export JWT_SECRET="$(openssl rand -base64 32)"
export CSRF_SECRET="$(openssl rand -base64 32)"
export APP_ENCRYPTION_KEY="$(openssl rand -base64 32)"
export PUBLIC_URL=http://localhost:8080 TIMEZONE=Asia/Shanghai TRUSTED_PROXY_CIDRS=
go mod download
go run ./cmd/ojctl migrate up
go run ./cmd/ojctl migrate version
go run ./cmd/web
```

打开 <http://localhost:8080>。每次启动须使用相同的密钥；以上随机生成命令用于首次配置，后续应从自己的安全配置加载既有值。更换 JWT 密钥会使原有登录失效。

初始化超级管理员：在相同配置的另一终端运行以下命令，密码不通过命令参数传入。

```powershell
$adminPassword = Read-Host '初始管理员密码' -AsSecureString
$adminCredential = New-Object System.Management.Automation.PSCredential('admin', $adminPassword)
$env:OJ_ADMIN_PASSWORD = $adminCredential.GetNetworkCredential().Password
try { go run ./cmd/ojctl admin-init --username admin }
finally { Remove-Item Env:OJ_ADMIN_PASSWORD -ErrorAction SilentlyContinue }
```

```bash
read -r -s -p 'Initial admin password: ' OJ_ADMIN_PASSWORD; echo; export OJ_ADMIN_PASSWORD
go run ./cmd/ojctl admin-init --username admin
unset OJ_ADMIN_PASSWORD
```

重复初始化不会重置已有管理员密码，也不会将同名普通账号自动提权。若命令失败，请查看脱敏错误信息，不要通过删库解决。

## 开发 Compose

在项目根目录执行。先复制配置文件，再填写所有空白密码和密钥：

```powershell
Copy-Item ./deploy/.env.example ./deploy/.env
# 用上面的 New-RandomSecret 为每个空白密码/密钥独立生成一个值，写入 deploy/.env。
```

```bash
cp deploy/.env.example deploy/.env
chmod 600 deploy/.env
# 用 openssl rand -base64 32 为每个空白密码/密钥独立生成一个值，写入 deploy/.env。
```

配置完成后执行：

```text
docker compose --env-file deploy/.env -f deploy/docker-compose.yml config --quiet
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up --build -d
docker compose --env-file deploy/.env -f deploy/docker-compose.yml ps -a
docker compose --env-file deploy/.env -f deploy/docker-compose.yml logs --tail 100 web migrate
```

访问地址为 <http://localhost:8080>。首次启动先等待数据库和 Redis 就绪，再执行一次性迁移，迁移成功后启动 Web 与 Nginx；`migrate` 正常结束时状态为 `Exited (0)`。没有自动创建默认管理员，使用安全输入设置 `OJ_ADMIN_PASSWORD` 后执行：

```text
docker compose --env-file deploy/.env -f deploy/docker-compose.yml run --rm -e OJ_ADMIN_PASSWORD migrate admin-init --username admin
```

用完后移除终端中的 `OJ_ADMIN_PASSWORD`。Compose 持久化使用 `mysql-data` 和 `redis-data` 命名卷。停止服务使用 `docker compose --env-file deploy/.env -f deploy/docker-compose.yml down`，此命令保留数据；不要添加 `--volumes`，它会删除数据卷。

开发配置只绑定宿主机 `127.0.0.1`。修改访问端口时同步修改 `PUBLIC_HTTP_PORT` 和 `PUBLIC_URL`。容器内部 Web 固定使用 8080。`edge` 网络使用 `172.30.0.0/24`，Nginx 固定为 `172.30.0.10`，应用仅信任该 `/32` 代理；若本机已有同名网段，需同时修改 Compose 网段、Nginx 地址和 `TRUSTED_PROXY_CIDRS`。直接运行 Web 且无反向代理时，该变量留空。

本批 Nginx 提供本机 HTTP 入口。HTTPS 生产入口、证书维护、完整安装升级脚本、备份恢复将在部署批次补齐；`PUBLIC_URL` 应与实际访问的协议、主机和端口一致，不能仅修改为 HTTPS 而未配置 TLS 入口。

## 配置与接口

可配置项见 [环境变量示例](deploy/.env.example)。JWT、CSRF 和应用加密密钥各至少 32 字节且必须不同；密码和密钥不得写进代码或提交到版本库。生产环境拒绝明显占位值及弱密码。修改配置后重启对应进程；Compose 环境变量变更后使用 `up -d` 重建受影响容器。

`APP_ENCRYPTION_KEY` 为后续需要加密存储的功能预留，本批不将密码以可逆方式加密。密码仅保存 bcrypt 哈希。默认会话时长为 12 小时，无 refresh token 或密码找回流程。学号为空时不影响注册，填写后显示待审核；学号审核与身份纠错后台在后续批次交付。

认证接口、请求与响应见 [API 文档](docs/API.md)；建表约束与迁移说明见 [数据库文档](docs/DATABASE.md)。

测试数据解析器的格式、限制和调用方法见 [测试数据说明](docs/TEST_DATA.md)。它只返回已校验的内存数据，不写入文件、不执行程序，也不分配 `data_version`；题目上传、不可变版本持久化和比赛版本快照将在后续批次实现。

| 路径 | 含义 |
| --- | --- |
| `/health` | Web 进程存活，不代表业务依赖可用 |
| `/ready` | 检查 MySQL、Redis、迁移版本及 dirty 状态，失败返回 503 |
| `/register`、`/login`、`/me` | 注册、登录与个人中心 |

依赖故障时健康接口仍可查询，相关业务返回 503；未实现的 Worker 和 Runner 不列为健康组件。

## 验证与当前限制

```text
go test ./...
go vet ./...
go build ./...
go test -race ./...
node --check webui/static/app.js
```

联网环境先执行 `go mod download`，再运行上述全量检查；`go mod verify` 可核对本地模块缓存。`-race` 在 Windows 需要可用的 C 编译器。页面本身不依赖 Node，最后一条仅用于 JavaScript 语法检查。HTTP、sqlmock、miniredis 测试验证认证行为、事务边界与限流，不能代替真实 MySQL 唯一约束、迁移和 Redis 故障恢复验收。

首次开发遇到的 Go 依赖下载问题已通过正常授权的 HTTPS GET 路径解决，模块包与官方校验记录已锁定。首批全量单元测试、静态检查、构建及竞态检查已通过，也已验证真实 Web 进程在数据库和 Redis 不可用时仍可提供页面、CSRF 与健康检查。具体结果以 [CHANGELOG.md](CHANGELOG.md) 为准。

本机 Docker 引擎仍不可连接；真实数据库迁移、容器构建启动、浏览器联调、Linux/amd64 完整部署和 arm64 启动仍须另行验证。交叉编译不能替代对应平台运行验收。

后续开发按实施计划推进，每批同步修订日志，保留原始需求及历史数据。
