# OJ 实施计划

最新进度：**批次 3 已完成**，迁移版本为 6。身份与比赛功能、三种计分、固定版本提交、快照与幂等结算均已接入；真实数据库/沙箱链路、并发场景及浏览器验收见 [比赛说明](CONTESTS.md)。此前 2D 的四语言判题及故障恢复验收继续有效，原生 arm64 和生产整机验收保留在部署批次。

需求基线：根目录 `plan.md`。不修改或删减原始需求。开始日期：2026-11-09。

## 已确认的决定

- 首批完成工程基础和永久账号认证，分为 1A、1B 两个可验证批次。
- 学号属于 `persons`，可空且全局唯一；账号通过 `person_accounts` 关联身份。不得自动按姓名或学号合并身份。
- 暂缓真实 MySQL/Redis、Docker 判题和 Linux 部署验收；模拟测试不能代替真实环境验收。
- 使用 Go、Gin、sqlx、MySQL 8、go-redis v9、slog、Go Template 和原生 JavaScript。HTTP 服务底层基于 Go net/http，API 路由、中间件、参数绑定统一使用 Gin。

当前 HTTP 层使用 Gin v1.12.0，1C、2B、2C 及 2D 的代码与相关验证已推进完成。下一批进入批次 4；此前各批次历史验证记录保留，不用后来的通过结果覆盖当时的待验证状态。

## 批次路线

| 批次 | 内容 | 状态 | 建议模型 | 思考强度 |
| --- | --- | --- | --- | --- |
| 1A | 工程结构、环境配置、日志、数据库/Redis 接入、版本迁移、健康检查、开发 Compose | 本地验证通过，真实服务/容器待验 | GPT-6.1 Sol | high（高） |
| 1B | 永久账号注册/登录/退出/改密、身份与会话、管理员初始化、基础页面 | 本地验证通过，真实数据库/浏览器联调待验 | GPT-6.1 Sol | high（高） |
| 1C | Gin HTTP 层迁移：路由、中间件、参数绑定与兼容性回归 | 本地验证通过，真实服务/容器待验 | GPT-6.1 Sol | high（高） |
| 2 | 题目与不可变数据版本、ZIP 安全导入、Run/Submit、Outbox、Worker、独立 Runner、C++17/Go/Python3/Java 沙箱 | 已完成；真实 MySQL/Redis/Linux amd64 判题及故障恢复通过，原生 arm64/生产部署另列后续 | GPT-6 Astra | high（高） |
| 3 | 学号审核与纠错、身份管理、比赛与队伍、报名、正式/打星/退赛、独立 OI/ACM/IOI 引擎、快照与幂等结算 | 已完成；真实 API/SQL、Python 沙箱链路、并发结算及 Edge 页面验收通过，详见 CONTESTS.md | GPT-6 Astra | high（高） |
| 4 | 日常练习、题单、讨论、题解、日常解题/比赛积分/题解发布三个全局榜 | 待开始 | GPT-6.1 Sol | high（高） |
| 5 | CSV、封榜、完整管理后台、监控、安装升级、备份恢复、Linux 部署及运维文档 | 待开始 | GPT-6 Astra | high（高） |
| 6 | SSE、Special Judge、重判管理等 P2 扩展 | 待开始 | GPT-6 Astra | high（高） |

必要审计、题目数据版本、generation/attempt 与成绩重建基础随相关模块实现，不推迟至管理页面完善时补做。MySQL 是事实来源；Redis 可恢复。判题必须采用事务 Outbox，先保存结果再 ACK，防止重复与迟到任务覆盖。Web/Worker 不获得 Docker Socket。后续批次在编码前补齐该批接口、数据库、权限和验收细案。

## 模型与思考强度建议（2026-11-10）

