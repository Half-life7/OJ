# 修订日志

每批记录目标、实际变更、数据库/API/权限影响、验证命令与结果、遗留项。状态只使用：进行中、已完成、待验证。

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
