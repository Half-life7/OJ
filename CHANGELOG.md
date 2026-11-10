# 修订日志

每批记录目标、实际变更、数据库/API/权限影响、验证命令与结果、遗留项。状态只使用：进行中、已完成、待验证。

## 2026-11-11 — 大陆网络部署下载源与离线镜像（已完成）

- 定位到 Docker Hub 基础镜像和构建阶段 Go/Alpine 下载源均固定直连官方地址。新增 `OJ_NETWORK_PROFILE=cn`，项目内切换 DaoCloud Docker 镜像前缀、goproxy.cn 和清华 Alpine/Docker CE APT 源；支持自定义仓库及依赖源，保留固定镜像版本、Go 校验和原有业务凭据，不修改全局 Docker daemon 配置。
- 缺失基础镜像逐个预拉取、有限次数重试与单次超时，构建串行执行。更新在停业务前准备镜像；启动阶段禁止隐式拉取/构建。新增 `export-images` 完整九镜像包及 SHA256 导出、`OJ_SKIP_BUILD=1` 本地镜像安装/升级，缺镜像在停业务前报错。导出不覆盖已有文件，使用私有暂存目录和原子不覆盖发布。
- `python tools/check_image_sources.py`、`python tools/check_maintenance.py`、`python tools/check_deploy_guards.py` 通过；`tools/check_deploy_network.sh` 使用真实 Compose 配置解析与模拟仓库故障，验证全部镜像/构建参数、源配置持久化、旧凭据保留、有限重试、无网络安装/更新/导出和缺镜像提前失败。
- 实际经 cn 代理拉取全部九个基础镜像，并使用 cn 镜像/Go/Alpine 源构建应用、Python 沙箱和 Nginx 独立测试标签。独立 `biue-network-check-*` 项目在控制器禁网条件下完成本地镜像 install/update/restore/uninstall，迁移 9 与全部常驻服务健康检查通过，卸载保留配置和备份；未改动原运行项目。
- 数据库/API/业务权限无变更。未在用户的大陆服务器实测速度；公共镜像代理的白名单、限流和可用性限制仍适用，提供自定义仓库与离线方式。使用步骤见 docs/OPERATIONS.md，原生平台待验项沿用既有约定。

## 2026-11-10 — 批次 6 P2 扩展（已完成）

- 迁移 9 增加 javascript/text 语言、榜单 period 主键和重判幂等回执。题面不可变快照保存题型和 Python checker，公开题库与比赛题面隐藏 checker_source；修改后的配置只影响新版本，原提交/重判保持原版本。
- 新增受限独立 SPJ，选手容器不获取答案，判题器只返回 AC/WA，异常按原预算重试且不公开输出/诊断，空输入/答案明确编码为空 base64 字符串。输出型 JSON 不启动选手程序，提供只读 case 标识清单，逐点验证，缺点 WA、重复/未知键及非字符串/null 拒绝；增加固定 Node.js 22.23.4 沙箱，原四语言顺序和默认值保留。
- 新增认证 SSE 摘要推送、连接数和时长上限、每秒权限重查、终态/撤权事件及轮询降级；Nginx 关闭事件缓冲。批量重判使用 actor/key 幂等、generation CAS 和单事务 Outbox，保留旧结果并使全局榜失效，已结算比赛拒绝；后台及详情显示新轮次。
- 三个全局榜新增 all/week/month，身份去重和 30 秒可重建缓存保留；输出型、SPJ 和新语言管理/提交界面已接入。同步修复题目编号搜索，匹配现有界面约定。
- 全量 Go test/race、vet/build 及原 16 项 Node 页面行为测试通过；真实 MySQL 迁移/回退保护、Node Submit/Run、SPJ AC/WA/异常保密、输出型、SSE 即时送达/撤权、重判幂等/回滚/历史/缓存/结算保护和周期统计通过。Edge 桌面/手机界面回归通过；应用及新 Node 镜像 arm64 构建/仿真启动通过，Web /ready 实连数据库/Redis。
- 原生整机待验事项延续批次 5 约定，不用仿真替代。六批次实施内容已落地，操作和复现见 docs/EXTENSIONS.md；改动尚未提交 Git。

## 2026-11-10 — 批次 5 管理与部署交付（已完成；原生平台待验证）

- 迁移 8 新增封榜历史和 CSV 预览令牌。CSV 支持逐行预览、身份冲突检查、确认时重新校验、15 分钟有效期和幂等原子导入；保留全部封榜历史，普通成员使用冻结成绩，管理员继续看实时榜，支持解封、批量正式/打星/退赛恢复和安全 CSV 导出。
- `/admin` 与管理 API 提供账号搜索/禁用恢复/提交历史、审计、队列与死信重试、隔离记录和服务就绪统计；禁用撤销会话，恢复后重登，防止禁用自身/最后的有效管理员。Web/Worker 不获得 Docker Socket。
- 根 deploy.sh、production/HTTPS Compose 和 maintenance.py 提供幂等安装、官方 APT Docker 自动安装、独立强随机配置、内部 Runner TLS、公网 HTTP/HTTPS、更新前备份、停写逻辑备份、校验/防路径越界恢复、恢复前自动备份、会话撤销、显式保留策略及保留数据卸载。持久配置不被父进程同名变量覆盖，数据/备份与源码隔离；构建器支持原生编译到目标架构。
- 全量 Go test/vet/build/race、原 16 项页面行为检查通过；实际 MySQL/Redis/Python 沙箱验证封榜下继续判题、CSV 冲突/确认防重、账号失效、监控与导出；Edge 桌面/手机后台及 CSV 文件预览/确认/批量操作通过。
- 独立 Debian 12 控制器实际安装 Docker/Compose，完成两次 install 保持配置摘要、生产 TLS Runner 下四语言和 OI/ACM/IOI 判题/结算、backup/update/restart、同实例恢复及新数据目录独立实例恢复；恢复后新判题成功，旧 JWT 失效。HTTPS 测试显式校验证书，Secure/HttpOnly Cookie 与 HTTP 跳转通过。卸载容器后数据/备份保留。
- 应用、四语言和 Nginx 的 arm64 镜像构建/仿真启动通过，Web /ready 连接真实服务成功。此结果不等于原生 arm64 整机验收。用户选择暂无原生服务器，原生 Ubuntu 空机/真实重启/真实跨机与原生 arm64 验收按约定待验证；详见 docs/OPERATIONS.md，不将平台项标记为已通过。

