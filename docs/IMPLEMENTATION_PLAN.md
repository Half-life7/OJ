# OJ 实施计划

需求基线：根目录 `plan.md`。不修改或删减原始需求。开始日期：2026-11-09。

## 已确认的决定

- 首批完成工程基础和永久账号认证，分为 1A、1B 两个可验证批次。
- 学号属于 `persons`，可空且全局唯一；账号通过 `person_accounts` 关联身份。不得自动按姓名或学号合并身份。
- 暂缓真实 MySQL/Redis、Docker 判题和 Linux 部署验收；模拟测试不能代替真实环境验收。
- 使用 Go、net/http、chi、sqlx、MySQL 8、go-redis v9、slog、Go Template 和原生 JavaScript。

## 批次路线

| 批次 | 内容 | 状态 |
| --- | --- | --- |
| 1A | 工程结构、环境配置、日志、数据库/Redis 接入、版本迁移、健康检查、开发 Compose | 本地验证通过，真实服务/容器待验 |
| 1B | 永久账号注册/登录/退出/改密、身份与会话、管理员初始化、基础页面 | 本地验证通过，真实数据库/浏览器联调待验 |
| 2 | 题目与不可变数据版本、ZIP 安全导入、Run/Submit、Outbox、Worker、独立 Runner、C++17/Go/Python3/Java 沙箱 | 2A 独立校验器本地验证通过，其余待开始 |
| 3 | 学号审核与纠错、身份管理、比赛与队伍、报名、正式/打星/退赛、独立 OI/ACM/IOI 引擎、快照与幂等结算 | 待开始 |
| 4 | 日常练习、题单、讨论、题解、日常解题/比赛积分/题解发布三个全局榜 | 待开始 |
| 5 | CSV、封榜、完整管理后台、监控、安装升级、备份恢复、Linux 部署及运维文档 | 待开始 |
| 6 | SSE、Special Judge、重判管理等 P2 扩展 | 待开始 |

必要审计、题目数据版本、generation/attempt 与成绩重建基础随相关模块实现，不推迟至管理页面完善时补做。MySQL 是事实来源；Redis 可恢复。判题必须采用事务 Outbox，先保存结果再 ACK，防止重复与迟到任务覆盖。Web/Worker 不获得 Docker Socket。后续批次在编码前补齐该批接口、数据库、权限和验收细案。

## 1A 实现与验收

- 建立 Web 与 `ojctl` 管理入口，内部按配置、存储、HTTP 和业务职责分包。
- 环境变量配置、生产密钥校验、脱敏 slog、请求 ID、超时与优雅退出。
- MySQL/sqlx 与 Redis 客户端采用连接池和有限超时；依赖失效时 HTTP 仍可查询健康状态。
- `/health` 仅判断进程；`/ready` 检查 MySQL、Redis、期望迁移版本和 dirty 状态；故障返回 503，恢复后自动就绪。
- golang-migrate/v4 执行嵌入的版本化 SQL。重复迁移成功，dirty 不自动 Force。业务连接不开 multiStatements，迁移使用独立连接。
- 多阶段、非 root Dockerfile；开发 Compose 包含 MySQL、Redis、迁移步骤、Web、Nginx，固定版本、健康检查、数据持久化，不开放数据库公网端口。
- 验证配置、健康状态、错误映射和构建。真实启动与迁移验收等待可用环境。

## 1B 数据、API 与权限

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

- 新增 internal/testcase：Parse(ctx, io.ReaderAt, size, scores, limits) 返回测试点列表、内容摘要、展开体积和总分；不写文件，不连接数据库，不生成 data_version，不改变现有 API。
- 格式限定 ZIP32、单卷、无自解压前缀或尾随垃圾，根目录为成对 `<id>.in`/`<id>.out`。ID 为 1～64 位 ASCII 字母/数字/下划线/连字符且首位字母或数字，拒绝 Windows 保留设备名、大小写折叠冲突、未知成员、目录、链接、危险路径、加密和非 Store/Deflate 压缩。
- scores 由调用者独立传入并精确覆盖 case ID，非负 int64，允许零分点但总分须大于零，溢出拒绝。输入/输出原始字节保持不变，支持空内容。
- 默认限制：压缩包 32 MiB、单成员 16 MiB、总展开 64 MiB、512 个测试点、中央目录 1 MiB；Limits 显式传入，可配置但拒绝无效值。
- 在 archive/zip 分配条目前预检中央目录体积与实际条目数量；解压时同时限制声明和实际字节数，读至 EOF 校验 CRC，处理取消与读取失败，错误时不返回部分结果。
- 测试点按 ID 字节排序；SHA-256 指纹包含域标识和长度分隔的 ID、输入、输出、分值，不受 ZIP 条目顺序、时间戳或压缩方式影响。
- 验收：标准库单测、race、vet，以及配对/分值/路径/CRC/体积/伪造目录与尺寸/取消场景；附无 panic fuzz 用例。上传接口、持久化、不可变版本发布和比赛版本锁定保留后续细案。

2A 验证更新：单测、race、vet 及 5 秒双 worker fuzz 通过；安全审阅补充无 data descriptor 且 CRC 为零的显式校验回归。加入 2A 后全量 readonly test/race/vet/build 和模块校验再次通过。下一批 2B 实现题目管理与不可变测试数据版本的数据库/API/页面接线；其迁移、权限、上传存储和失败补偿在编码前细化。