上述配置及下文各节点建议是依据任务复杂性作出的项目建议，不代表历史批次实际使用过这些模型，也不改变原有需求、批次顺序或验收状态。选型参考 [OpenAI 官方模型选择指南](https://developers.openai.com/api/docs/guides/model-selection) 与 [思考强度说明](https://developers.openai.com/api/docs/guides/reasoning)；具体可选项以当前客户端为准。

- GPT-6.1 Sol（`gpt-6.1-sol`）配合 high（高）：用于需求清晰、需要跨数据库/API/页面协同的常规实现与回归。
- GPT-6 Astra（`gpt-6-astra`）配合 high（高）：用于身份与权限建模、三种独立计分、幂等结算、任务一致性、沙箱隔离和部署恢复等复杂节点。
- 批次 4 中已明确接口与权限的简单页面、CRUD、文档整理，可使用 GPT-6.1 Terra（`gpt-6.1-terra`）配合 medium（中）；涉及全局榜去重、积分归属或成绩重建时使用该批默认配置。
- 批次 6 中单独实现 SSE 通知，可使用 GPT-6.1 Sol 配合 medium（中）；Special Judge、重判及成绩重建使用该批默认配置。
- 暂不默认使用 xhigh（超高）或 max（最高）；只有代表性难例验证出收益后再提高强度。

**批次 3 已完成。下一批次 4 建议 GPT-6.1 Sol + high（高）；实际交付与验收见 [比赛说明](CONTESTS.md)。**

## 1A 实现与验收

建议模型：**GPT-6.1 Sol**；思考强度：**high（高）**。重点覆盖配置、迁移、依赖恢复与容器边界。

- 建立 Web 与 `ojctl` 管理入口，内部按配置、存储、HTTP 和业务职责分包。
- 环境变量配置、生产密钥校验、脱敏 slog、请求 ID、超时与优雅退出。
- MySQL/sqlx 与 Redis 客户端采用连接池和有限超时；依赖失效时 HTTP 仍可查询健康状态。
- `/health` 仅判断进程；`/ready` 检查 MySQL、Redis、期望迁移版本和 dirty 状态；故障返回 503，恢复后自动就绪。
- golang-migrate/v4 执行嵌入的版本化 SQL。重复迁移成功，dirty 不自动 Force。业务连接不开 multiStatements，迁移使用独立连接。
- 多阶段、非 root Dockerfile；开发 Compose 包含 MySQL、Redis、迁移步骤、Web、Nginx，固定版本、健康检查、数据持久化，不开放数据库公网端口。
- 验证配置、健康状态、错误映射和构建。真实启动与迁移验收等待可用环境。

## 1B 数据、API 与权限

建议模型：**GPT-6.1 Sol**；思考强度：**high（高）**。重点覆盖认证事务、会话撤销、CSRF 与权限回归。

首次迁移包含 users、persons、person_accounts、student_no_history、auth_sessions、audit_logs；统一使用 InnoDB、utf8mb4、BIGINT 主键与 UTC 时间。外键不级联删除历史。

- users 保存账号、bcrypt 哈希、类型、全局角色、状态和 token_version。账号规范化为小写，3～32 个 ASCII 字母/数字/下划线，拒绝保留名。
- persons 保存显示名称、可空唯一学号、none/pending/verified/rejected 状态。空学号转 NULL，注册填写为 pending。
- 注册事务创建账号、身份、关联、初次学号历史及审计；唯一冲突整笔回滚。
- auth_sessions 持久化 jti 摘要、有效期和撤销状态。JWT HS256 有效期默认 12 小时，验证算法、issuer、audience、exp、jti。每次鉴权检查账号状态、token_version 和会话；Redis 不作为会话撤销唯一依据。
- 密码 8～72 UTF-8 字节，bcrypt cost 默认 12；改密校验旧密码，在同一事务更新哈希、token_version 和审计，所有旧会话失效。
- HttpOnly、SameSite=Lax Cookie；Secure 与 PUBLIC_URL 一致。gorilla/csrf 保护所有写接口。Redis 按 IP 和账号限流，故障不静默放行。代理来源仅信任显式配置。
- 初始化超级管理员使用独立命令；密码不放命令参数或日志，重复执行不得重置或自动提权同名普通账号。

| 方法 | 路径 | 请求/用途 |
| --- | --- | --- |
| GET | /api/auth/csrf | 获取 CSRF token |
| POST | /api/auth/register | username、password、password_confirmation、可选 student_no |
| POST | /api/auth/login | username、password；设置认证 Cookie |
| POST | /api/auth/logout | 幂等撤销当前会话 |
| POST | /api/auth/password | current_password、password、password_confirmation |
| GET | /api/users/me | 本人账号与身份，不返回密码哈希或会话密钥 |

统一响应 `{code,message,data}`；区分 400/401/403/409/429/503，并隐藏内部错误。首批页面：/、/register、/login、/me；按钮对接真实 API，具备加载和错误状态。未实现的判题/比赛不提供伪接口或可用性承诺。

## 1C Gin HTTP 层迁移（本地验证完成）

建议模型：**GPT-6.1 Sol**；思考强度：**high（高）**。重点保持 HTTP 兼容性并回归完整认证流程。

- 将 Web 路由、认证 HTTP 处理、API 注册接口及相关测试迁至 Gin，更新并锁定依赖，移除 chi 依赖。业务服务与数据库事务保持既有职责边界。
- API 路由、中间件、参数绑定统一使用 Gin；保留现有接口路径、请求字段、响应结构、状态码和 Cookie 约定，以及 MPA 页面和静态资源访问行为。
- 将请求 ID、脱敏日志、异常恢复、超时、就绪检查、可信代理与来源校验集成至 Gin；集成现有 gorilla/csrf 保护，保持会话验证、退出与改密失效、Redis 限流及依赖故障返回行为。
- 参数绑定保持严格 JSON 对象校验：16 KiB 请求体上限、拒绝未知字段、多份 JSON、错误类型及非法 UTF-8；退出继续允许无请求体或空对象。
- 回归认证全流程、Cookie/CSRF、错误 Origin、伪造代理头、请求 ID 与日志脱敏、依赖不可用时的健康检查，以及 HEAD、API 404/405 和页面路由行为。
- 按下述检查规则执行全量 test/vet/build/race；Gin 迁移结果单独记录，既有通过记录不作为迁移验收。完成后再细化并实施 2B，真实服务与部署验收仍按既有约定保留。

## 检查与交付规则

- 每小批完成执行 go test ./...、go vet ./...、go build ./...；环境支持时执行 race 检查及 Compose 静态校验。
- httptest 验证完整认证流程、Cookie、CSRF、错误 Origin、越权、过期/篡改 JWT、退出后重放与改密失效。
- sqlmock 验证事务提交与回滚；miniredis 验证真实 go-redis 客户端的限流与故障处理；不能据此宣称 MySQL 约束和真实 Redis 已验收。
- 待真实环境：空库及重复迁移、dirty 拒绝、并发唯一约束、依赖中断恢复、Compose 空目录启动与重启数据保留，以及后续 Linux/amd64 全验收和 arm64 构建启动。
- 每次修改同步 CHANGELOG.md；记录准确命令和结果。不能将未运行项标为通过。首批完成后更新 README、API 与数据库说明，交代剩余范围。

## 首次编码后的修订（2026-11-09）

- 1A/1B 的源代码、页面、迁移及开发配置已落盘；配置和页面可独立执行的测试已通过。
- 全量构建/测试/静态检查尚未进入编译：当前网络不允许首次下载所需 Go 模块，缺少 go.sum。此限制不同于已约定暂缓的真实数据库/Docker 验收，需先解决依赖获取才能完成首批验收。
- 首批基础和认证按固定接口并行实现；验收仍分别记录，未用模拟模块代替真实存储实现。
- 下一执行入口：网络可用后 go mod tidy，保存生成的间接依赖和 go.sum，再执行全量 test/vet/build/race；排除失败后才将首批标记完成，并进入批次 2。

## 继续执行的修订与小批次 2A（2026-11-09）

依赖获取已找到正常授权范围内的 curl HTTPS GET 路径，使用官方模块包和 sum.golang.org 校验记录补齐工作区缓存，保持校验开启；首批状态待全量检查后更新。下载期间并行开发一个不依赖数据库、认证接线或容器的标准库模块，单独验收，不代表完整批次 2 完成。

当前验证更新：原 10 个直接依赖不变，Go 补齐 7 个间接依赖并保存 go.sum。tidy、模块校验、全量单元测试、vet、build、认证/配置/页面/服务/存储的 race 均已通过；两个入口已交叉编译 Linux/amd64 和 Linux/arm64。真实进程 smoke 确认依赖不可用时 /health=200、/ready=503、注册页面与 CSRF=200，测试进程已关闭。此结果不代表真实数据库、容器或跨平台运行已验收。

### 2A：内存测试数据 ZIP 校验器

建议模型：**GPT-6.1 Sol**；思考强度：**high（高）**。重点覆盖 ZIP 边界、有界解压、完整性校验与取消场景。

- 新增 internal/testcase：Parse(ctx, io.ReaderAt, size, scores, limits) 返回测试点列表、内容摘要、展开体积和总分；不写文件，不连接数据库，不生成 data_version，不改变现有 API。
- 格式限定 ZIP32、单卷、无自解压前缀或尾随垃圾，根目录为成对 `<id>.in`/`<id>.out`。ID 为 1～64 位 ASCII 字母/数字/下划线/连字符且首位字母或数字，拒绝 Windows 保留设备名、大小写折叠冲突、未知成员、目录、链接、危险路径、加密和非 Store/Deflate 压缩。
- scores 由调用者独立传入并精确覆盖 case ID，非负 int64，允许零分点但总分须大于零，溢出拒绝。输入/输出原始字节保持不变，支持空内容。
- 默认限制：压缩包 32 MiB、单成员 16 MiB、总展开 64 MiB、512 个测试点、中央目录 1 MiB；Limits 显式传入，可配置但拒绝无效值。
- 在 archive/zip 分配条目前预检中央目录体积与实际条目数量；解压时同时限制声明和实际字节数，读至 EOF 校验 CRC，处理取消与读取失败，错误时不返回部分结果。
- 测试点按 ID 字节排序；SHA-256 指纹包含域标识和长度分隔的 ID、输入、输出、分值，不受 ZIP 条目顺序、时间戳或压缩方式影响。
- 验收：标准库单测、race、vet，以及配对/分值/路径/CRC/体积/伪造目录与尺寸/取消场景；附无 panic fuzz 用例。上传接口、持久化、不可变版本发布和比赛版本锁定保留后续细案。

2A 验证更新：单测、race、vet 及 5 秒双 worker fuzz 通过；安全审阅补充无 data descriptor 且 CRC 为零的显式校验回归。加入 2A 后全量 readonly test/race/vet/build 和模块校验再次通过。当时计划下一批 2B 实现题目管理与不可变测试数据版本的数据库/API/页面接线；其迁移、权限、上传存储和失败补偿在编码前细化。

## Gin 选型同步（2026-11-09）

新版 plan.md 将 Web 框架调整为 Gin，HTTP 服务底层仍基于 Go net/http，API 路由、中间件和参数绑定统一使用 Gin。当时仅同步计划与说明文档，代码和依赖仍为 chi，安排先完成 1C 并重新验收 HTTP 层，再继续 2B；此前历史验证结果保留。

## 1C 实现与本地验收（2026-11-09）

- 已将服务器、认证 HTTP 处理和测试迁至 Gin，API 注册接口统一为 `func(*gin.RouterGroup)`；HTTP 服务继续由 net/http 管理生命周期。Gin v1.12.0 与依赖已锁定，业务事务和数据库迁移未变。
- 使用 Gin 自定义绑定器保留严格 JSON/UTF-8/16 KiB 校验；Gin 原生中间件集成来源校验、现有 CSRF、可信代理、请求 ID、超时和脱敏日志。关闭 Gin 自动路径重定向及默认代理头信任，保留 API 404/405、HEAD、页面与静态资源行为。
- go mod tidy、go mod verify，以及全量 readonly test/vet/build/race 均通过。请求体边界、会话与 Cookie、HTTP/HTTPS CSRF、依赖恢复、代理头、编码路径、错误响应及日志脱敏已回归。
- Web 与 ojctl 已交叉编译 Linux/amd64、Linux/arm64；真实 Web 进程在依赖不可用时提供健康检查、页面、静态资源及 CSRF，业务 API 返回 503，测试进程已关闭。此结果不代表真实服务、容器、Linux 运行或优雅退出已验收。
- 下一批 2B：先细化题目与不可变测试数据版本的数据库/API/权限/存储及失败补偿，再实现接线。

## 2B 实施细案（本地验证完成）

建议模型：**GPT-6.1 Sol**；思考强度：**high（高）**。重点覆盖不可变版本、并发修订与文件/数据库失败补偿。

细案见 [题目与版本管理](PROBLEMS.md)。本批交付公开题库、超级管理员创建/编辑/下架、不可变题面快照、ZIP 导入和数据版本历史；仅返回公开题面与管理元数据，隐藏输入输出不通过 HTTP 暴露。

- 新增迁移 2，使用 revision 防止并发覆盖，题面与数据版本独立递增；审计与版本指针同事务提交，保留所有历史。
- 已校验的数据先以随机键原子发布到专用目录，再登记数据库；数据库失败或提交结果未知时不删除已发布目录，孤儿清理留待受控维护。
- 新增 PROBLEM_DATA_DIR、导入并发限制及 Compose 持久卷；Gin、Nginx 和浏览器同步设置有界上传，先鉴权再读取正文。
- 验证权限/可见性、并发修订、版本不可变、事务回滚、损坏/超大 ZIP、文件失败与取消、重启读取，以及 MPA 与认证回归。真实数据库和 Docker 验收仍单列待验证。

本地验证：全量 test/vet/build/race 通过，页面脚本行为测试与 Compose 静态配置通过。安全审阅补充缺少 CSRF header 时不读取 multipart 正文、重复分值键拒绝、未知长度上传尾随内容计入请求上限等回归。具体命令和限制见修订日志。

## 2C 提交与任务基础（本地验证完成）

建议模型：**GPT-6.1 Sol**；思考强度：**high（高）**。重点覆盖固定版本、队列准入与 Outbox 事务一致性；2D Worker/Runner/沙箱建议使用 **GPT-6 Astra + high（高）**。

细案见 [提交与任务基础](SUBMISSIONS.md)。本批增加真实 Run/Submit 记录、固定题面/数据版本、独立限流、队列准入、事务 Outbox、任务信封/状态机和本人提交页面；Worker 与独立 Runner 在 2D 接入，未执行的任务保持待处理状态。

- 迁移 3 保存提交、任务、Outbox、按轮次结果及准入锁，外键保留历史；generation/attempt 为后续租约和迟到结果保护提供基础。
- Run 只接样例或自定义输入，正式提交不返回隐藏测试输入输出；源码和本人记录有后端权限校验。
- CodeMirror 6 以固定依赖生成本地静态资产；日常运行无需 Node 构建，动态样式使用每请求 CSP nonce，不放宽脚本策略。

全量 Go 单测、race、vet、构建与模块校验均通过；页面与静态资源、提交行为、权限、限流、版本固定及事务回滚已本地验证。当前只保存真实 Pending 任务，2D 尚未接入消费和执行，不将本批计为真实判题完成。详细命令和结果见 CHANGELOG.md。