## 2026-11-10 — 批次 4 日常业务与三个全局榜（已完成）

- 迁移 7 新增题单、收藏、讨论/回复/点赞、带审核的公开/私有 Markdown 题解、写限速和 MySQL 榜单快照。日常题库增加分类筛选与通过人数/通过率/本人 AC 状态，复用原有真实提交链路。
- 固定日常解题、比赛积分、题解覆盖三榜，按 person 去重；日常排除 Run/比赛/临时账号，比赛按已结算正式有效队伍及同人同场最高积分汇总，题解只计公开审核通过且仍有有效 AC 的不同题目。默认缓存 30 秒，身份关联/结算/内容写入主动失效，可清空后从 MySQL 重建。
- 写事务重查账号/权限/revision/上下文/AC，收藏及点赞幂等，删除父讨论后隐藏后代。Goldmark v2.1.8 禁原始 HTML，Bluemonday v1.0.27 净化输出；新增依赖和校验记录固定在 go.mod/go.sum。
- 全量 test/vet/build/race、模块校验及既有 16 项 Node 页面行为测试通过；真实 MySQL 迁移 7、Python 沙箱日常判题和 API 权限/去重/审核/重建验收通过。实际 Edge 桌面/手机浏览器验收通过，无 JS 异常。
- 页面 `/learning`、`/rankings` 及题目统计/社区入口已交付；复现命令、统计口径和限制见 docs/COMMUNITY.md。独立验收容器及数据卷保留。生产压力/部署继续归批次 5。

## 2026-11-10 — 批次 3 完成与验收（已完成）

- 迁移 6、身份审核/纠错/关联、个人/团队比赛、报名/授权/临时账号、正式/打星/退赛恢复、独立 OI/ACM/IOI 引擎、固定版本比赛提交、重建与幂等结算、按身份去重的可配置策略积分账本，以及对应页面已交付。
- 最终审阅补充身份列表的关联账号展示、名单类型/状态/提交数筛选、审计 request ID、结算后人工重试保护和 IOI 零分点的通过证据。现有日常提交和判题恢复机制保留。
- `go test ./...`、`go vet ./...`、`go build ./...`、`go test -race ./...` 通过；新增 JS 语法检查通过。真实 MySQL 迁移 6/重复启动、三模式 API/SQL 回归通过；SQL 夹具与真实沙箱验收分开记录。
- `python tools/check_contest_runtime.py` 验证真实 Python Docker 判题、逐点保存、IOI 榜单和结算；并发退赛/提交、四并发结算通过，仅一份快照和积分。`node tools/check_contest_browser.cjs` 使用实际 Edge 验证桌面/手机页面、创建/编辑、题目绑定、临时选手、名单筛选和身份选择，无 JS 异常。
- 复现方式、权限和边界见 docs/CONTESTS.md。CSV、封榜/解封、完整批量后台及生产容量/原生 arm64 验收保留批次 5；本次并发测试不计为长期压力验收。独立 `biue-batch3` 容器及数据卷保留。

## 2026-11-10 — 批次 3 身份与比赛系统（进行中）

- 新增迁移 6、身份管理、比赛与队伍、临时账号、授权和报名、独立三模式计分、比赛固定版本提交、重建与幂等结算、按身份去重的积分账本，以及比赛/身份页面。
- 全量 Go test/vet/build 通过；最新源码全量 race 通过。真实 MySQL 迁移及三模式 API/SQL 验收脚本通过，计分采用明确的 SQL 夹具，不据此声称真实沙箱链路验收完成。
- 用户要求汇报并结束，已停止推进。实际浏览器检查、最新源码容器重建、比赛真实判题链路和最终范围复核留待继续；详细交接见 docs/CONTESTS.md。未提交 Git，独立验收容器与数据卷保留。

## 2026-11-10 — 目标节点模型与思考强度建议（已完成）

- 为实施计划的全部批次及现有小批次细案补充建议模型与思考强度；为 plan.md 开发优先级的六个阶段追加建议，并在 README 标明下一切换点。
- 根据 OpenAI 官方模型选择与思考强度说明，结合项目复杂性推荐配置；已完成节点的标注用于后续维护参考，不追溯声称历史使用模型，不修改原始需求、已有进度及验收记录。
- 下一入口为批次 3 的接口、数据库、权限与验收细案，建议 GPT-6 Astra + high（高）。本次仅修改文档，未开始该批开发；等待用户切换模型与思考强度并明确继续。
- 数据库/API/权限影响：无。验证：`git diff --check`；仅文档变更，未运行代码测试。

## 2026-11-10 — 2D 全链路及真实验收（已完成）

