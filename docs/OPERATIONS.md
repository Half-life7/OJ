# 批次 5：完整管理和部署交付

状态：功能实现与容器验收已完成。原生 Ubuntu 整机验收已按用户选择单列待验。当批引入迁移 **8**；当前构建为 9，新增 Node 镜像由基础 Compose 一并构建。完整需求见 plan.md 第十三、十七、十九章。

- CSV 名单采用“解析预览 → 服务端保存预览令牌 → 确认时重新核对 → 同事务入队伍”。不覆盖既有学号/身份，预览属于操作管理员且有有效期，重复确认幂等。
- 封榜保存完整成绩快照；管理员继续看实时榜，选手只看冻结快照。提交和判题不停止，解封后恢复实时成绩；每次封榜历史都保留。
- 管理后台覆盖账号搜索/禁用恢复、身份/题目/比赛既有页面、批量参赛状态、CSV、成绩导出、审计、队列/死信/隔离记录与服务就绪信息。后端重查作用域，禁止禁用自己或最后的有效超级管理员。
- Linux deploy.sh 提供安装、状态、日志、停止/启动/重启、更新、备份/恢复和默认保留数据的卸载。配置和持久化数据与源码分开，重复安装不覆盖凭据；生产 Runner 使用内部 TLS，公网入口可配置 HTTP/HTTPS。
- 备份先停写与判题进程，用 mysqldump 逻辑备份和文件归档，不复制运行中 MySQL 数据目录。恢复先验证文件清单和摘要、自动备份当前数据，再恢复，失败保持业务停止并清楚报告。
- 验收准确区分 Linux 容器/宿主 Docker 验证和原生 Ubuntu/Debian 服务器验证，不将仿真或容器测试记成未执行的原生平台验收。

## 管理功能

`/admin` 采用左侧导航和中栏分层工作区："管理概览、测试中心、账号管理、队列与死信、审计与重判"只在当前选中板块加载。概览链接身份、题目、比赛和社区管理页面；测试中心内部再分为“系统检查、编辑器、CSV 样例、题包导入”四个子页签，提供真实系统诊断、代码/Markdown 编辑器演示、CSV 表头样例和外部题包预览/导入；账号板块提供 ID/姓名/学号搜索、禁用/恢复和提交历史；队列板块提供死信重试与消息隔离；审计板块提供审计读取和批量重判。禁用账号同时增加 token_version 并撤销全部会话；恢复后须重新登录。本人和最后的有效超级管理员不能被禁用；已报废临时账号不能通过此接口重新启用。

`/api/admin/operations` 下的 users、users/:id/status、users/:id/submissions、metrics、audits、dead-letters、dead-letters/:id/retry、quarantine 全部要求当前有效的永久超级管理员。列表默认最多 100 条；users/audits/submissions 的 after 为 ID 游标，quarantine 的 after 为偏移量。后台不返回密码哈希、会话密钥或原始隔离消息。Web 不获得 Docker 权限；Runner 就绪状态经 Worker 验证，日志通过部署 CLI 查看。

比赛管理增加 freeze / unfreeze / bulk_status 动作。bulk_status 接受最多 500 个不重复 team_ids、status=active/withdrawn、participant_type=official/star，同事务全部成功或全部回滚。封榜快照追加至 contest_freezes，重复封榜不刷新当前快照；解封仅关闭当前记录。结算后仍能解封，超级管理员仍可授予或撤销比赛管理员，成绩与积分保持锁定。授权管理员一直查看实时成绩。普通成员仍能提交和查看本人结果，但榜单只返回冻结的成绩。

CSV 接口：`POST /api/contests/:id/csv/preview` 接收 `{csv}`；至少需要 username/账号，新比赛账号另需姓名和学号，可选学校或组织、队伍、选手类型。解析器自动识别中英文别名、BOM、大小写和首尾空白，返回规范化列映射；未知列或两个别名映射到同一字段时拒绝。UTF-8 最多 256 KiB / 500 行，单次最多创建 200 个新比赛账号。预览逐行报告身份冲突、重复身份、已参赛和队伍人数问题，并为不存在永久账号的选手生成一次性随机初始密码；数据库只保存 bcrypt 哈希，不保存 CSV 明文密码。确认在比赛结束前可执行，事务内创建 person/比赛账号/关系并组队。测试中心可用相同规则生成当前页面样例，不写正式比赛。

