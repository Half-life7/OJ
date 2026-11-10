# Biue OJ

实际使用反馈与后续工作见[后续优化与用户验收清单](后续优化.md)。以提交 `c83618c` 为当前优化基线；后续每次实现记录在[变更日志](CHANGELOG.md)，由用户评判是否通过，agent 不自行删除优化条目。

OPT-001～OPT-005 已实现并通过本机独立浏览器验证，等待用户验收：登录导航、题库创建入口、比赛创建校验、持续时间推算结束时间、上传禁用说明及快捷保存。详细步骤和验证边界见清单末尾实施记录；取得本次源码后通过 `sudo bash deploy.sh update` 重新构建部署。

最新进度：**批次 6 已完成，原生整机验收继续单列待验**，当前迁移版本 **9**。新增提交状态 SSE、隔离 Python Special Judge、原子幂等批量重判、三榜周/月统计、输出型题目和固定版本 Node.js 22 JavaScript。真实数据库/沙箱和 Edge 桌面/手机验收通过，应用及新增 Node 的 arm64 镜像构建/仿真启动通过，详见 [P2 扩展说明](docs/EXTENSIONS.md)。此前六个批次的功能均已落地，原生 Ubuntu、真实重启/跨机和原生 arm64 整机验收按用户选择保留待验。

面向编程练习与校内竞赛的在线评测系统，采用 Go 模块化单体架构。现已实现工程基础、账号认证、题库、不可变数据版本与提交任务基础，完整需求见 [plan.md](plan.md)，分批计划见 [实施计划](docs/IMPLEMENTATION_PLAN.md)，实际改动与验证记录见 [修订日志](CHANGELOG.md)。

Web 框架使用 Gin v1.12.0，HTTP 服务底层基于 Go net/http，API 路由、中间件和参数绑定统一使用 Gin。四语言镜像已实际构建，amd64 全链路及故障恢复通过；arm64 已交叉编译并通过仿真启动，原生 arm64/生产整机验收留在后续部署批次。