环境：Windows/amd64 开发机；经授权后台启动 Docker Desktop，实际引擎 29.9.3、Linux/amd64。使用独立 Compose 项目 `biue-2d-20261110004742`、新随机测试凭据、独立数据库/Redis/数据卷，未修改其他项目数据。验收入口 `http://localhost:6677`，凭据只保存在 Git/构建上下文排除的 `.cache/2d-acceptance.env`，未打印到日志。

- 完成常驻 worker/dispatcher、独立 judge-runner 与四语言镜像。Worker 先检查就绪，再串联领取、固定请求、租约心跳、阶段 CAS、Runner 流式调用、可信结果校验、原子持久化及 ACK；忙碌/丢租约/未知数据库写入/退出不误消耗执行重试预算。
- Runner 认证采用独立 Bearer 密钥，客户端禁代理与重定向，生产要求 TLS。持久化执行 journal、请求指纹、Linux 文件锁与新租约接管取消保证同 attempt 不重复启动；重启清理自身 owner 的遗留容器，再重放结果或返回可重试 SystemError。清理失败保留补偿债务并暂停就绪。
- 四语言代码通过容器 attached stdin 的受控文件包传入，编译产物/资源经每容器 HMAC 签名 stdout 返回。不可变监督程序禁 dumpable、清理子进程环境，隐藏答案从不进入容器。每点全新容器、非 root、禁网/只读 rootfs、cap-drop/no-new-privileges/seccomp、CPU/内存/swap/PID/时间/tmpfs/输出/文件大小/描述符限制，关闭沙箱日志以免重复保存产物/输出。
- 实际验收修复了 archive API 与活跃 tmpfs 可见性不一致问题，改用流式标准输入输出；修复 Docker OOM 事件接口必须使用数字 Unix 秒/纳秒的问题，并对异步事件短暂延迟进行有界重查。没有通过关闭只读根目录或放宽权限绕过问题。固定 Docker API 1.46，要求 Engine 27+。
- 新增迁移 5 消息隔离表：只保存消息 ID/摘要/受控原因，持久化后才 ACK。增加 Redis 丢数据后的普通/死信 Outbox 对账；提供有超级管理员身份/容量/预期 generation 检查及同事务审计的 `ojctl judge dead-list/retry`，保留原 generation 全部历史。
- Compose 加入 Worker、dispatcher、Runner、四语言镜像准备服务、独立 judge 网络和 runner-data 卷；只有 Runner 获得 Docker Socket，Runner 无数据库密码、不接入 backend、不暴露宿主机端口；TLS 覆盖配置及健康探针齐备。主业务代码仍无宿主机命令执行能力。

实际验证：

| 检查 | 结果 |
| --- | --- |
| Windows `go test ./...`、`go vet ./...`、`go build ./...`、`go test -race ./...` | 通过 |
| Linux 验收容器 `BIUE_DOCKER_TEST=1 go test -race -p 2 ./... -count=1 -timeout=20m` | 通过，包含实际 Docker 执行，非跳过 |
| Node 页面回归 | 16 项通过 |
| `go mod verify` | 所有模块校验通过 |
| Compose 普通/TLS 静态配置、角色/网络/Socket/密钥分离校验 | 通过 |
| MySQL 空库迁移、重复 `migrate up` | 当前版本 5，dirty=false |
| 实际 CHECK、历史外键、重复 Outbox 唯一约束 | 分别验证错误 3819、1451、1062；测试操作事务回滚 |
| 四语言经真实 Web→Redis→Worker→Runner→MySQL | C++17/Go/Python3/Java 均 Accepted、100 分；Run 输出 42、零分 |
| HTTP 终态持久化及输出限制 | CE/WA/RE/TLE/MLE、Run 输出超限全部通过 |
| 实际沙箱边界 | 非 root、只读根目录、禁网、监督程序 secret 隔离、输出/PID/文件大小/描述符限制通过 |
| 固定版本 | 等待中修改题面、样例和数据指针，旧 Submit 仍使用原版本，旧 Run 保持创建时输入 |
| Worker/Runner SIGKILL | 均自动恢复并最终 Accepted，旧执行未覆盖新尝试 |
| 三次重试及第四次失败 | 真实数据不可用产生恰好四次失败、SystemError、持久化死信及死信流 |
| 死信人工重判 | 创建新 generation 后 Accepted，旧 SystemError 结果保留，审计同事务 |
| 重复终态消息 | ACK 后结果历史数量不变 |
| Redis FLUSHDB | MySQL 对账重新派发，原任务最终 Accepted，数据库会话保持可用 |
| MySQL/Redis 服务重启 | Web 自动恢复就绪，提交/结果/隔离记录数量不变，Redis 消费者组及已确认状态保留 |
| 非法 Stream 消息 | 实际保存 SHA-256 摘要与原因后 ACK，未保存原始消息 |
| linux/amd64、linux/arm64 全部 cmd 入口交叉编译 | 通过 |
| arm64 启动 | 交叉编译 web 在仿真容器启动，/health=200、/ready=200；非原生 arm64 整机验收 |

HTTP 验收元数据保存在 `.cache/2d-smoke-basic.json`、`.cache/2d-smoke-recovery.json`、`.cache/2d-smoke-verdicts.json`，均不含凭据。恢复样本包括固定版本、Worker/Runner 崩溃、死信/人工重判和 Redis 丢失恢复；终态 HTTP 样本覆盖四语言、CE/WA/RE/TLE/MLE 及 Run 输出限制。初次定位问题产生的失败测试历史按保留规则未删除。

验收脚本为 tools/verify_2d.py（可选状态/故障矩阵）、tools/verify_2d_recovery.py、tools/verify_2d_verdicts.py，配置检查为 tools/check_2d_compose.py，真实沙箱用例在 internal/judge_runner/docker_integration_test.go。文档已同步至 README、API、数据库、实施计划及 RUNNER/WORKER/DISPATCH 说明。