确认接口 `POST /api/contests/:id/csv/confirm` 接收 `{token}`。令牌绑定当前操作人和比赛，有效期 15 分钟，确认时重新解析并核对用户/身份/revision；MySQL 的 JSON 格式化不影响比较。只有全部行有效才原子创建队伍，重复确认不重复添加；开始后不能导入。后台支持选择 CSV 文件、查看预览，再明确确认。

授权管理员通过 `GET /api/contests/:id/export.csv` 导出实时/已结算成绩，每位队员一行，携带队伍名次/分数。字段使用标准 CSV 引号，可能触发电子表格公式的值添加前导单引号。

## Linux 首次安装

支持 Ubuntu 22.04/24.04 和 Debian 12。脚本要求 root、本机 Docker Socket，Docker/Compose 未安装时 install 默认从 [Docker 官方 APT 仓库](https://docs.docker.com/engine/install/ubuntu/) 安装，可设置 `OJ_AUTO_INSTALL_DOCKER=0` 禁止安装。其他必要工具是系统 Python 3、OpenSSL、tar/gzip 和 coreutils/util-linux；无需手动安装 Go、MySQL、Redis。Compose 需 >=2.24.4，以支持 [完整覆盖卷和端口](https://docs.docker.com/reference/compose-file/merge/)。

将仓库放在独立代码目录，例如 `/opt/biue`。数据默认保存在 `/var/lib/biue`，禁止使用系统根目录、源码的上级目录或符号链接作为数据根。首次默认 HTTP 地址为 `http://127.0.0.1`，外部访问时设置实际域名/IP：

```bash
sudo OJ_PUBLIC_URL=http://oj.example.edu bash deploy.sh install
sudo bash deploy.sh status
sudo bash deploy.sh logs
```

安装交互式读取初始管理员密码（不回显）；非交互方式通过 `OJ_ADMIN_PASSWORD` 环境变量传入，勿把口令写入命令行参数或版本库。默认账号 admin，可用 OJ_ADMIN_USERNAME 指定。重复 install 保留 `.env` 和既有账号，不重置密码。生成七个独立随机密钥/口令，配置权限 600，应用与 Runner 分离凭据。脚本从持久配置获取 Compose 变量，父进程中同名 MYSQL_PASSWORD/PUBLIC_HTTP_PORT 等不会覆盖保存配置。

可选首次参数：OJ_DATA_DIR、OJ_PROJECT、OJ_PUBLIC_URL、OJ_HTTP_PORT、OJ_HTTPS_PORT、OJ_PUBLIC_BIND、OJ_TIMEZONE、OJ_JUDGE_CONCURRENCY、OJ_EDGE_SUBNET、OJ_NGINX_IP。应用支持 `PROBLEM_SAMPLE_DIR` 指定内置题包目录，镜像默认 `/app/导入样例`。更换数据目录或项目名后，所有维护命令必须传入相同值。已有配置的日常修改在 DATA_DIR/config/.env 进行，install 不覆盖；改端口/时区/并发后运行 restart，改入口协议/证书后运行 install 并重启。数据库口令不能只修改 env 而不修改数据库账号。

## 公网来源与日志定位

写请求的 `Origin` 必须与持久配置的 `PUBLIC_URL` 在协议、主机和端口上完全一致。来源校验不会通过代理推断地址，也不会在报错时关闭，避免跨站写请求绕过保护。只查看可安全展示的公网来源：

```bash
sudo bash deploy.sh public-info
```

浏览器使用 `https://oj.example.edu`、反向代理终止 TLS 时，`PUBLIC_URL` 也必须写该 HTTPS 地址；只配置 `OJ_PUBLIC_URL` 不会覆盖已经保存的配置。修改并重启：

```bash
sudo bash deploy.sh public-url https://oj.example.edu
```

切换到 HTTPS 时还需要已有公网证书或同时提供 `OJ_TLS_CERT`/`OJ_TLS_KEY`；命令会在写入配置前检查是否存在可用证书，并重新生成 Nginx 配置。外部 TLS 代理必须保留浏览器访问的 `Host`，并传递 `X-Forwarded-Proto`。Nginx 的 `$http_host` 保留了非默认端口。错误来源响应和页面提示附带请求 ID；Web JSON 日志中的 `request_id` 可用于关联同一请求，Nginx 访问日志记录方法、URI、状态和耗时，审计日志记录已授权的业务写入。分别使用 `sudo bash deploy.sh logs`、数据目录 `logs/` 与 MySQL `audit_logs` 排查；不要把完整 `.env`、会话 Cookie、密码或密钥发送到聊天或工单。

生产 Compose 使用独立 bind 目录保存 mysql、redis、problem-data、runner-data、uploads、config、logs、backups，数据和源码分离；语言与应用镜像版本固定。业务容器均不挂 Docker Socket，只有 Runner 挂载；Redis 初始化临时容器只增加 CHOWN 权限，运行时仍 cap-drop ALL。所有常驻服务均有健康检查和 unless-stopped 策略。

### 中国大陆网络与镜像拉取失败

首次安装，或上次因下载失败中断后重试：

```bash
sudo OJ_NETWORK_PROFILE=cn OJ_PUBLIC_URL=http://你的服务器IP bash deploy.sh install
```

已经运行的实例，在更新源码后切换源并升级：

```bash
sudo OJ_NETWORK_PROFILE=cn bash deploy.sh update
```

`cn` 同时配置下面三条下载路径，保持原有固定版本号和 Go 校验机制。配置保存在数据目录 `config/.env`，后续命令会沿用；重复 install 或切换源不会重置账号、数据库密码或业务数据。已有自定义数据目录/项目名时仍需传入相同的 `OJ_DATA_DIR`/`OJ_PROJECT`。

| 下载内容 | cn 配置 | 自定义首次安装/更新参数 |
| --- | --- | --- |
| 全部 Docker Hub 基础镜像，包括 MySQL/Redis 和五种判题语言 | `m.daocloud.io/docker.io/library` | `OJ_DOCKERHUB_LIBRARY=仓库域名/命名空间` |
| Go 模块 | `https://goproxy.cn` | `OJ_GO_PROXY=https://你的模块代理` |
| Alpine 软件包 | `https://mirrors.tuna.tsinghua.edu.cn/alpine` | `OJ_ALPINE_MIRROR=https://你的镜像站/alpine` |

Docker 镜像前缀采用 [DaoCloud 文档中的前缀映射方式](https://github.com/DaoCloud/public-image-mirror)，例如 `mysql:8.0.46` 对应 `m.daocloud.io/docker.io/library/mysql:8.0.46`。[Go 模块代理](https://goproxy.cn/)保留校验数据库验证；[Alpine 镜像](https://mirrors.tuna.tsinghua.edu.cn/help/alpine/)保留发行版分支与软件包签名。公共 Docker 镜像代理有白名单、限流与缓存限制，不能保证每台服务器都可用；可替换为本单位或云厂商仓库，仓库中需事先同步所有固定版本镜像。

例如，按 `mysql`、`redis`、`golang`、`alpine`、`gcc`、`python`、`eclipse-temurin`、`node`、`nginx` 的原名和固定标签同步到 `registry.example.edu/oj` 后：

```bash
sudo OJ_NETWORK_PROFILE=cn \
  OJ_DOCKERHUB_LIBRARY=registry.example.edu/oj \
  bash deploy.sh install
```

前缀不带 `https://` 或末尾镜像名；私有仓库先用 `sudo docker login registry.example.edu` 登录。同一次命令中自定义参数优先于 profile。恢复官方源使用 `OJ_NETWORK_PROFILE=official` 执行 install/update。不传 profile 或自定义参数时保留已存配置；旧版本配置自动补齐官方默认值。

缺失的基础镜像会逐个预拉取，缓存命中则复用。每张镜像默认最多 3 次、每次最多 600 秒；可通过 `OJ_PULL_RETRIES` 和 `OJ_PULL_TIMEOUT`（秒）调整。构建串行进行以减少小服务器的并发下载和内存压力；更新时下载/构建检查先于停止业务和备份，镜像下载失败会指出具体引用。脚本不修改 Docker daemon 的全局镜像源配置。

install 自动安装 Docker 时，`cn` 还会使用[清华 Docker CE APT 镜像](https://mirrors.tuna.tsinghua.edu.cn/help/docker-ce/)，可用 `OJ_DOCKER_APT_MIRROR=https://你的镜像站/docker-ce` 指定替代地址。此参数只影响 Docker 安装仓库；Ubuntu/Debian 系统 APT 源仍沿用服务器现有设置。如果卡在系统 `apt-get update`，需先配置服务器的系统软件源。

### 离线镜像安装与更新

服务器完全无法访问镜像仓库时，在**已安装同一代码版本、相同 CPU 架构**的联网 Linux 机器上导出完整的九个运行镜像（应用、Nginx、五语言、MySQL、Redis）：

```bash
sudo bash deploy.sh export-images /tmp/biue-images.tar
```

生成 `biue-images.tar` 和 `biue-images.tar.sha256`，保留实际使用的完整镜像标签。包只含镜像，不含部署配置或业务数据。输出必须位于源码目录之外，已有同名文件不会覆盖。将两份文件与同一版本源码传到目标服务器，在镜像包所在目录执行：

```bash
sha256sum -c biue-images.tar.sha256 && sudo docker load --input biue-images.tar
cd /你的源码路径/OJ
# 若导出机器使用 cn 配置，首次安装也指定 cn，以匹配 MySQL/Redis 标签：
sudo OJ_AUTO_INSTALL_DOCKER=0 OJ_NETWORK_PROFILE=cn OJ_SKIP_BUILD=1 \
  OJ_PUBLIC_URL=http://你的服务器IP bash deploy.sh install
```

若导出机器使用官方源或自定义仓库，目标机改用相同的 profile/仓库前缀。离线安装前须已有 Docker/Compose 和上文列出的系统工具；脚本不会离线安装这些工具。镜像包和源码须来自可信渠道，SHA256 用于检测传输损坏。

`OJ_SKIP_BUILD=1` 会检查全部运行镜像，缺任何一个立即报错，并禁止启动阶段的拉取和构建。升级时先导入新版本的同架构镜像、更新到匹配源码，然后执行 `sudo OJ_SKIP_BUILD=1 bash deploy.sh update`。此选项每次显式传入，不会永久开启，以免后续升级误用旧镜像。日常 start/restart 同样禁止隐式拉取/构建；若镜像已删除，先重新导入，或执行在线 install/update 重新准备镜像。

## HTTPS 和内部证书

Runner 内部 HTTPS 在生产配置强制开启，安装生成私有 CA 和带 runner/localhost/127.0.0.1 SAN 的证书，只向 Worker/Runner 挂载所需文件；CA 私钥只存在 root 配置目录，不进入运行容器。应用签名密钥和 Runner 令牌互相独立。

公网 HTTPS 使用已有可信证书，首次安装时传 `OJ_PUBLIC_URL=https://oj.example.edu`、`OJ_TLS_CERT=/安全路径/fullchain.pem`、`OJ_TLS_KEY=/安全路径/privkey.pem`。脚本校验格式和证书/私钥匹配，将证书以最小权限保存，配置 TLS 1.2/1.3，HTTP 跳转 HTTPS；`/nginx-health` 例外保留探活。更换/续期证书后运行 install 和 restart，内部证书到期前同样须更新并重启 Worker/Runner。未集成自动申请证书。

## 更新、备份、恢复与保留策略

```bash
sudo bash deploy.sh stop
sudo bash deploy.sh start
sudo bash deploy.sh restart
# 先将代码更新到需要部署的版本（例如 git pull --ff-only），再执行：
sudo bash deploy.sh update
sudo bash deploy.sh backup
sudo bash deploy.sh restore /var/lib/biue/backups/日期-随机后缀
sudo OJ_BACKUP_KEEP=7 bash deploy.sh prune
sudo bash deploy.sh uninstall
```

update 先准备并检查当前源码对应的镜像，再备份数据并迁移，失败保留备份，不自动强制迁移或删库。语言镜像更新后应重启同宿主使用该标签的 Runner。stop/start/restart 管理全套服务；uninstall 只移除本项目容器/网络，保留数据、配置和备份。

backup 停止入口/Web/Worker/dispatcher/Runner，等进程退出后使用 mysqldump 单事务逻辑备份，并归档题目文件、上传目录、Runner journal 和配置；MySQL/Redis 不通过复制运行中目录备份。生成 database.sql.gz、files.tar.gz、SHA256SUMS，校验通过才从 pending 目录原子发布，再恢复服务。OJ_BACKUP_DIR 可指定其他备份目录；备份含密钥，须保护权限和离机副本。

restore 在任何停写/替换之前验证 SHA-256、gzip 和 tar 成员路径，拒绝绝对路径、父目录跳转、符号链接、硬链接和设备文件。随后自动备份当前数据，原文件另存 pre-restore 目录；恢复应用数据库及对应账号口令，保留并重定位必要配置，撤销所有旧登录会话并记录 maintenance.restore 审计，清空可恢复 Redis 状态后迁移/启动。恢复还原备份中的入口 URL/证书配置，输出新的访问地址。失败保持业务停止并报告恢复前备份位置，不假称回滚成功。可在另一台已安装相同/更新兼容代码的服务器执行恢复；只恢复逻辑数据库，不能跨不同 MySQL 主版本盲目导入。

prune 是显式保留操作，默认保留最近 7 份，可配置 1～3650 份；只处理标准命名且内容/摘要有效的三文件备份目录，不递归删除未知路径。恢复前自动备份不会主动清理用户正在选用的旧备份。失败的 pending 目录和 pre-restore 原文件由管理员核查后另行处理。

## 实际验收与剩余平台项

- 大陆源修订：`tools/check_image_sources.py`、`tools/check_deploy_network.sh` 验证配置迁移/保留凭据、完整镜像清单、代理/构建参数、重试和离线流程；全部九个基础镜像已通过 cn 代理实际拉取，应用、Python 沙箱、Nginx 已通过 cn 源构建。独立 `biue-network-check-*` 项目完成本地镜像安装、更新、恢复和保留数据卸载，所有常驻服务健康。没有在用户的大陆服务器测量下载速度，也未将公共代理可用性视为永久保证。

- 全量 Go test/vet/build/race 及既有 16 项 Node 页面行为检查通过；新增脚本语法与维护 helper 的密钥幂等、归档路径/链接/摘要检查、备份保留策略测试通过。
- 独立 `biue-batch3` 升级至迁移 8，实际验证账号禁用后会话撤销、恢复后必须重新登录、CSV 冲突/重新核对/重复确认、封榜期间继续真实 Python 判题、冻结与实时成绩分离、历史封榜保留、批量状态、CSV 导出、监控/审计/队列接口。Edge 桌面/手机验收通过，截图在 `.cache/5-admin-desktop.png` 与 `.cache/5-admin-mobile.png`。
- 在独立 Debian 12 用户空间控制器中，实际执行官方 APT 安装 Docker/Compose、两次 install 和配置摘要比较；没有在控制器手动安装 Go、MySQL 或 Redis，应用运行于专用 Docker 项目。生产 Runner 内部 TLS、四语言真实判题和 OI/ACM/IOI 比赛结算通过。
- 实际执行 backup/update/restart/restore，确认历史提交与三场积分完整，备份后新增记录回滚，恢复后可继续真实判题。卸载保留数据后，在全新数据目录/独立 Compose 项目安装并恢复原备份，核对凭据与路径重定位并再次判题成功；这不是跨两台物理服务器的验收。
- 恢复实例还实际启用带 IP SAN 的自签测试证书；客户端显式信任该证书，未关闭证书验证。验证 HTTPS 登录、Secure/HttpOnly Cookie、HTTP 跳转和实际判题；再次恢复验证旧 JWT 失效、新登录正常。专用生产服务容器最终卸载，控制器停止，卷、配置和备份保留。
- 应用（Web/Worker/Runner 共用）、C++17/Go/Python/Java 和 Nginx 自定义镜像均完成 linux/arm64 构建及 QEMU 启动检查；Web 的 `/ready` 连接真实数据库/Redis 通过，各语言解释器/编译器与监督程序成功启动。arm64 测试使用独立标签，不覆盖 amd64 标签；未将 QEMU 结果算成原生 arm64 整机验收。
- 用户已选择当前没有原生服务器，原生 Ubuntu 22.04/24.04 空机安装、真实宿主机重启、跨真实服务器灾备和原生 arm64 全链路保留为待验。容器测试不代替这些平台项，生产容量/长期压力数据也未据此推断。

自动化入口：`tools/check_operations.py`、`tools/check_operations_browser.cjs`、`tools/check_maintenance.py`、`tools/check_image_sources.py`、`tools/check_deploy_network.sh`、`tools/check_deployment.sh`、`tools/check_production.py`、`tools/check_restore_sessions.py`、`tools/check_https_deployment.sh`、`tools/check_arm64.sh`。网络回归脚本在 `deploy/test/Dockerfile.cli` 控制器内以 `bash /source/tools/check_deploy_network.sh` 运行，挂载只读源码到 `/source` 和本机 Docker Socket，只让真实 CLI 解析配置，修改服务的命令全部模拟。原部署链路测试仅面向显式命名的 biue-deploy5-* 项目，数据位于专用卷；生产 Web/Worker 没有 Socket。详见脚本头部参数和独立环境约束。