大陆服务器安装可用 `sudo OJ_NETWORK_PROFILE=cn OJ_PUBLIC_URL=http://你的服务器IP bash deploy.sh install`，同步切换 Docker/Go/Alpine 下载源；也支持自定义镜像仓库、完整镜像包导出和离线安装。已有实例使用 `sudo OJ_NETWORK_PROFILE=cn bash deploy.sh update` 切换，详见[部署与运维](docs/OPERATIONS.md#中国大陆网络与镜像拉取失败)。

## 当前功能

- SSE 提交状态、隔离 SPJ、输出型题目、JavaScript 判题、保留历史的批量重判和三个全局榜的周/月维度，配置、权限及限制见 [批次 6](docs/EXTENSIONS.md)。

- 管理后台 `/admin`、CSV 名单预览确认、批量参赛状态、成绩导出、保留历史的封榜/解封；根目录 `deploy.sh` 提供 Linux 安装、更新、备份、恢复和保留数据卸载。

- 学习社区 `/learning`：有序公开/私有题单、收藏、题目/比赛讨论、回复/点赞、Markdown 题解审核；`/rankings` 提供三个可重建全局榜，题目页显示日常通过统计。

- 比赛中心 `/contests`：报名、队伍、临时账号、名单筛选、三种计分、比赛提交、结算；身份审核与显式账号关联 `/admin/identities`。

- 永久账号开放注册，账号 ID 独立于可选学号；身份档案保存唯一学号及审核状态。
- 登录、退出、修改密码、查看个人资料；持久化会话撤销、改密后旧会话失效、Cookie、CSRF 与 Redis 限流。
- 中文响应式首页、注册、登录、个人中心，使用 Go Template、原生 CSS/JavaScript，无前端构建步骤。
- 版本化数据库迁移、超级管理员初始化、结构化日志、`/health` 与 `/ready`。
- 开发用 Docker Compose：MySQL、Redis、一次性迁移、Web、Nginx；数据库端口不映射宿主机，业务容器无 Docker Socket。
- 公开题库与题面，超级管理员创建、编辑、下架、标签/难度/可见性管理，修订号防止并发覆盖。
- ZIP32 安全校验与有界上传、不可变题面/数据版本、分值与摘要、版本元数据历史，以及专用持久卷；隐藏输入输出不通过 HTTP 暴露。
- Run/Submit 分离、版本固定、独立限流与队列容量控制；提交、任务、Outbox 同事务保存。CodeMirror 6 本地四语言编辑器、本人提交记录和状态轮询已接入。Worker 在依赖就绪后领取 Pending 任务并调用认证 Runner，真实结果同事务保存后才确认消息。
- 四语言编译/执行、每点独立沙箱、禁网/只读根目录/CPU/内存/PID/时间/输出限制、执行重试与死信、消息隔离、Redis 数据丢失后的 Outbox 对账及经管理员授权的死信 CLI。

批次 5 的管理和部署功能已交付，原生整机验收按约定待验；批次 6 的 P2 扩展也已交付。

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

用完后移除终端中的 `OJ_ADMIN_PASSWORD`。Compose 持久化使用 `mysql-data`、`redis-data` 和 `problem-data` 命名卷。停止服务使用 `docker compose --env-file deploy/.env -f deploy/docker-compose.yml down`，此命令保留数据；不要添加 `--volumes`，它会删除数据卷。

开发配置只绑定宿主机 `127.0.0.1`。修改访问端口时同步修改 `PUBLIC_HTTP_PORT` 和 `PUBLIC_URL`。容器内部 Web 固定使用 8080。`edge` 网络使用 `172.30.0.0/24`，Nginx 固定为 `172.30.0.10`，应用仅信任该 `/32` 代理；若本机已有同名网段，需同时修改 Compose 网段、Nginx 地址和 `TRUSTED_PROXY_CIDRS`。直接运行 Web 且无反向代理时，该变量留空。

本节开发 Nginx 提供本机 HTTP 入口。生产 HTTP/HTTPS、安装升级和备份恢复使用 [部署与运维](docs/OPERATIONS.md) 中的独立配置；`PUBLIC_URL` 应与实际访问的协议、主机和端口一致，不能仅修改为 HTTPS 而未配置 TLS 入口。

## 配置与接口

可配置项见 [环境变量示例](deploy/.env.example)。JWT、CSRF 和应用加密密钥各至少 32 字节且必须不同；密码和密钥不得写进代码或提交到版本库。生产环境拒绝明显占位值及弱密码。修改配置后重启对应进程；Compose 环境变量变更后使用 `up -d` 重建受影响容器。

`APP_ENCRYPTION_KEY` 为后续需要加密存储的功能预留，本批不将密码以可逆方式加密。密码仅保存 bcrypt 哈希。默认会话时长为 12 小时，无 refresh token 或密码找回流程。学号为空时不影响注册，填写后显示待审核；学号审核与身份纠错已在 `/admin/identities` 交付。

认证接口、请求与响应见 [API 文档](docs/API.md)；建表约束与迁移说明见 [数据库文档](docs/DATABASE.md)。

测试数据解析器的格式、限制和调用方法见 [测试数据说明](docs/TEST_DATA.md)，题目 API、权限、修订号与 ZIP 上传见 [题目管理说明](docs/PROBLEMS.md)。解析器仍只返回内存数据；2B 服务将其发布为不可变版本并事务登记。PROBLEM_DATA_DIR 默认 oj-data/problem-data，Compose 固定挂载 /app/oj-data/problem-data；导入并发默认 2，可由 PROBLEM_IMPORT_CONCURRENCY 调整为 1～8。数据库与数据目录均需备份；已发布孤儿目录不会被自动删除，禁止仅凭提交错误自行删除。比赛版本快照留待比赛模块接入。

| 路径 | 含义 |
| --- | --- |
| `/health` | Web 进程存活，不代表业务依赖可用 |
| `/ready` | 检查 MySQL、Redis、迁移版本及 dirty 状态，失败返回 503 |
| `/register`、`/login`、`/me` | 注册、登录与个人中心 |
| `/problems`、`/problems/{id}` | 公开题库与题面 |
| `/admin/problems` | 超级管理员题目管理、ZIP/成对文件上传、分值表与 SPJ 快捷编辑 |
| `/submissions`、`/submissions/{id}` | 本人提交记录与详情；接口和限制见 [提交说明](docs/SUBMISSIONS.md) |

依赖故障时健康接口仍可查询，相关业务返回 503；Worker、dispatcher 与 Runner 具有各自的进程/依赖健康检查，Web `/ready` 只报告自身数据库/Redis/迁移就绪，不代表整个判题集群就绪。

## 验证与当前限制

```text
go test ./...
go vet ./...
go build ./...
go test -race ./...
node --check webui/static/app.js
node --check webui/static/problems.js
node --check webui/static/contests.js
node --check webui/static/community.js
node --check webui/static/submissions.js
node --check webui/static/editor.js
node --test webui/tests/problems.test.cjs webui/tests/submissions.test.cjs webui/tests/contests.test.cjs webui/tests/community.test.cjs
```

联网环境先执行 `go mod download`，再运行上述全量检查；`go mod verify` 可核对本地模块缓存。`-race` 在 Windows 需要可用的 C 编译器。页面本身不依赖 Node，Node 命令用于脚本语法和页面行为验证。HTTP、sqlmock、miniredis 测试验证认证行为、事务边界与限流，不能代替真实 MySQL 唯一约束、迁移和 Redis 故障恢复验收。

真实页面回归需要独立的 `.cache/2d-acceptance.env`、运行中的验收 Compose 项目和 Playwright；使用当前源码重建 Web/Nginx 后运行 `node tools/check_optimizations_browser.cjs`。该脚本覆盖公开题目、直接文件 replace/merge、分值预览、SPJ 模板、比赛工作区和社区权限状态，不连接用户生产环境。

首次开发遇到的 Go 依赖下载问题已通过正常授权的 HTTPS GET 路径解决，模块包与官方校验记录已锁定。首批全量单元测试、静态检查、构建及竞态检查已通过，也已验证真实 Web 进程在数据库和 Redis 不可用时仍可提供页面、CSRF 与健康检查。具体结果以 [CHANGELOG.md](CHANGELOG.md) 为准。

本机 Docker Desktop 已启动；真实 MySQL/Redis、四语言镜像构建、Linux/amd64 判题及故障恢复通过，arm64 静态程序的仿真启动也已通过。完整生产部署、原生 arm64 整机与长期压力/备份恢复另列后续；交叉编译或仿真启动不能替代原生平台完整验收。

后续开发按实施计划推进，每批同步修订日志，保留原始需求及历史数据。

六个批次及 `后续优化.md` 中的后续可用性事项已实现并进入用户验收；涉及真实公网 Origin、用户服务器升级和最终操作体验仍以用户现场验收为准。