剩余范围：批次 3 的身份审核/比赛/计分；完整管理网页、生产容量与长期压力、生产安装升级/备份恢复、原生 arm64 完整部署仍属后续批次。本次没有部署生产公网服务。

## 2026-11-10 — GitHub 同步与验收环境收尾

- 2D 实现提交 `c4e0dae`（`feat: complete 2D judging pipeline`）已推送至 `origin/main`；本次文档收尾随后会作为单独提交同步。
- 独立验收项目 `biue-2d-20261110004742` 完成检查后关闭；MySQL、Redis、problem-data 和 runner-data 数据卷保留。
- 随机验收凭据只在 Git 忽略的 `.cache/2d-acceptance.env`，没有进入提交或构建上下文。

## 2026-11-09 — 2D 第四小批：执行重试与死信（已完成，本地验证）

- 新增迁移 4：judge_attempt_failures 按 task/attempt 唯一保存受控失败原因与 retry/dead 决策；judge_dead_letters 保存持久化死信兼 Outbox；普通 Outbox 增加 available_at 和到期索引。ExpectedVersion 升至 4，真实数据库尚未执行迁移。
- 新增 ExecutionStore.Fail：有效 generation/attempt/token/租约下，前 3 次基础设施失败原子保存失败历史、增加 attempt、清理租约并复位 Pending、创建延迟 1/2/4 秒的新 Outbox；第 4 次同事务保存 SystemError 零分终态、结果历史和死信意图。数据卷/Redis 不可用不阻止 MySQL 失败决策保存。
- Consumer.Fail 在提交决策后才 ACK；AcknowledgeFailed 查询完整身份的持久化决策并核对 Stream 载荷，支持提交响应未知或 Redis 故障后的补 ACK。旧 attempt 不能重复增加重试次数；普通判题结果不重试。合法 Runner SystemError 经 Consumer.Complete 路由到失败流程，Finish 不允许直接绕过预算保存该状态。
- dispatcher 扩展独立死信派发至 oj:judge:dead，消息仅带四项任务标识、稳定 dead_letter_id 及原因码；XADD 后登记，允许安全重复投递。每轮分别处理普通与死信意图，普通派发只读取已到 available_at 的意图。
- 实际通过：go test ./...、go vet ./...、go build ./...、go test -race ./...。测试覆盖全部三次重试的身份与退避参数、事务各步骤回滚、第四次失败终结、失败决策先提交再 ACK、提交未知后的防重复及补 ACK、Redis 恢复、死信非法记录/发送/登记/提交失败、普通派发到期条件、嵌入迁移链及版本门禁；使用工作区 Go 缓存和 GOPROXY=off。
- 已同步 README、实施计划、Worker/派发/提交说明与数据库文档。无新增 HTTP API、环境变量或 Docker 权限。真实 MySQL DDL/约束/退避与 Redis 重启恢复仍待验收；下一小批对接独立 Runner 与常驻 Worker，死信管理/人工重试、非法消息隔离、对账补投和 Compose 仍待实现。

## 2026-11-09 — 2D 第三小批：固定请求、阶段推进、结果持久化与 ACK（已完成，本地验证）

- 新增 ExecutionStore：按 submission 固定 statement_version/data_version 加载题面限制及测试数据，Run 使用保存的 stdin；Submit 核对数据卷指纹、点数、总字节和总分。
- 新增阶段推进及结果提交事务，按 submission→task 锁顺序检查 generation/attempt/token/有效期、双方状态与合法流转；清理终态租约，并原子写入 submission_results、submission_case_results 及 submission 汇总。任何失败回滚，终态及迟到旧结果不可覆盖。
- 新增 Runner 结果契约校验：任务身份、终态、分数、点数、逐点 ID/顺序及资源汇总一致性；Run 永不计分。正式提交在写库前清除输出及非编译诊断，逐点表不保存隐藏输入输出。
- 新增 WithHeartbeat：执行前续期、TTL/3 心跳、续期失败取消上下文并等待心跳退出；执行器需响应取消。
- Consumer.Complete 严格先 Finish 再 ACK；AcknowledgeCompleted 先查询完整任务身份/attempt 的持久化终态、核对实际 Stream 消息载荷，再 XACK，支持数据库提交响应丢失或 Redis 故障后的补 ACK。
- 实际通过：go test ./...、go vet ./...、go build ./...、go test -race ./...。新增测试覆盖真实数据卷读取/摘要核对、阶段重放/倒退、旧 generation/attempt/token、写入时过期、历史/逐点/汇总写入失败回滚、提交结果未知、ACK 前结果证明与载荷核对、Redis 恢复及心跳取消；使用工作区 Go 缓存和 GOPROXY=off。
- 无新增迁移、HTTP API、环境变量或权限。已同步 README、实施计划、提交及派发说明、Worker 调用与结果契约。真实 MySQL/Redis、跨进程崩溃及容器执行仍待验收；下一小批实现执行重试/死信，后续接入 Runner、常驻 Worker、对账补投与 Compose。

## 2026-11-09 — 2D 第二小批：Worker 消费与租约基础（已完成，本地验证）

- 新增 Queue：从 Stream 起点幂等创建 judgers 组，XAUTOCLAIM 游标扫描未确认消息，非阻塞 XREADGROUP 读取新消息；不提前 ACK，不返回非法消息原始字段。
- 新增 LeaseStore：submission→task 行锁顺序、当前轮次与状态检查、128 位随机租约、MySQL 时钟过期判断、Pending→Queued 原子更新、过期令牌替换及完整身份/令牌/有效期续期 CAS。
- 新增 Consumer.Next 串联接收与领取，区分 acquired/busy/obsolete；只有事务提交成功才返回可用租约。没有新增迁移、HTTP API、配置项或容器权限。
- 测试覆盖组创建前消息、未确认消息恢复、重复领取、非法消息、Redis 故障、旧轮次、终态、状态不一致、事务回滚、续期失效及跨消费者更换令牌且不 ACK 的组合流程。
- 实际通过：go test ./...、go vet ./...、go build ./...、go test -race ./...；使用工作区 Go 缓存和 GOPROXY=off。同步 README、实施计划、提交说明、派发说明及 docs/WORKER.md。
- 真实 MySQL 并发/时钟、Redis 重启与跨进程崩溃仍待验证。消费模块尚未接入常驻入口；请求加载、执行阶段推进、心跳循环、结果落库及 ACK、执行重试/死信、Runner、对账补投和 Compose 继续留待后续小批。

## 2026-11-09 — 2D 第一小批：Outbox 派发（已完成，本地验证）

- 新增独立 cmd/dispatcher 与 internal/judge 派发器：MySQL 行锁 SKIP LOCKED 领取已提交意图，XADD 成功后登记 Stream ID，单次操作上下文上限 5 秒；空队列及故障每秒重试。
- 入口检查迁移版本、dirty 与依赖就绪，支持信号取消；无数据库迁移、HTTP API 或权限变更，无 Docker Socket。
- 增加 sqlmock/miniredis 测试：成功、空队列、非法信封、Redis 失败、发送后登记失败及提交结果未知；确保只有数据库提交成功才报告派发成功，失败允许重复投递。
- 实际通过：go test ./...、go vet ./...、go build ./...、go test -race ./...、git diff --check。Go 使用工作区 .cache/gobuild 与 .cache/gomod，GOPROXY=off。
- 更新 README、实施计划、提交说明及 docs/DISPATCH.md。真实 MySQL 并发锁、Redis 重启恢复与信号验收待验证；Compose 接线、MySQL 对账补投、Worker 消费与租约、结果持久化及 ACK、重试死信和 Runner 尚未实现，完整 2D 继续进行中。

## 2026-11-09 — 计划落地

- 已阅读需求基线 plan.md，建立 docs/IMPLEMENTATION_PLAN.md。
- 已确认首批为工程基础与认证，学号归属 persons，真实运行环境验收暂缓。
- 当前环境：Windows/amd64，Go 1.25.4；Docker CLI 存在但引擎不可连接。
- 当前只有需求文档，无既有代码或 Git 仓库。本次不自动创建远程仓库或发布服务。

## 2026-11-09 — 1A 工程基础（待验证，代码已落盘）

- 建立 Go 工程、环境配置与脱敏 slog、Web 和 ojctl 入口、请求 ID、超时、异常恢复与优雅退出。
- 建立 MySQL/sqlx 和 Redis 连接池、独立迁移连接及嵌入 SQL。业务连接禁止 multiStatements；迁移不自动清除 dirty 状态。
- 新增 GET/HEAD /health、/ready；就绪检查包含 MySQL、Redis、迁移版本及 dirty。业务依赖故障时返回统一 503。
- 提供非 root 多阶段 Dockerfile、开发 Compose、Nginx、环境示例、忽略规则、README 和数据库/API 文档。MySQL/Redis 不映射宿主端口，未挂载 Docker Socket。
- 修正 IPv6 数据库地址拼接、Redis 默认重试参数、审计请求 ID 上下文、API 404/405 的 JSON 响应及日志查询参数脱敏。
- 数据库变更只写成迁移文件，没有连接或变更真实数据库。

## 2026-11-09 — 1B 账号认证（待验证，代码已落盘）

- 永久账号注册、登录、退出、改密、本人资料与管理员初始化的实现已落盘。
- 新增 persons、users、person_accounts、student_no_history、auth_sessions、audit_logs 六表迁移。学号属于身份、可空唯一；历史外键限制删除。
- JWT 使用 HttpOnly Cookie；持久化会话撤销、版本失效、CSRF、来源校验、Redis 双维限流及关键事务审计。
- 登录、退出、改密统一按用户到会话顺序加锁；改密复核版本与会话并撤销全部旧会话。禁止自动提权或覆盖已有管理员密码。
- 提供首页、注册、登录、个人中心，以及对接真实 API 的加载/失败状态。未实现判题或比赛的伪接口。
- 已编写认证全流程、CSRF、JWT、事务回滚、禁用/改密失效、限流和管理员保护测试；这些依赖外部 Go 模块的测试尚未实际执行。
- 严格校验 JSON 对象、未知字段和体积；退出接口允许无请求体或 JSON 空对象，新增十种输入边界测试。前端密码长度统一按 UTF-8 字节计算，避免浏览器字符数与服务端口径不一致。

## 2026-11-09 — 本批验证记录

执行环境：Windows/amd64、Go 1.25.4。Go 检查使用工作区 GOCACHE；无外部依赖的检查可在 GOPROXY=off 下执行。

| 检查 | 实际结果 |
| --- | --- |
| go test ./internal/config | 通过 |
| go test -race ./internal/config | 通过 |
| go vet ./internal/config | 通过 |
| go test ./webui | 通过，含页面、静态资源、404/405 和 HEAD |
| go test -race ./webui | 通过 |
| go vet ./webui | 通过 |
| go test ./migrations | 通过编译；该包没有测试用例，不代表 SQL 已执行 |
| node --check webui/static/app.js | 通过 |
| gofmt -l cmd internal migrations webui | 无未格式化文件 |
| Go parser 检查全部源码 | 通过语法检查，不代表类型检查或构建成功 |
| Compose config --quiet（仅临时进程验证值，无服务启动） | 通过；Docker 用户配置读取出现权限警告 |
| go mod download | 未成功：模块代理网络连接被当前环境阻止 |
| go test ./...、go vet ./...、go build ./... | 未通过初始化：缺少依赖与 go.sum，尚未进入编译 |
| 真实 MySQL/Redis、迁移约束、容器构建启动、浏览器联调、Linux/arm64 验收 | 未运行，按约定暂缓 |

直接依赖固定版本及三个关键第三方 API 已核验官方 tag 源码存在、声明的 Go 版本兼容本机；此核验不代替构建。

下一批入口：先在可获取依赖的环境执行 go mod tidy，保留 go.mod 间接依赖和 go.sum；完成全量 test/vet/build/race 并修复失败，再验收真实 MySQL/Redis 与开发 Compose。首批尚未标记完成，不提前宣称完整 OJ 或生产部署可用。

## 2026-11-09 — 继续验证与 2A 准备（进行中）

- 再次确认 Go 直连模块代理被限制，Docker 引擎仍不可用。
- 找到正常权限的直接 curl HTTPS GET 下载路径；从官方模块代理与 sum.golang.org 校验记录补齐工作区缓存和真实 go.sum，未关闭模块校验。HEAD 请求失败不能用于断言 GET 不可用。
- 修复原始 JSON 非法 UTF-8 会被 encoding/json 替换后进入密码验证的问题，新增输入回归和 HTTP 改密/清 Cookie/旧会话失效用例。
- 计划新增独立的内存测试数据 ZIP 安全校验模块（2A）；格式、限制、分值与摘要规则先写入实施计划。无数据库/API 变更，不执行用户代码。
- 实际全量验证及 2A 测试结果待完成后补记。

## 2026-11-09 — 首批本地验证完成

- 使用官方模块包和官方校验记录补齐缓存，保留原 10 个直接依赖版本，由 Go 自动添加 7 个间接依赖。go.sum 已生成；未关闭 GOSUMDB，未手写 ziphash。
- go mod tidy：通过。go mod verify：通过，输出 all modules verified。
- go test -mod=mod ./...：通过，认证、服务、存储、配置、页面均实际执行；两个命令入口成功编译。此时 2A 测试尚未完成，不将其列入该结果。
- go vet -mod=readonly ./...、go build -mod=readonly ./...：通过。
- go test -race -mod=readonly ./internal/auth ./internal/server ./internal/storage ./internal/config ./webui：通过。
- Web 与 ojctl 均成功交叉编译为 Linux/amd64 和 Linux/arm64；只验证产物构建，没有声称在 Linux 或 arm64 上运行成功。
- 实际启动临时 Web 进程，指向无数据库/Redis 服务的测试端口：GET /health=200、/ready=503、/register=200、/api/auth/csrf=200 且返回 token；HTTP smoke 通过。测试进程已关闭，不将强制关闭计作优雅退出验收。
- Dockerfile 改为复制已锁定的 go.mod/go.sum 并使用 -mod=readonly 构建，构建时不再隐式执行 tidy 修改依赖；README 同步更新。
- 当前本地检查使用 GOCACHE=.cache/gobuild、GOMODCACHE=.cache/gomod、GOPROXY=file:///D:/Code/biue/.cache/gomod/cache/download；缓存不进入源码或容器上下文。
- 真实 MySQL/Redis 的迁移、唯一约束/锁与故障恢复、Docker 镜像构建/启动、浏览器联调和 Linux 部署仍待验。

## 2026-11-09 — 2A 测试数据 ZIP 校验器（已完成独立模块验收）

- 新增 internal/testcase：在内存中解析 ZIP32，返回成对输入/输出、精确分值映射、总分/总字节数及稳定 SHA-256 内容摘要。
- 在标准库分配 ZIP 条目前限制中央目录大小和实际条目数量；拒绝危险路径/名称、链接、加密、多卷、ZIP64、未知成员、缺失配对及大小写冲突。
- 限制压缩包、单成员与总展开体积，校验声明尺寸与实际读取量，支持上下文取消，失败不返回部分数据。保留输入输出的原始二进制字节。
- 独立审阅发现标准库在无 data descriptor 且 CRC 为零时的校验边界；模块改为始终显式验证实际 CRC，并新增对应回归。摘要完成后再次检查取消状态。
- 无新增数据库迁移或 HTTP 接口；不写磁盘、不执行代码、不生成 data_version。详细格式及调用方法见 docs/TEST_DATA.md。
- go test ./internal/testcase、go test -race ./internal/testcase、go vet ./internal/testcase：通过。
- 5 秒双 worker fuzz：通过，378,135 次执行；常规单测包含 fuzz 种子回归。
- 合并后最终 go test -mod=readonly ./...、go test -race -mod=readonly ./...、go vet -mod=readonly ./...、go build -mod=readonly ./...：全部通过。
- 最终 go mod verify：all modules verified；gofmt -l 无输出。
- 后续入口为 2B 题目与不可变测试数据版本管理：先细化数据库/API/权限/存储与失败补偿，再接入本模块。真实服务与部署验收仍按前述待验证项保留。

## 2026-11-09 — GitHub 仓库导入准备（待验证/待推送）

- 用户指定目标仓库 https://github.com/Half-life7/OJ，授权提交当前项目。
- 已初始化本地 Git 的 main 分支；未修改既有源码功能或依赖。
- 发布前只读审核未发现真实 .env、私钥或常见访问令牌；仅有空白密码/密钥的 deploy/.env.example。忽略规则排除缓存、环境配置、数据目录和可执行产物。
- 当前任务对 .git 的写入受限，设置远端和 git add --dry-run 均无法创建锁文件；没有生成提交，也没有执行成功的 push。
- 现有 GitHub 凭据管理器无法访问/持久化 wincredman 凭据，远端读取未通过认证。不能将匿名 API 的 404 解释为仓库不存在。
- 准备 .cache/OJ-source.zip 及文件摘要清单，作为可审阅的完整源码包；不包含 .git/.cache、真实环境配置或构建缓存。
- 后续须在 Git 元数据可写且 GitHub 已认证的环境中提交并推送；若远端已有提交，先获取并合并历史，禁止强制覆盖。

## 2026-11-09 — Gin 选型文档同步（已完成）

- 按用户更新后的 plan.md 同步目标技术栈：Web 框架使用 Gin，HTTP 服务底层基于 Go net/http，API 路由、中间件和参数绑定统一使用 Gin。
- 更新实施计划，新增待开始的 1C Gin HTTP 层迁移，安排在 2B 前；列明参数校验、认证、CSRF、来源与代理校验、日志、健康检查和 HEAD/404/405 的兼容性验收要求。
- README 与 API 文档明确当前代码及依赖仍为 chi，Gin 迁移尚未实施；迁移继续遵守既有 API、Cookie 和安全约定，完成后单独记录测试结果。
- 本次仅更新文档，保留用户对 plan.md 的修改及此前历史验证记录；源码、依赖、数据库与 ZIP 校验器行为未变。
- 验证：git diff --check 通过，Markdown 框架引用与批次顺序检查通过；SHA-256 比对确认 plan.md、go.mod、go.sum 未被本次更新改变。本次纯文档修改，未运行 Go 测试。

## 2026-11-09 — 1C Gin HTTP 层迁移（已完成，本地验证）

- 将 Web 路由、认证处理函数、中间件和相关测试迁至 Gin v1.12.0。API 注册接口改为 func(*gin.RouterGroup)，HTTP 服务继续使用 net/http 的超时与退出管理，业务服务和数据库事务未变。
- 使用 ShouldBindWith 自定义绑定器维持 16 KiB、JSON 对象、单文档、未知字段、错误类型和 UTF-8 校验；新增恰好达到体积上限及超量尾随空白回归。退出仍允许无请求体或空对象。
- 将现有 gorilla/csrf 集成到 Gin，保留令牌上下文，失败时中断处理链；保留 Cookie、会话撤销、改密失效、限流、来源校验和依赖故障语义。
- 禁用 Gin 默认代理信任及自动路径重定向；保留可信代理链、RawPath、API JSON 404/405、HEAD、MPA 页面及静态资源行为。统一服务器生成的请求 ID 与审计上下文，保留日志脱敏、异常恢复和请求超时。
- 启动入口使用 Gin release 模式与现有 slog；未启用 Gin 默认请求日志。新增请求 ID、防伪代理头、HTTP/HTTPS CSRF、上下文超时/取消、页面 fallback 和真实 HTTP HEAD 回归。
- go get github.com/gin-gonic/gin@v1.12.0、go mod tidy：通过。Gin 已替换 chi 直接依赖，chi 校验记录已清理；Go 同步调整包括 golang.org/x/crypto v0.48.0 在内的所需依赖，保留官方模块校验。

检查环境为 Windows/amd64、Go 1.25.4，使用工作区 GOCACHE=.cache/gobuild、GOMODCACHE=.cache/gomod；依赖下载后以下 Go 检查使用 GOPROXY=off：

| 命令 | 实际结果 |
| --- | --- |
| go test -mod=readonly ./... | 通过 |
| go vet -mod=readonly ./... | 通过 |
| go build -mod=readonly ./... | 通过 |
| go test -race -mod=readonly ./... | 通过 |
| go mod verify | 通过，all modules verified |
| CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -o .cache/bin/gin-linux-amd64/ ./cmd/web ./cmd/ojctl | 通过，仅验证交叉编译 |
| CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -mod=readonly -o .cache/bin/gin-linux-arm64/ ./cmd/web ./cmd/ojctl | 通过，仅验证交叉编译 |
| gofmt -l cmd internal migrations webui | 无未格式化文件 |
| git diff --check | 通过 |

- 实际构建并运行 Web 测试进程，指向不可用的 MySQL/Redis：GET/HEAD /health=200、GET /ready=503、/register=200、/static/app.js=200、/api/auth/csrf=200 且返回令牌、未知 API=404、错误方法=405、POST /api/auth/login=503；各响应均包含请求 ID。未出现 Gin debug 输出。
- 测试进程已关闭；未将终止进程计作优雅退出验证。真实 MySQL/Redis、数据库约束、容器构建启动、浏览器联调及 Linux/arm64 运行仍待验收。
- README、API 和实施计划同步更新当前状态。下一批进入 2B，先细化题目与不可变测试数据版本的数据库、接口、权限和存储方案。

## 2026-11-09 — 2B 题目与不可变测试数据版本（已完成，本地验证）

- 新增题库公开列表/详情、超级管理员创建/编辑/下架、标签/难度/可见性管理和对应 MPA 页面。普通读取在 SQL 层只允许 published+public，其余返回 404；管理接口在读取请求正文前验证会话与角色。
- 迁移 2 新增六张题目表，revision 防止并发覆盖，题面和数据版本独立递增；复合外键限定同题版本，创建、编辑、导入与审计同事务登记。旧版本和历史不物理删除。
- 复用 ZIP32 校验器，增加有界 multipart 导入、独立分值、并发槽和数据版本元数据接口；隐藏输入输出、物理路径和存储键不通过 HTTP 返回。
- 新增 problemdata 不可变文件存储：随机键独占目录、固定序号文件、有界 manifest、每文件 SHA-256 与整体指纹校验、暂存后原子发布、重启读取。已发布但数据库提交未知的目录不删除，自动孤儿清理留待受控维护。
- 新增 PROBLEM_DATA_DIR 与 PROBLEM_IMPORT_CONCURRENCY，Compose 独立持久卷与非 root 所有权；Nginx 对 ZIP 单独设置 34 MiB 上限并流式转发，保持 Web 先鉴权再读取正文。
- 安全审阅补充：CSRF 缺少 header 时不触发库的 multipart 表单回退；分值重复键/null/字符串/非整数拒绝；分块请求尾随 epilogue 同样计入 34 MiB 上限。客户端保留 int64 分值原文，避免 JavaScript 数字往返失真。
- 全量 go test -mod=readonly ./...、go vet -mod=readonly ./...、go build -mod=readonly ./...、go test -race -mod=readonly ./...：通过。新增 HTTP、服务事务与文件存储行为测试；文件符号链接用例在 Windows 无创建权限时跳过，POSIX 目录 fsync/权限仍需 Linux 验证。
- node --check webui/static/problems.js、node --test webui/tests/problems.test.cjs：通过，7 项页面行为测试；Go 页面 test/race/vet 通过。尚未进行浏览器实机联调。
- docker compose -f deploy/docker-compose.yml config --quiet：通过，使用临时进程验证值；Docker 用户配置读取有权限警告，未启动容器或运行真实迁移。
- README、API、数据库、解析器和实施计划已同步。后续 2C 接 Run/Submit 与 Outbox，2D 接 Worker 和独立沙箱；真实 MySQL/Redis、容器权限/重启恢复及 Linux 运行仍待验收。

## 2026-11-09 — 2C 提交与任务基础（已完成，本地验证）

- 迁移 3 新增 submissions、judge_tasks、judge_task_outbox、submission_results、submission_case_results 和 judge_admission；ExpectedVersion=3。固定题面和数据版本，账号复查、队列准入、提交、任务、Outbox 和审计在同一事务中保存。
- 接入 Run/Submit 创建、本人记录分页、受权限保护的详情和四语言列表。Run 使用样例或本人输入，始终不计分；Submit 必须存在测试数据，禁止返回正式测试输入/程序输出，编译错误以外的执行诊断不对外暴露。
- Run/Submit 分开按账号与 IP 原子限流，Redis 故障拒绝请求；全局准入行锁防止并发超出队列容量，JUDGE_QUEUE_CAPACITY 默认 1000、范围 1～100000。源码/自测输入各 64 KiB，JSON 上限 256 KiB，MEDIUMTEXT/BLOB 覆盖恰好 65536 字节的边界。
- 添加题面代码编辑与提交、本人历史/详情及退避轮询；CodeMirror 6 与四语言语法扩展以固定依赖打包到本地 editor.js，附完整依赖锁和第三方许可证，日常 Go/Docker 构建无需 Node。每请求 CSP nonce 授权编辑器样式，保留 textarea 降级。
- generation/attempt、12 状态与严格任务信封、执行协议类型已准备；2D 的 Redis 派发、Worker、租约/重试/死信及 Docker Runner 尚未实现。当前任务实际保存为 Pending，不生成模拟执行结果。
- 独立后端审阅未发现阻塞问题，覆盖公开题目版本固定、准入并发、事务回滚、账号状态复查、他人源码权限和正式输出过滤。

本次 Windows/amd64、Go 1.25.4，使用工作区 Go 缓存及 GOPROXY=off 验证：

| 检查 | 实际结果 |
| --- | --- |
| go test -mod=readonly ./... | 通过 |
| go test -race -mod=readonly ./... | 通过 |
| go vet -mod=readonly ./... | 通过 |
| go build -mod=readonly ./... | 通过 |
| go mod verify | 通过，all modules verified |
| Linux/amd64、Linux/arm64 的 web/ojctl 交叉构建（CGO_ENABLED=0） | 通过，仅验证构建 |
| docker compose -f deploy/docker-compose.yml config --quiet | 通过；用户 Docker 配置读取有权限警告，未启动容器 |
| gofmt -l cmd internal migrations webui | 无未格式化文件 |
| 提交文件清单及常见凭据模式检查 | 未发现真实环境配置、私钥、访问令牌、node_modules 或缓存产物 |
| node --check webui/static/submissions.js、node --check webui/static/editor.js | 通过 |
| node --test webui/tests/problems.test.cjs webui/tests/submissions.test.cjs | 通过，共 16 项页面行为测试 |

- 实际启动 Web 测试进程：题库/提交页面、editor.js、submissions.js 均返回 200；页面 nonce 与 CSP 一致。依赖不可用时 languages/CSRF/health 为 200、ready/业务提交为 503，API 404/405 保持正确；测试进程已终止，未计作优雅退出验收。
- 保留用户修改后的 plan.md，README、API、数据库和实施计划同步本地交付状态。真实 MySQL/Redis 约束与恢复、浏览器实机、容器及 Linux 运行仍待验收；下一批为 2D。
- 编辑器的 node_modules 同时从 Git 和 Docker 上下文忽略；生成的本地静态资源和许可证纳入源码。页面缓存恢复时重新校验会话，终态与离页停止轮询，并以界面测试验证正式输出不会显示。
